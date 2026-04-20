package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const gistUserID = "gist-user"
const githubGistAPI = "https://api.github.com/gists/"

// GistStore implements Store using a GitHub Gist as a single-user backend.
// Auth and session methods are stubs — the server bypasses requireAuth in gist mode.
type GistStore struct {
	mu           sync.RWMutex
	gistID       string
	githubToken  string
	gistFilename string
	rawConfig    map[string]json.RawMessage // preserves all unknown fields
	stocks       []Stock
	categories   []Category
	xGroups      []XGroup
}

func NewGistStore(_ context.Context) (*GistStore, error) {
	gistID := os.Getenv("GIST_ID")
	token := os.Getenv("GH_TOKEN")
	if gistID == "" {
		return nil, fmt.Errorf("GIST_ID is required for gist storage backend")
	}
	if token == "" {
		return nil, fmt.Errorf("GH_TOKEN is required for gist storage backend")
	}
	s := &GistStore{gistID: gistID, githubToken: token}
	return s, s.load()
}

func (s *GistStore) gistRequest(method, body string) (*http.Response, error) {
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, githubGistAPI+s.gistID, reqBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.githubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return http.DefaultClient.Do(req)
}

func (s *GistStore) load() error {
	resp, err := s.gistRequest(http.MethodGet, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("github API returned %d", resp.StatusCode)
	}
	var gist struct {
		Files map[string]struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return err
	}
	const filename = "stock-config.json"
	f, ok := gist.Files[filename]
	if !ok {
		return fmt.Errorf("file %q not found in gist %s", filename, s.gistID)
	}
	s.gistFilename = filename
	s.rawConfig = make(map[string]json.RawMessage)
	if err := json.Unmarshal([]byte(f.Content), &s.rawConfig); err != nil {
		return err
	}
	if v, ok := s.rawConfig["stocks"]; ok {
		json.Unmarshal(v, &s.stocks)
	}
	if v, ok := s.rawConfig["categories"]; ok {
		json.Unmarshal(v, &s.categories)
	}
	if v, ok := s.rawConfig["xGroups"]; ok {
		json.Unmarshal(v, &s.xGroups)
	} else if v, ok := s.rawConfig["xAccounts"]; ok {
		// migrate legacy flat list into a default group
		var handles []string
		if json.Unmarshal(v, &handles) == nil && len(handles) > 0 {
			s.xGroups = []XGroup{{Name: "General", Accounts: handles}}
		}
	}
	return nil
}

func (s *GistStore) persist() error {
	stocksJSON, _ := json.Marshal(s.stocks)
	catsJSON, _ := json.Marshal(s.categories)
	xGroupsJSON, _ := json.Marshal(s.xGroups)
	s.rawConfig["stocks"] = json.RawMessage(stocksJSON)
	s.rawConfig["categories"] = json.RawMessage(catsJSON)
	s.rawConfig["xGroups"] = json.RawMessage(xGroupsJSON)
	delete(s.rawConfig, "xAccounts")
	data, err := json.MarshalIndent(s.rawConfig, "", "  ")
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"files": map[string]any{
			s.gistFilename: map[string]any{"content": string(data)},
		},
	})
	resp, err := s.gistRequest(http.MethodPatch, string(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github API returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

// ---- portfolio ----

func (s *GistStore) GetPortfolio(_ context.Context, _ string) ([]Stock, []Category, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stocks := make([]Stock, len(s.stocks))
	copy(stocks, s.stocks)
	cats := make([]Category, len(s.categories))
	copy(cats, s.categories)
	return stocks, cats, nil
}

func (s *GistStore) PutStock(_ context.Context, _ string, st Stock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.stocks {
		if existing.Ticker == st.Ticker {
			return ErrConflict
		}
	}
	s.stocks = append(s.stocks, st)
	return s.persist()
}

func (s *GistStore) DeleteStock(_ context.Context, _, ticker string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, st := range s.stocks {
		if st.Ticker == ticker {
			s.stocks = append(s.stocks[:i], s.stocks[i+1:]...)
			return s.persist()
		}
	}
	return ErrNotFound
}

func (s *GistStore) UpdateStockCategory(_ context.Context, _, ticker, category string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, st := range s.stocks {
		if st.Ticker == ticker {
			s.stocks[i].Category = category
			return s.persist()
		}
	}
	return ErrNotFound
}

func (s *GistStore) ReplaceStocks(_ context.Context, _ string, stocks []Stock) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stocks = stocks
	return s.persist()
}

func (s *GistStore) PutCategory(_ context.Context, _ string, c Category) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.categories {
		if existing.Name == c.Name {
			return ErrConflict
		}
	}
	s.categories = append(s.categories, c)
	return s.persist()
}

