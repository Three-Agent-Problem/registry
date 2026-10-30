// Package registry holds the types shared by the registry server and its client.
//
// The registry is an index, not the source of truth. It records where an agent's card
// lives and how good it is, while clients still fetch the card from the agent's own
// domain before they call it.
package registry

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/three-agent-problem/registry/lint"
)

// Entry is one registered agent.
type Entry struct {
	// URL is the base URL the agent was registered with.
	URL string `json:"url"`
	// CardURL is where the card was actually served.
	CardURL string `json:"cardUrl"`
	// Card is nil when the card could not be decoded.
	Card   *a2a.AgentCard `json:"card,omitempty"`
	Report lint.Report    `json:"report"`

	Registered time.Time `json:"registered"`
	LastSeen   time.Time `json:"lastSeen"`
	LatencyMS  int64     `json:"latencyMs"`
	Up         bool      `json:"up"`
	LastError  string    `json:"lastError,omitempty"`
}

// Name returns the card name, or the URL when there is no card.
func (e *Entry) Name() string {
	if e.Card != nil && e.Card.Name != "" {
		return e.Card.Name
	}
	return e.URL
}

// Query filters a search. Empty fields match everything.
type Query struct {
	// Skill matches a skill ID or a skill tag.
	Skill string
	// Text matches the agent name or description, case-insensitively.
	Text string
	// IncludeNonConformant also returns agents whose card has errors.
	IncludeNonConformant bool
	// IncludeDown also returns agents that failed their last liveness check.
	IncludeDown bool
}

// Matches reports whether the entry satisfies the query.
func (q Query) Matches(e *Entry) bool {
	if e.Card == nil {
		return false
	}
	if !q.IncludeNonConformant && e.Report.Grade == lint.GradeNonConformant {
		return false
	}
	if !q.IncludeDown && !e.Up {
		return false
	}
	if q.Text != "" {
		text := strings.ToLower(q.Text)
		if !strings.Contains(strings.ToLower(e.Card.Name), text) && !strings.Contains(strings.ToLower(e.Card.Description), text) {
			return false
		}
	}
	if q.Skill != "" {
		return slices.ContainsFunc(e.Card.Skills, func(s a2a.AgentSkill) bool {
			return s.ID == q.Skill || slices.Contains(s.Tags, q.Skill)
		})
	}
	return true
}

// gradeRank orders grades from best to worst.
var gradeRank = map[lint.Grade]int{
	lint.GradeConformant:     0,
	lint.GradeNearConformant: 1,
	lint.GradeNonConformant:  2,
}

// LatencyBandMS is the latency resolution used for ranking: agents within the same
// band are ordered by name, so a few milliseconds of jitter do not reshuffle results.
const LatencyBandMS = 100

// Compare orders search results: best grade first, then lowest latency band, then name.
func Compare(a, b *Entry) int {
	if d := gradeRank[a.Report.Grade] - gradeRank[b.Report.Grade]; d != 0 {
		return d
	}
	if d := cmp.Compare(a.LatencyMS/LatencyBandMS, b.LatencyMS/LatencyBandMS); d != 0 {
		return d
	}
	return strings.Compare(a.Name(), b.Name())
}
