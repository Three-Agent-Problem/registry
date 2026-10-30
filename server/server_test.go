package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/three-agent-problem/registry"
	"github.com/three-agent-problem/registry/client"
	"github.com/three-agent-problem/registry/lint"
	"github.com/three-agent-problem/registry/server"
)

// newAgent serves only an agent card, which is all the registry needs.
func newAgent(t *testing.T, name string, skill a2a.AgentSkill) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	card := &a2a.AgentCard{
		Name:                name,
		Description:         name + " test agent",
		Version:             "1.0.0",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(srv.URL+"/invoke", a2a.TransportProtocolJSONRPC)},
		DefaultInputModes:   []string{"text/plain"},
		DefaultOutputModes:  []string{"text/plain"},
		Skills:              []a2a.AgentSkill{skill},
	}
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	return srv
}

func TestRegisterAndSearch(t *testing.T) {
	ctx := context.Background()
	reg := server.New(nil)
	regSrv := httptest.NewServer(reg.Handler())
	defer regSrv.Close()
	c := client.New(regSrv.URL, nil)

	vendor := newAgent(t, "Northwind", a2a.AgentSkill{ID: "quote", Name: "Quote", Description: "Price an order", Tags: []string{"merch"}})
	other := newAgent(t, "Weather", a2a.AgentSkill{ID: "forecast", Name: "Forecast", Description: "Weather", Tags: []string{"weather"}})

	for _, u := range []string{vendor.URL, other.URL} {
		entry, err := c.Register(ctx, u)
		if err != nil {
			t.Fatalf("Register(%s) error = %v", u, err)
		}
		if entry.Report.Grade != lint.GradeConformant {
			t.Errorf("Register(%s) grade = %s, findings %+v", u, entry.Report.Grade, entry.Report.Findings)
		}
	}

	tests := []struct {
		name  string
		query registry.Query
		want  []string
	}{
		{name: "by skill id", query: registry.Query{Skill: "quote"}, want: []string{"Northwind"}},
		{name: "by tag", query: registry.Query{Skill: "weather"}, want: []string{"Weather"}},
		{name: "by text", query: registry.Query{Text: "north"}, want: []string{"Northwind"}},
		{name: "everything", query: registry.Query{}, want: []string{"Northwind", "Weather"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := c.Search(ctx, tc.query)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("Search() returned %d agents, want %d", len(got), len(tc.want))
			}
			for i, e := range got {
				if e.Name() != tc.want[i] {
					t.Errorf("Search()[%d] = %s, want %s", i, e.Name(), tc.want[i])
				}
			}
		})
	}
}

func TestRegisterUnreachable(t *testing.T) {
	reg := server.New(nil)
	dead := httptest.NewServer(http.NotFoundHandler())
	defer dead.Close()
	if _, err := reg.Register(context.Background(), dead.URL); err == nil {
		t.Fatal("Register() error = nil, want an error for an agent without a card")
	}
	if got := reg.Search(registry.Query{IncludeDown: true, IncludeNonConformant: true}); len(got) != 0 {
		t.Errorf("Search() = %d entries, want none stored", len(got))
	}
}

func TestPages(t *testing.T) {
	reg := server.New(nil)
	regSrv := httptest.NewServer(reg.Handler())
	defer regSrv.Close()
	vendor := newAgent(t, "Northwind", a2a.AgentSkill{ID: "quote", Name: "Quote", Description: "Price an order", Tags: []string{"merch"}})
	if _, err := reg.Register(context.Background(), vendor.URL); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/check", "/check?url=" + vendor.URL, "/api/check?url=" + vendor.URL} {
		resp, err := http.Get(regSrv.URL + path)
		if err != nil {
			t.Fatalf("GET %s error = %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %s, want 200", path, resp.Status)
		}
	}
}
