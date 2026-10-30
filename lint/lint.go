// Package lint grades an A2A agent card against the A2A 1.0 AgentCard definition.
//
// It reads the raw JSON instead of a decoded a2a.AgentCard. The most common real-world
// failures (an array where an object belongs, a 0.x field layout) are exactly the ones
// a typed decoder either rejects with one opaque error or silently drops.
package lint

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// Severity says whether a finding breaks clients or only weakens the card.
type Severity string

const (
	// SeverityError means an A2A 1.0 client can't reliably consume the card.
	SeverityError Severity = "error"
	// SeverityWarning means the card is usable but misses something the spec asks for.
	SeverityWarning Severity = "warning"
)

// Finding is one problem found in a card.
type Finding struct {
	Severity Severity `json:"severity"`
	// Path points at the offending field, for example "skills[0].tags".
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Grade summarizes a Report.
type Grade string

const (
	// GradeConformant means no findings at all.
	GradeConformant Grade = "conformant"
	// GradeNearConformant means only warnings.
	GradeNearConformant Grade = "near-conformant"
	// GradeNonConformant means at least one error.
	GradeNonConformant Grade = "non-conformant"
)

// Report is the result of checking one card.
type Report struct {
	Grade    Grade     `json:"grade"`
	Findings []Finding `json:"findings"`
}

// Count returns the number of findings with the given severity.
func (r Report) Count(s Severity) int {
	n := 0
	for _, f := range r.Findings {
		if f.Severity == s {
			n++
		}
	}
	return n
}

// Check grades the raw JSON of an agent card.
func Check(raw []byte) Report {
	var c checker
	c.check(raw)
	return newReport(c.findings)
}

func newReport(findings []Finding) Report {
	slices.SortStableFunc(findings, func(a, b Finding) int {
		if a.Severity != b.Severity {
			if a.Severity == SeverityError {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Path, b.Path)
	})
	r := Report{Grade: GradeConformant, Findings: findings}
	if r.Findings == nil {
		r.Findings = []Finding{}
	}
	switch {
	case r.Count(SeverityError) > 0:
		r.Grade = GradeNonConformant
	case r.Count(SeverityWarning) > 0:
		r.Grade = GradeNearConformant
	}
	return r
}

type checker struct {
	findings []Finding
}

func (c *checker) errorf(path, format string, args ...any) {
	c.findings = append(c.findings, Finding{Severity: SeverityError, Path: path, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) warnf(path, format string, args ...any) {
	c.findings = append(c.findings, Finding{Severity: SeverityWarning, Path: path, Message: fmt.Sprintf(format, args...)})
}

func (c *checker) check(raw []byte) {
	var card map[string]json.RawMessage
	if err := json.Unmarshal(raw, &card); err != nil {
		c.errorf("$", "the card is not a JSON object: %v", err)
		return
	}

	for _, field := range []string{"name", "description", "version"} {
		c.requireString(field, card[field])
	}
	c.checkInterfaces(card["supportedInterfaces"])
	c.checkCapabilities(card["capabilities"])
	for _, field := range []string{"defaultInputModes", "defaultOutputModes"} {
		c.checkModes(field, card[field])
	}
	c.checkSkills(card["skills"])
	c.checkLegacyFields(card)

	// A typed decode only adds information once the shape checks pass.
	if !c.hasErrors() {
		var typed a2a.AgentCard
		if err := json.Unmarshal(raw, &typed); err != nil {
			c.errorf("$", "a2a-go cannot decode the card: %v", err)
		}
	}
}

func (c *checker) hasErrors() bool {
	return slices.ContainsFunc(c.findings, func(f Finding) bool { return f.Severity == SeverityError })
}

func (c *checker) requireString(path string, raw json.RawMessage) {
	switch k := kindOf(raw); k {
	case "missing":
		c.errorf(path, "%s is required", path)
	case "string":
		if s, _ := decodeString(raw); s == "" {
			c.errorf(path, "%s must not be empty", path)
		}
	default:
		c.errorf(path, "%s must be a string, got %s", path, k)
	}
}

func (c *checker) checkInterfaces(raw json.RawMessage) {
	const path = "supportedInterfaces"
	items, ok := c.array(path, raw, "a 0.x card declares url + preferredTransport instead; 1.0 clients look here")
	if !ok {
		return
	}
	if len(items) == 0 {
		c.errorf(path, "supportedInterfaces must list at least one interface")
	}
	for i, item := range items {
		p := fmt.Sprintf("%s[%d]", path, i)
		iface, ok := c.object(p, item)
		if !ok {
			continue
		}
		c.requireString(p+".url", iface["url"])
		if s, err := decodeString(iface["url"]); err == nil && s != "" {
			if u, err := url.Parse(s); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
				c.errorf(p+".url", "url must be an absolute http(s) URL, got %q", s)
			} else if u.Scheme == "http" && !isLocalHost(u.Hostname()) {
				c.warnf(p+".url", "production agents must use HTTPS (spec 7.1)")
			}
		}
		c.requireString(p+".protocolBinding", iface["protocolBinding"])
		if s, err := decodeString(iface["protocolBinding"]); err == nil && s != "" && !isKnownBinding(s) {
			c.warnf(p+".protocolBinding", "%q is not a standard binding (JSONRPC, GRPC, HTTP+JSON); only clients with a matching custom binding can call it", s)
		}
		c.requireString(p+".protocolVersion", iface["protocolVersion"])
	}
}

func (c *checker) checkCapabilities(raw json.RawMessage) {
	const path = "capabilities"
	caps, ok := c.object(path, raw)
	if !ok {
		return
	}
	for _, field := range []string{"streaming", "pushNotifications", "extendedAgentCard"} {
		if k := kindOf(caps[field]); k != "missing" && k != "boolean" {
			c.errorf(path+"."+field, "%s must be a boolean, got %s", field, k)
		}
	}
	if kindOf(caps["extensions"]) == "missing" {
		return
	}
	exts, ok := c.array(path+".extensions", caps["extensions"], "")
	if !ok {
		return
	}
	for i, ext := range exts {
		p := fmt.Sprintf("%s.extensions[%d]", path, i)
		if fields, ok := c.object(p, ext); ok {
			c.requireString(p+".uri", fields["uri"])
		}
	}
}

func (c *checker) checkModes(path string, raw json.RawMessage) {
	if kindOf(raw) == "missing" {
		c.warnf(path, "%s is required by the spec; without it clients have to guess media types", path)
		return
	}
	items, ok := c.array(path, raw, "")
	if !ok {
		return
	}
	for i, item := range items {
		if kindOf(item) != "string" {
			c.errorf(fmt.Sprintf("%s[%d]", path, i), "media types must be strings")
		}
	}
}

func (c *checker) checkSkills(raw json.RawMessage) {
	const path = "skills"
	items, ok := c.array(path, raw, "")
	if !ok {
		return
	}
	if len(items) == 0 {
		c.warnf(path, "the card lists no skills, so registries and orchestrators can't tell what it does")
	}
	for i, item := range items {
		p := fmt.Sprintf("%s[%d]", path, i)
		skill, ok := c.object(p, item)
		if !ok {
			continue
		}
		for _, field := range []string{"id", "name", "description"} {
			c.requireString(p+"."+field, skill[field])
		}
		switch k := kindOf(skill["tags"]); k {
		case "missing":
			c.warnf(p+".tags", "tags is required by the spec and is what registries search on")
		case "array":
		default:
			c.errorf(p+".tags", "tags must be an array of strings, got %s", k)
		}
	}
}

// legacyFields are 0.x card fields that 1.0 clients ignore (spec Appendix A).
var legacyFields = map[string]string{
	"url":                               "use supportedInterfaces[].url",
	"preferredTransport":                "use supportedInterfaces[].protocolBinding",
	"additionalInterfaces":              "use supportedInterfaces",
	"protocolVersion":                   "use supportedInterfaces[].protocolVersion",
	"supportsAuthenticatedExtendedCard": "use capabilities.extendedAgentCard",
}

func (c *checker) checkLegacyFields(card map[string]json.RawMessage) {
	for _, field := range slices.Sorted(maps.Keys(legacyFields)) {
		if kindOf(card[field]) != "missing" {
			c.warnf(field, "%s is a 0.x field that 1.0 clients ignore; %s", field, legacyFields[field])
		}
	}
}

func (c *checker) array(path string, raw json.RawMessage, hint string) ([]json.RawMessage, bool) {
	k := kindOf(raw)
	if k == "missing" {
		if hint != "" {
			c.errorf(path, "%s is required (%s)", path, hint)
		} else {
			c.errorf(path, "%s is required", path)
		}
		return nil, false
	}
	if k != "array" {
		c.errorf(path, "%s must be an array, got %s", path, k)
		return nil, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		c.errorf(path, "%s is not a valid array: %v", path, err)
		return nil, false
	}
	return items, true
}

func (c *checker) object(path string, raw json.RawMessage) (map[string]json.RawMessage, bool) {
	k := kindOf(raw)
	if k == "missing" {
		c.errorf(path, "%s is required", path)
		return nil, false
	}
	if k != "object" {
		c.errorf(path, "%s must be an object, got %s", path, k)
		return nil, false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		c.errorf(path, "%s is not a valid object: %v", path, err)
		return nil, false
	}
	return fields, true
}

// kindOf names the JSON type of a raw value, or "missing" when the field is absent.
func kindOf(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "missing"
	}
	switch raw[0] {
	case '{':
		return "object"
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}

func decodeString(raw json.RawMessage) (string, error) {
	var s string
	err := json.Unmarshal(raw, &s)
	return s, err
}

func isKnownBinding(s string) bool {
	switch a2a.TransportProtocol(s) {
	case a2a.TransportProtocolJSONRPC, a2a.TransportProtocolGRPC, a2a.TransportProtocolHTTPJSON:
		return true
	}
	return false
}

func isLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}
