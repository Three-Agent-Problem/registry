package lint

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const validCard = `{
  "name": "Northwind Merch",
  "description": "Quotes and fulfils branded merchandise orders",
  "version": "1.0.0",
  "supportedInterfaces": [{"url": "https://agents.northwind.example/a2a", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}],
  "capabilities": {"streaming": true, "pushNotifications": true},
  "defaultInputModes": ["text/plain"],
  "defaultOutputModes": ["application/json"],
  "skills": [{"id": "quote", "name": "Quote", "description": "Price an order", "tags": ["quote", "merch"]}]
}`

// replace swaps one exact substring of validCard, so each case shows only what it breaks.
func replace(old, new string) string {
	if !strings.Contains(validCard, old) {
		panic("test fixture does not contain " + old)
	}
	return strings.Replace(validCard, old, new, 1)
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name      string
		card      string
		wantGrade Grade
		wantPath  string // a finding at this path must exist ("" for none)
	}{
		{name: "valid card", card: validCard, wantGrade: GradeConformant},
		{name: "not an object", card: `[]`, wantGrade: GradeNonConformant, wantPath: "$"},
		{
			name:      "capabilities as array",
			card:      replace(`"capabilities": {"streaming": true, "pushNotifications": true}`, `"capabilities": ["streaming"]`),
			wantGrade: GradeNonConformant,
			wantPath:  "capabilities",
		},
		{
			name:      "skills not an array",
			card:      replace(`"skills": [{"id": "quote", "name": "Quote", "description": "Price an order", "tags": ["quote", "merch"]}]`, `"skills": {"quote": {}}`),
			wantGrade: GradeNonConformant,
			wantPath:  "skills",
		},
		{
			name:      "0.x layout without supportedInterfaces",
			card:      replace(`"supportedInterfaces": [{"url": "https://agents.northwind.example/a2a", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}]`, `"url": "https://agents.northwind.example/a2a", "preferredTransport": "JSONRPC"`),
			wantGrade: GradeNonConformant,
			wantPath:  "supportedInterfaces",
		},
		{
			name:      "interface without protocolVersion",
			card:      replace(`, "protocolVersion": "1.0"}`, `}`),
			wantGrade: GradeNonConformant,
			wantPath:  "supportedInterfaces[0].protocolVersion",
		},
		{
			name:      "relative interface url",
			card:      replace(`"url": "https://agents.northwind.example/a2a"`, `"url": "/a2a"`),
			wantGrade: GradeNonConformant,
			wantPath:  "supportedInterfaces[0].url",
		},
		{
			name:      "missing name",
			card:      replace(`"name": "Northwind Merch",`, ``),
			wantGrade: GradeNonConformant,
			wantPath:  "name",
		},
		{
			name:      "missing default input modes is only a warning",
			card:      replace(`"defaultInputModes": ["text/plain"],`, ``),
			wantGrade: GradeNearConformant,
			wantPath:  "defaultInputModes",
		},
		{
			name:      "missing skill tags is only a warning",
			card:      replace(`, "tags": ["quote", "merch"]`, ``),
			wantGrade: GradeNearConformant,
			wantPath:  "skills[0].tags",
		},
		{
			name:      "plain http outside localhost",
			card:      replace(`https://agents.northwind.example`, `http://agents.northwind.example`),
			wantGrade: GradeNearConformant,
			wantPath:  "supportedInterfaces[0].url",
		},
		{
			name:      "leftover 0.x field",
			card:      replace(`"version": "1.0.0",`, `"version": "1.0.0", "protocolVersion": "0.3.0",`),
			wantGrade: GradeNearConformant,
			wantPath:  "protocolVersion",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := Check([]byte(tc.card))
			if report.Grade != tc.wantGrade {
				t.Errorf("Check() grade = %s, want %s; findings: %+v", report.Grade, tc.wantGrade, report.Findings)
			}
			if tc.wantPath == "" {
				if len(report.Findings) != 0 {
					t.Errorf("Check() findings = %+v, want none", report.Findings)
				}
				return
			}
			if !hasPath(report, tc.wantPath) {
				t.Errorf("Check() has no finding at %q; findings: %+v", tc.wantPath, report.Findings)
			}
		})
	}
}

func TestCheckSortsErrorsFirst(t *testing.T) {
	// Missing required fields are errors; missing modes and an empty skill list are warnings.
	report := Check([]byte(`{"skills": []}`))
	if len(report.Findings) == 0 || report.Findings[0].Severity != SeverityError {
		t.Fatalf("Check() first finding = %+v, want an error first", report.Findings)
	}
	seenWarning := false
	for _, f := range report.Findings {
		if f.Severity == SeverityWarning {
			seenWarning = true
		} else if seenWarning {
			t.Fatalf("Check() has an error after a warning: %+v", report.Findings)
		}
	}
}

func TestFetch(t *testing.T) {
	tests := []struct {
		name      string
		path      string // where the fake server serves the card
		wantPath  string // the CardURL suffix Fetch must report
		wantGrade Grade
	}{
		{name: "well-known path", path: WellKnownPath, wantPath: WellKnownPath, wantGrade: GradeConformant},
		{name: "legacy path only", path: LegacyWellKnownPath, wantPath: LegacyWellKnownPath, wantGrade: GradeNonConformant},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(tc.path, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(validCard))
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			fetched, err := Fetch(context.Background(), srv.Client(), srv.URL)
			if err != nil {
				t.Fatalf("Fetch() error = %v", err)
			}
			if !strings.HasSuffix(fetched.CardURL, tc.wantPath) {
				t.Errorf("Fetch() CardURL = %s, want suffix %s", fetched.CardURL, tc.wantPath)
			}
			if fetched.Report.Grade != tc.wantGrade {
				t.Errorf("Fetch() grade = %s, want %s; findings: %+v", fetched.Report.Grade, tc.wantGrade, fetched.Report.Findings)
			}
		})
	}
}

func TestFetchNotFound(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := Fetch(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("Fetch() error = nil, want an error when no card is served")
	}
}

func hasPath(r Report, path string) bool {
	for _, f := range r.Findings {
		if f.Path == path {
			return true
		}
	}
	return false
}
