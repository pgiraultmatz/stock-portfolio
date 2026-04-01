package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
)

//go:embed static
var staticFiles embed.FS

type Stock struct {
	Ticker   string `json:"ticker"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

type Category struct {
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
	Order int    `json:"order"`
}

const githubGistAPI = "https://api.github.com/gists/"

// Server holds the in-memory portfolio state and the GitHub Gist config source.
type Server struct {
	mu           sync.RWMutex
	gistID       string
	githubToken  string
	gistFilename string
	rawConfig    map[string]json.RawMessage // preserves all other config fields
	stocks       []Stock
	categories   []Category
}

func NewServer() (*Server, error) {
	gistID := os.Getenv("GIST_ID")
	githubToken := os.Getenv("GH_TOKEN")
	if gistID == "" {
		return nil, fmt.Errorf("GIST_ID environment variable is required")
	}
	if githubToken == "" {
		return nil, fmt.Errorf("GH_TOKEN environment variable is required")
	}
	s := &Server{
		gistID:      gistID,
		githubToken: githubToken,
		rawConfig:   make(map[string]json.RawMessage),
	}
	return s, s.load()
}

func (s *Server) gistRequest(method, body string) (*http.Response, error) {
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

func (s *Server) load() error {
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

	const expectedFilename = "stock-config.json"
	f, ok := gist.Files[expectedFilename]
	if !ok {
		return fmt.Errorf("file %q not found in gist %s", expectedFilename, s.gistID)
	}
	s.gistFilename = expectedFilename
	content := f.Content

	if err := json.Unmarshal([]byte(content), &s.rawConfig); err != nil {
		return err
	}
	if v, ok := s.rawConfig["stocks"]; ok {
		if err := json.Unmarshal(v, &s.stocks); err != nil {
			return err
		}
	}
	if v, ok := s.rawConfig["categories"]; ok {
		if err := json.Unmarshal(v, &s.categories); err != nil {
			return err
		}
	}
	return nil
}

// ---- handlers ----

func (s *Server) getPortfolio(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"stocks":     s.stocks,
		"categories": s.categories,
	})
}

func (s *Server) postStock(w http.ResponseWriter, r *http.Request) {
	var st Stock
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st.Ticker = strings.ToUpper(strings.TrimSpace(st.Ticker))
	st.Name = strings.TrimSpace(st.Name)
	st.Category = strings.TrimSpace(st.Category)
	if st.Ticker == "" {
		http.Error(w, "ticker is required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.stocks {
		if strings.EqualFold(existing.Ticker, st.Ticker) {
			http.Error(w, "ticker already exists", http.StatusConflict)
			return
		}
	}
	s.stocks = append(s.stocks, st)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(st)
}

func (s *Server) putCategories(w http.ResponseWriter, r *http.Request) {
	var cats []Category
	if err := json.NewDecoder(r.Body).Decode(&cats); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.categories = cats
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putStocks(w http.ResponseWriter, r *http.Request) {
	var stocks []Stock
	if err := json.NewDecoder(r.Body).Decode(&stocks); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stocks = stocks
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) patchStock(w http.ResponseWriter, r *http.Request) {
	ticker := strings.TrimPrefix(r.URL.Path, "/api/stocks/")
	ticker, _ = url.PathUnescape(ticker)
	if ticker == "" {
		http.Error(w, "ticker required", http.StatusBadRequest)
		return
	}
	var body struct {
		Category string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	found := false
	for i, st := range s.stocks {
		if strings.EqualFold(st.Ticker, ticker) {
			s.stocks[i].Category = strings.TrimSpace(body.Category)
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteStock(w http.ResponseWriter, r *http.Request) {
	ticker := strings.TrimPrefix(r.URL.Path, "/api/stocks/")
	ticker, _ = url.PathUnescape(ticker)
	if ticker == "" {
		http.Error(w, "ticker required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	newStocks := make([]Stock, 0, len(s.stocks))
	found := false
	for _, st := range s.stocks {
		if strings.EqualFold(st.Ticker, ticker) {
			found = true
			continue
		}
		newStocks = append(newStocks, st)
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.stocks = newStocks
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) postCategory(w http.ResponseWriter, r *http.Request) {
	var cat Category
	if err := json.NewDecoder(r.Body).Decode(&cat); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cat.Name = strings.TrimSpace(cat.Name)
	cat.Emoji = strings.TrimSpace(cat.Emoji)
	if cat.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.categories {
		if strings.EqualFold(c.Name, cat.Name) {
			http.Error(w, "category already exists", http.StatusConflict)
			return
		}
	}
	maxOrder := 0
	for _, c := range s.categories {
		if c.Order > maxOrder {
			maxOrder = c.Order
		}
	}
	cat.Order = maxOrder + 1
	s.categories = append(s.categories, cat)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(cat)
}

func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/categories/")
	name, _ = url.PathUnescape(name)
	if name == "" {
		http.Error(w, "category name required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	newCats := make([]Category, 0, len(s.categories))
	found := false
	for _, c := range s.categories {
		if c.Name == name {
			found = true
			continue
		}
		newCats = append(newCats, c)
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.categories = newCats
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) searchYahoo(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		http.Error(w, "q required", http.StatusBadRequest)
		return
	}
	yahooURL := "https://query1.finance.yahoo.com/v1/finance/search?q=" +
		url.QueryEscape(q) + "&quotesCount=8&newsCount=0&listsCount=0"
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, yahooURL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

func (s *Server) saveConfig(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	stocksJSON, _ := json.Marshal(s.stocks)
	catsJSON, _ := json.Marshal(s.categories)
	s.rawConfig["stocks"] = json.RawMessage(stocksJSON)
	s.rawConfig["categories"] = json.RawMessage(catsJSON)
	data, _ := json.MarshalIndent(s.rawConfig, "", "  ")

	payload, _ := json.Marshal(map[string]any{
		"files": map[string]any{
			s.gistFilename: map[string]any{
				"content": string(data),
			},
		},
	})
	resp, err := s.gistRequest(http.MethodPatch, string(payload))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		http.Error(w, fmt.Sprintf("github API returned %d: %s", resp.StatusCode, bytes.TrimSpace(body)), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

// ---- routing ----

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticFS)))

	mux.HandleFunc("/api/portfolio", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.getPortfolio(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/stocks/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			s.deleteStock(w, r)
		case http.MethodPatch:
			s.patchStock(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/stocks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.postStock(w, r)
		case http.MethodPut:
			s.putStocks(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/categories/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.deleteCategory(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/categories", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.postCategory(w, r)
		case http.MethodPut:
			s.putCategories(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.searchYahoo(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.saveConfig(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	return mux
}

// loadDotEnv reads a .env file and sets variables that are not already set in the environment.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // .env is optional
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Strip optional surrounding quotes
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

func main() {
	addr := flag.String("addr", ":8080", "listen address (e.g. :8080)")
	flag.Parse()

	loadDotEnv(".env")

	srv, err := NewServer()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	log.Printf("Portfolio editor running at http://localhost%s", *addr)
	log.Printf("Gist: https://gist.github.com/%s", srv.gistID)
	if err := http.ListenAndServe(*addr, srv.routes()); err != nil {
		log.Fatal(err)
	}
}
