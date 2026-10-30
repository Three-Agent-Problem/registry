package lint

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// WellKnownPath is where A2A 1.0 clients look for a card (spec 8.2, 14).
	WellKnownPath = "/.well-known/agent-card.json"
	// LegacyWellKnownPath is the pre-1.0 location that 1.0 clients don't check.
	LegacyWellKnownPath = "/.well-known/agent.json"

	maxCardBytes = 1 << 20
)

// Fetched is a card as it was served.
type Fetched struct {
	// CardURL is the URL that served the card.
	CardURL string
	Raw     []byte
	Latency time.Duration
	// Report already includes findings about where the card was served.
	Report Report
}

// Fetch downloads and checks the card for an agent. target is either the agent's base URL,
// in which case the well-known path is tried and then the legacy path, or a full card URL.
func Fetch(ctx context.Context, client *http.Client, target string) (*Fetched, error) {
	if client == nil {
		client = http.DefaultClient
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid agent URL %q", target)
	}

	if u.Path != "" && u.Path != "/" {
		return fetchOne(ctx, client, target, nil)
	}

	base := strings.TrimSuffix(target, "/")
	fetched, err := fetchOne(ctx, client, base+WellKnownPath, nil)
	if err == nil {
		return fetched, nil
	}
	legacy, legacyErr := fetchOne(ctx, client, base+LegacyWellKnownPath, &Finding{
		Severity: SeverityError,
		Path:     "$",
		Message:  "the card is only served at " + LegacyWellKnownPath + ", which A2A 1.0 clients don't check; serve it at " + WellKnownPath,
	})
	if legacyErr != nil {
		return nil, err
	}
	return legacy, nil
}

func fetchOne(ctx context.Context, client *http.Client, cardURL string, extra *Finding) (*Fetched, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cardURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", cardURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxCardBytes))
	latency := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cardURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", cardURL, resp.Status)
	}

	var c checker
	c.check(raw)
	if extra != nil {
		c.findings = append(c.findings, *extra)
	}
	return &Fetched{CardURL: cardURL, Raw: raw, Latency: latency, Report: newReport(c.findings)}, nil
}
