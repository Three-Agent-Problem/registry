// Package client talks to a registry server: agents register themselves, and
// orchestrators search for agents by skill.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/three-agent-problem/registry"
)

// Client is a registry API client.
type Client struct {
	baseURL string
	http    *http.Client
}

// New creates a client for the registry at baseURL. A nil httpClient uses http.DefaultClient.
func New(baseURL string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: strings.TrimSuffix(baseURL, "/"), http: httpClient}
}

// Register asks the registry to fetch, grade and list the agent at agentURL.
func (c *Client) Register(ctx context.Context, agentURL string) (*registry.Entry, error) {
	body, err := json.Marshal(map[string]string{"url": agentURL})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/agents", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	var entry registry.Entry
	if err := c.do(req, http.StatusCreated, &entry); err != nil {
		return nil, fmt.Errorf("register %s: %w", agentURL, err)
	}
	return &entry, nil
}

// Search returns the agents that match q, best first.
func (c *Client) Search(ctx context.Context, q registry.Query) ([]*registry.Entry, error) {
	params := url.Values{}
	if q.Skill != "" {
		params.Set("skill", q.Skill)
	}
	if q.Text != "" {
		params.Set("q", q.Text)
	}
	if q.IncludeNonConformant || q.IncludeDown {
		params.Set("all", "1")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/agents?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Agents []*registry.Entry `json:"agents"`
	}
	if err := c.do(req, http.StatusOK, &resp); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	return resp.Agents, nil
}

func (c *Client) do(req *http.Request, wantStatus int, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantStatus {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		return fmt.Errorf("registry returned %s: %s", resp.Status, apiErr.Error)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
