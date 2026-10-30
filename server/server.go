// Package server is a small A2A agent registry: agents register their base URL, the
// server fetches and grades the card, re-checks liveness in the background, and answers
// searches by skill.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/three-agent-problem/registry"
	"github.com/three-agent-problem/registry/lint"
)

//go:embed templates/*.html
var templateFS embed.FS

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"since": func(t time.Time) string { return time.Since(t).Round(time.Second).String() },
	"skills": func(card *a2a.AgentCard) string {
		if card == nil {
			return ""
		}
		ids := make([]string, 0, len(card.Skills))
		for _, s := range card.Skills {
			ids = append(ids, s.ID)
		}
		return strings.Join(ids, ", ")
	},
}).ParseFS(templateFS, "templates/*.html"))

// Server keeps registered agents in memory.
type Server struct {
	client *http.Client
	logger *slog.Logger

	mu      sync.RWMutex
	entries map[string]*registry.Entry
}

// New creates a registry server. A nil client uses a client with a 5 second timeout.
func New(client *http.Client) *Server {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Server{client: client, logger: slog.Default(), entries: make(map[string]*registry.Entry)}
}

// Handler returns the HTTP API and the HTML pages.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /agents", s.handleRegister)
	mux.HandleFunc("GET /agents", s.handleSearch)
	mux.HandleFunc("GET /api/check", s.handleCheckJSON)
	mux.HandleFunc("GET /check", s.handleCheckPage)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	return mux
}

// Register fetches and grades the card of the agent at baseURL and stores the result.
// An agent whose card can't be fetched at all is not stored.
func (s *Server) Register(ctx context.Context, baseURL string) (*registry.Entry, error) {
	baseURL = strings.TrimSuffix(baseURL, "/")
	fetched, err := lint.Fetch(ctx, s.client, baseURL)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	entry := &registry.Entry{URL: baseURL, Registered: now}
	apply(entry, fetched, now)

	s.mu.Lock()
	if prev, ok := s.entries[baseURL]; ok {
		entry.Registered = prev.Registered
	}
	s.entries[baseURL] = entry
	s.mu.Unlock()

	s.logger.Info("agent registered", "url", baseURL, "name", entry.Name(), "grade", entry.Report.Grade)
	return copyEntry(entry), nil
}

// Search returns the entries that match q, best first.
func (s *Server) Search(q registry.Query) []*registry.Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*registry.Entry
	for _, e := range s.entries {
		if q.Matches(e) {
			out = append(out, copyEntry(e))
		}
	}
	slices.SortFunc(out, registry.Compare)
	return out
}

// Run re-checks every registered agent at the given interval until ctx is done.
func (s *Server) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.recheckAll(ctx)
		}
	}
}

func (s *Server) recheckAll(ctx context.Context) {
	s.mu.RLock()
	urls := make([]string, 0, len(s.entries))
	for u := range s.entries {
		urls = append(urls, u)
	}
	s.mu.RUnlock()

	for _, u := range urls {
		fetched, err := lint.Fetch(ctx, s.client, u)
		now := time.Now()
		s.mu.Lock()
		if entry, ok := s.entries[u]; ok {
			if err != nil {
				entry.Up = false
				entry.LastError = err.Error()
			} else {
				apply(entry, fetched, now)
			}
		}
		s.mu.Unlock()
	}
}

func apply(entry *registry.Entry, fetched *lint.Fetched, now time.Time) {
	entry.CardURL = fetched.CardURL
	entry.Report = fetched.Report
	entry.LatencyMS = fetched.Latency.Milliseconds()
	entry.LastSeen = now
	entry.Up = true
	entry.LastError = ""
	entry.Card = nil
	var card a2a.AgentCard
	if err := json.Unmarshal(fetched.Raw, &card); err == nil {
		entry.Card = &card
	}
}

func copyEntry(e *registry.Entry) *registry.Entry {
	cp := *e
	return &cp
}

type registerRequest struct {
	URL string `json:"url"`
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `body must be {"url": "<agent base URL>"}`})
		return
	}
	entry, err := s.Register(r.Context(), req.URL)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	entries := s.Search(registry.Query{
		Skill:                q.Get("skill"),
		Text:                 q.Get("q"),
		IncludeNonConformant: q.Get("all") != "",
		IncludeDown:          q.Get("all") != "",
	})
	if entries == nil {
		entries = []*registry.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": entries})
}

func (s *Server) check(r *http.Request) (*lint.Fetched, error) {
	target := r.URL.Query().Get("url")
	if target == "" {
		return nil, errors.New("missing ?url=")
	}
	return lint.Fetch(r.Context(), s.client, target)
}

func (s *Server) handleCheckJSON(w http.ResponseWriter, r *http.Request) {
	fetched, err := s.check(r)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cardUrl": fetched.CardURL, "report": fetched.Report})
}

func (s *Server) handleCheckPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"URL": r.URL.Query().Get("url")}
	if fetched, err := s.check(r); err != nil {
		data["Error"] = err.Error()
	} else {
		data["Fetched"] = fetched
	}
	s.render(w, "check.html", data)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	entries := s.Search(registry.Query{IncludeNonConformant: true, IncludeDown: true})
	s.render(w, "index.html", map[string]any{"Entries": entries})
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pages.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("render failed", "template", name, "error", err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