func (s *GistStore) DeleteCategory(_ context.Context, _, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.categories {
		if c.Name == name {
			s.categories = append(s.categories[:i], s.categories[i+1:]...)
			return s.persist()
		}
	}
	return ErrNotFound
}

func (s *GistStore) RenameCategory(_ context.Context, _ string, oldName, newName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for i, c := range s.categories {
		if c.Name == oldName {
			s.categories[i].Name = newName
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	for i, st := range s.stocks {
		if st.Category == oldName {
			s.stocks[i].Category = newName
		}
	}
	return s.persist()
}

func (s *GistStore) ReplaceCategories(_ context.Context, _ string, cats []Category) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.categories = cats
	return s.persist()
}

func (s *GistStore) GetNitterInstances(_ context.Context, _ string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.rawConfig["twitter"]
	if !ok {
		return nil
	}
	var twitter struct {
		NitterInstances []string `json:"nitter_instances"`
	}
	if err := json.Unmarshal(v, &twitter); err != nil {
		return nil
	}
	return twitter.NitterInstances
}

// ---- report config ----

func (s *GistStore) GetReportConfig(_ context.Context, _ string) ([]XGroup, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	groups := make([]XGroup, len(s.xGroups))
	copy(groups, s.xGroups)
	return groups, nil
}

func (s *GistStore) PutReportConfig(_ context.Context, _ string, groups []XGroup) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.xGroups = groups
	return s.persist()
}

// ---- auth stubs (bypassed in gist mode) ----

var gistStaticUser = &User{
	ID:          gistUserID,
	Email:       "local@gist",
	DisplayName: "Local User",
	Verified:    true,
}

func (s *GistStore) GetUserByID(_ context.Context, id string) (*User, error) {
	if id == gistUserID {
		return gistStaticUser, nil
	}
	return nil, ErrNotFound
}

func (s *GistStore) GetUserByEmail(_ context.Context, _ string) (*User, error) {
	return nil, ErrNotFound
}

func (s *GistStore) GetUserByGoogleID(_ context.Context, _ string) (*User, error) {
	return nil, ErrNotFound
}

func (s *GistStore) CreateUser(_ context.Context, _ *User) error        { return nil }
func (s *GistStore) VerifyUser(_ context.Context, _ string) error        { return nil }
func (s *GistStore) LinkGoogleID(_ context.Context, _, _ string) error   { return nil }
func (s *GistStore) DeleteUser(_ context.Context, _ *User) error         { return nil }

func (s *GistStore) UpdateUser(_ context.Context, _, _, _ string) error { return nil }

func (s *GistStore) CreateVerificationToken(_ context.Context, _, _ string, _ time.Time) error {
	return nil
}

func (s *GistStore) ConsumeVerificationToken(_ context.Context, _ string) (string, error) {
	return "", ErrNotFound
}

// ---- session stubs ----

func (s *GistStore) CreateSession(_ context.Context, _, _ string, _ time.Time) error { return nil }
func (s *GistStore) DeleteSession(_ context.Context, _ string) error                 { return nil }

func (s *GistStore) GetSession(_ context.Context, _ string) (*SessionData, error) {
	return nil, ErrNotFound
}
