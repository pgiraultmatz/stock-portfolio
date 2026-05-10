package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

//go:embed static
var staticFiles embed.FS

type Stock struct {
	Ticker      string `json:"ticker"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Note        string `json:"note,omitempty"`
	Conviction  string `json:"conviction,omitempty"`
	InPortfolio *bool  `json:"inPortfolio,omitempty"`
}

type Category struct {
	Name           string   `json:"name"`
	Emoji          string   `json:"emoji"`
	Order          int      `json:"order"`
	Description    string   `json:"description,omitempty"`
	NarrativeScore *float64 `json:"narrativeScore,omitempty"`
}

type contextKey string

const ctxUserID   contextKey = "userID"
const ctxUser     contextKey = "user"

type Server struct {
	store         Store
	googleOAuth   *oauth2.Config
	secureCookies bool
	pendingStates sync.Map
	mailer        Mailer
	baseURL       string
}

func NewServer(ctx context.Context) (*Server, error) {
	var store Store
	switch backend := os.Getenv("STORAGE_BACKEND"); backend {
	case "gist":
		gs, err := NewGistStore(ctx)
		if err != nil {
			return nil, fmt.Errorf("init gist store: %w", err)
		}
		store = gs
	case "", "dynamodb":
		region := os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
		}
		table := os.Getenv("DYNAMODB_TABLE")
		if table == "" {
			table = "stock-portfolio"
		}
		ds, err := NewDynamoStore(ctx, os.Getenv("DYNAMODB_ENDPOINT"), region, table)
		if err != nil {
			return nil, fmt.Errorf("init dynamodb store: %w", err)
		}
		store = ds
	default:
		return nil, fmt.Errorf("unknown STORAGE_BACKEND %q (valid: gist, dynamodb)", backend)
	}

	s := &Server{store: store}

	baseURL := os.Getenv("APP_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	s.baseURL = baseURL
	s.secureCookies = strings.HasPrefix(baseURL, "https://")

	gmailFrom := os.Getenv("GMAIL_FROM")
	gmailPassword := os.Getenv("GMAIL_APP_PASSWORD")
	if gmailFrom != "" && gmailPassword != "" {
		s.mailer = NewGmailMailer(gmailFrom, gmailPassword)
	} else {
		s.mailer = &LogMailer{}
	}

	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	if clientID != "" && clientSecret != "" {
		s.googleOAuth = &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  baseURL + "/auth/google/callback",
			Scopes: []string{
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/userinfo.profile",
			},
			Endpoint: google.Endpoint,
		}
	}

	return s, nil
}

// ---- auth middleware ----

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.store.(*GistStore); ok {
			ctx := context.WithValue(r.Context(), ctxUserID, gistUserID)
			ctx = context.WithValue(ctx, ctxUser, gistStaticUser)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		userID, ok := s.getSession(r)
		if !ok {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		}

		user, err := s.store.GetUserByID(r.Context(), userID)
		if err != nil || !user.Verified {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.Error(w, "email not verified", http.StatusForbidden)
				return
			}
			if cookie, err := r.Cookie("session"); err == nil {
				s.store.DeleteSession(r.Context(), cookie.Value)
			}
			http.SetCookie(w, &http.Cookie{
				Name: "session", Value: "", Path: "/",
				Expires: time.Unix(0, 0), MaxAge: -1,
			})
			http.Redirect(w, r, "/auth/login", http.StatusFound)
			return
		}

		ctx := context.WithValue(r.Context(), ctxUserID, userID)
		ctx = context.WithValue(ctx, ctxUser, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func userIDFromCtx(r *http.Request) string {
	v, _ := r.Context().Value(ctxUserID).(string)
	return v
}

func userFromCtx(r *http.Request) *User {
	v, _ := r.Context().Value(ctxUser).(*User)
	return v
}

// ---- user profile handlers ----

func (s *Server) getMe(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":          u.ID,
		"email":       u.Email,
		"displayName": u.DisplayName,
		"hasPassword": u.PasswordHash != "",
	})
}

func (s *Server) deleteMe(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	if cookie, err := r.Cookie("session"); err == nil {
		s.store.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: "", Path: "/",
		Expires: time.Unix(0, 0), MaxAge: -1,
	})
	if err := s.store.DeleteUser(r.Context(), u); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent) // frontend handles redirect
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) {
	u := userFromCtx(r)
	var body struct {
		DisplayName string `json:"displayName"`
		Password    string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.DisplayName)
	if name == "" {
		name = u.DisplayName
	}
	hash := u.PasswordHash
	if body.Password != "" {
		if len(body.Password) < 8 {
			http.Error(w, "password must be at least 8 characters", http.StatusBadRequest)
			return
		}
		b, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		hash = string(b)
	}
	if err := s.store.UpdateUser(r.Context(), u.ID, name, hash); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- portfolio handlers ----

func (s *Server) getPortfolio(w http.ResponseWriter, r *http.Request) {
	stocks, cats, err := s.store.GetPortfolio(r.Context(), userIDFromCtx(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if stocks == nil {
		stocks = []Stock{}
	}
	if cats == nil {
		cats = []Category{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"stocks": stocks, "categories": cats})
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

	if err := s.store.PutStock(r.Context(), userIDFromCtx(r), st); err != nil {
		if errors.Is(err, ErrConflict) {
			http.Error(w, "ticker already exists", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(st)
}

func (s *Server) putStocks(w http.ResponseWriter, r *http.Request) {
	var stocks []Stock
	if err := json.NewDecoder(r.Body).Decode(&stocks); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.ReplaceStocks(r.Context(), userIDFromCtx(r), stocks); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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
	err := s.store.UpdateStockCategory(r.Context(), userIDFromCtx(r), ticker, strings.TrimSpace(body.Category))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
	if err := s.store.DeleteStock(r.Context(), userIDFromCtx(r), ticker); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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

	_, cats, err := s.store.GetPortfolio(r.Context(), userIDFromCtx(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	maxOrder := 0
	for _, c := range cats {
		if c.Order > maxOrder {
			maxOrder = c.Order
		}
	}
	cat.Order = maxOrder + 1

	if err := s.store.PutCategory(r.Context(), userIDFromCtx(r), cat); err != nil {
		if errors.Is(err, ErrConflict) {
			http.Error(w, "category already exists", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(cat)
}

func (s *Server) putCategories(w http.ResponseWriter, r *http.Request) {
	var cats []Category
	if err := json.NewDecoder(r.Body).Decode(&cats); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.ReplaceCategories(r.Context(), userIDFromCtx(r), cats); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) patchCategory(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/categories/")
	name, _ = url.PathUnescape(name)
	if name == "" {
		http.Error(w, "category name required", http.StatusBadRequest)
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	newName := strings.TrimSpace(body.Name)
	if newName == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	err := s.store.RenameCategory(r.Context(), userIDFromCtx(r), name, newName)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteCategory(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/categories/")
	name, _ = url.PathUnescape(name)
	if name == "" {
		http.Error(w, "category name required", http.StatusBadRequest)
		return
	}
	if err := s.store.DeleteCategory(r.Context(), userIDFromCtx(r), name); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
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

func (s *Server) quotesYahoo(w http.ResponseWriter, r *http.Request) {
	tickersParam := strings.TrimSpace(r.URL.Query().Get("tickers"))
	if tickersParam == "" {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{}"))
		return
	}

	type quoteResult struct {
		Price    float64 `json:"price"`
		Change   float64 `json:"change"`
		Currency string  `json:"currency"`
		Stale    bool    `json:"stale"`
	}
	type chartMeta struct {
		RegularMarketPrice float64 `json:"regularMarketPrice"`
		ChartPreviousClose float64 `json:"chartPreviousClose"`
		Currency           string  `json:"currency"`
	}
	type chartResp struct {
		Chart struct {
			Result []struct {
				Meta      chartMeta `json:"meta"`
				Timestamp []int64   `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Open []float64 `json:"open"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error *struct{ Code string } `json:"error"`
		} `json:"chart"`
	}

	tickers := strings.Split(tickersParam, ",")
	results := make(map[string]quoteResult, len(tickers))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, t := range tickers {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		wg.Add(1)
		go func(ticker string) {
			defer wg.Done()
			yahooURL := "https://query2.finance.yahoo.com/v8/finance/chart/" + url.PathEscape(ticker) + "?range=1d&interval=5m"
			req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, yahooURL, nil)
			if err != nil {
				return
			}
			req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
			req.Header.Set("Accept", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}
			var cr chartResp
			if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
				return
			}
			if cr.Chart.Error != nil || len(cr.Chart.Result) == 0 {
				return
			}
			res := cr.Chart.Result[0]
			currentPrice := res.Meta.RegularMarketPrice
			refPrice := res.Meta.ChartPreviousClose
			if refPrice == 0 && len(res.Indicators.Quote) > 0 && len(res.Indicators.Quote[0].Open) > 0 {
				refPrice = res.Indicators.Quote[0].Open[0]
			}
			if refPrice == 0 || currentPrice == 0 {
				return
			}
			stale := true
			if len(res.Timestamp) > 0 {
				bt := time.Unix(res.Timestamp[0], 0)
				now := time.Now()
				stale = bt.Year() != now.Year() || bt.Month() != now.Month() || bt.Day() != now.Day()
			}
			mu.Lock()
			results[ticker] = quoteResult{
				Price:    currentPrice,
				Change:   (currentPrice - refPrice) / refPrice * 100,
				Currency: res.Meta.Currency,
				Stale:    stale,
			}
			mu.Unlock()
		}(t)
	}
	wg.Wait()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) validateXHandle(w http.ResponseWriter, r *http.Request) {
	handle := strings.TrimPrefix(strings.TrimSpace(r.URL.Query().Get("handle")), "@")
	if handle == "" {
		http.Error(w, "handle is required", http.StatusBadRequest)
		return
	}
	instances := s.store.GetNitterInstances(r.Context(), userIDFromCtx(r))
	client := &http.Client{Timeout: 8 * time.Second}
	for _, inst := range instances {
		inst = strings.TrimRight(strings.TrimSpace(inst), "/")
		if inst == "" {
			continue
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, inst+"/"+handle+"/rss", nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")
		resp, err := client.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]bool{"valid": true})
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"valid": false})
}

func (s *Server) getReport(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.GetReportConfig(r.Context(), userIDFromCtx(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if groups == nil {
		groups = []XGroup{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"xGroups": groups})
}

func (s *Server) putReport(w http.ResponseWriter, r *http.Request) {
	var body struct {
		XGroups []XGroup `json:"xGroups"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.PutReportConfig(r.Context(), userIDFromCtx(r), body.XGroups); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getPrompt(w http.ResponseWriter, r *http.Request) {
	content, err := s.store.GetPrompt(r.Context(), userIDFromCtx(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"content": content})
}

func (s *Server) putPrompt(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.store.PutPrompt(r.Context(), userIDFromCtx(r), body.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getStockData(w http.ResponseWriter, r *http.Request) {
	type getter interface {
		GetStockData(context.Context, string) (*StockDataFile, error)
	}
	g, ok := s.store.(getter)
	if !ok {
		http.Error(w, "not supported", http.StatusNotImplemented)
		return
	}
	data, err := g.GetStockData(r.Context(), userIDFromCtx(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if data == nil {
		http.Error(w, "no data available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (s *Server) saveConfig(w http.ResponseWriter, _ *http.Request) {
	// Portfolio is persisted immediately on every write — nothing to do.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
}

// ---- Performance (Trade Republic) — filesystem storage ----------------------
//
// CSV files  : files/performance/{userID}/tr/tr_{year}.csv
// Result cache: files/performance/{userID}/tr/result.json

type PerfTRResponse struct {
	Years     []PerfTRYearRecord `json:"years"`
	Monthly   MonthlyData        `json:"monthly"`
	Positions []OpenPosition     `json:"positions"`
}

func perfTRDir(userID string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, userID)
	return filepath.Join("files", "performance", safe, "tr")
}

func perfTRCSVPath(userID, year string) string {
	return filepath.Join(perfTRDir(userID), "tr_"+year+".csv")
}

func perfTRResultPath(userID string) string {
	return filepath.Join(perfTRDir(userID), "result.json")
}

func perfTRPricesPath(userID string) string {
	return filepath.Join(perfTRDir(userID), "prices.json")
}

func (s *Server) getAIRec(w http.ResponseWriter, r *http.Request) {
	content, err := s.store.GetTRAIRec(r.Context(), userIDFromCtx(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"content": content})
}

func (s *Server) putAIRec(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.store.PutTRAIRec(r.Context(), userIDFromCtx(r), body.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

const pricesCacheTTL = 15 * time.Minute

type pricesCache struct {
	UpdatedAt time.Time             `json:"updatedAt"`
	Quotes    map[string]yahooQuote `json:"quotes"`
}

func loadPricesCache(userID string) *pricesCache {
	data, err := os.ReadFile(perfTRPricesPath(userID))
	if err != nil {
		return nil
	}
	var c pricesCache
	if json.Unmarshal(data, &c) != nil {
		return nil
	}
	if time.Since(c.UpdatedAt) > pricesCacheTTL {
		return nil
	}
	return &c
}

func savePricesCache(userID string, quotes map[string]yahooQuote) {
	c := pricesCache{UpdatedAt: time.Now(), Quotes: quotes}
	if data, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(perfTRPricesPath(userID), data, 0o644)
	}
}

func perfTRListYears(userID string) ([]PerfTRYearRecord, error) {
	entries, err := os.ReadDir(perfTRDir(userID))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	currentYear := time.Now().Format("2006")
	var years []PerfTRYearRecord
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "tr_") || !strings.HasSuffix(name, ".csv") || len(name) != 11 {
			continue
		}
		year := name[3:7]
		years = append(years, PerfTRYearRecord{Year: year, IsCurrent: year == currentYear})
	}
	sort.Slice(years, func(i, j int) bool { return years[i].Year < years[j].Year })
	return years, nil
}

// computeAndCachePerfTR reads all CSV files, runs FIFO, writes result.json.
func (s *Server) computeAndCachePerfTR(userID string) (*PerfTRResponse, error) {
	yearRecords, err := perfTRListYears(userID)
	if err != nil {
		return nil, err
	}
	var allTxs []TRTransaction
	for _, yr := range yearRecords {
		data, err := os.ReadFile(perfTRCSVPath(userID, yr.Year))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", yr.Year, err)
		}
		txs, _, err := parseTRCSV(string(data))
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", yr.Year, err)
		}
		allTxs = append(allTxs, txs...)
	}
	sort.Slice(allTxs, func(i, j int) bool { return allTxs[i].Date < allTxs[j].Date })

	monthly := calcPerfPnL(allTxs)
	if monthly == nil {
		monthly = MonthlyData{}
	}
	positions := calcOpenPositions(allTxs)
	resp := &PerfTRResponse{Years: yearRecords, Monthly: monthly, Positions: positions}

	if data, err := json.Marshal(resp); err == nil {
		_ = os.WriteFile(perfTRResultPath(userID), data, 0o644)
	}
	return resp, nil
}

type yahooQuote struct {
	Price    float64 `json:"price"`
	Currency string  `json:"currency"`
}

var isinRe = regexp.MustCompile(`^[A-Z]{2}[A-Z0-9]{10}$`)

// openFIGIToYahooSuffix maps OpenFIGI exchange codes to Yahoo Finance ticker suffixes.
var openFIGIToYahooSuffix = map[string]string{
	"FH": ".HE", // Helsinki
	"FP": ".PA", // Paris (Euronext)
	"GY": ".DE", // XETRA
	"GF": ".F",  // Frankfurt
	"NA": ".AS", // Amsterdam
	"LN": ".L",  // London
	"SM": ".MC", // Madrid
	"SW": ".SW", // Zurich
	"EB": ".BR", // Brussels
	"IM": ".MI", // Milan
	"ID": ".IR", // Dublin
	"SS": ".ST", // Stockholm
	"DC": ".CO", // Copenhagen
	"OS": ".OL", // Oslo
	"AU": ".AX", // ASX (Australia)
}

// resolveISINs batch-resolves ISINs to Yahoo tickers via OpenFIGI.
// Returns a map ISIN → Yahoo ticker for successfully resolved symbols.
func resolveISINs(ctx context.Context, isins []string) map[string]string {
	if len(isins) == 0 {
		return nil
	}

	type figiReq struct {
		IDType   string `json:"idType"`
		IDValue  string `json:"idValue"`
		ExchCode string `json:"exchCode,omitempty"`
	}
	type figiMatch struct {
		Ticker   string `json:"ticker"`
		ExchCode string `json:"exchCode"`
	}
	type figiResp struct {
		Data  []figiMatch `json:"data"`
		Error string      `json:"error"`
	}

	reqs := make([]figiReq, len(isins))
	for i, isin := range isins {
		reqs[i] = figiReq{IDType: "ID_ISIN", IDValue: isin}
		if strings.HasPrefix(isin, "US") {
			reqs[i].ExchCode = "US"
		}
	}

	figiToYahoo := func(ticker, exchCode string) string {
		if suffix, ok := openFIGIToYahooSuffix[exchCode]; ok {
			return ticker + suffix
		}
		return ticker // US or unknown → use ticker as-is
	}

	const batchSize = 10
	mapping := make(map[string]string, len(isins))
	for start := 0; start < len(reqs); start += batchSize {
		end := start + batchSize
		if end > len(reqs) {
			end = len(reqs)
		}
		body, _ := json.Marshal(reqs[start:end])
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.openfigi.com/v3/mapping", strings.NewReader(string(body)))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		var results []figiResp
		if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()
		for i, r := range results {
			if r.Error == "" && len(r.Data) > 0 {
				m := r.Data[0]
				mapping[isins[start+i]] = figiToYahoo(m.Ticker, m.ExchCode)
			}
		}
	}
	return mapping
}

func fetchYahooPrice(ctx context.Context, ticker string) (yahooQuote, bool) {
	type chartMeta struct {
		RegularMarketPrice float64 `json:"regularMarketPrice"`
		Currency           string  `json:"currency"`
	}
	type chartResp struct {
		Chart struct {
			Result []struct{ Meta chartMeta `json:"meta"` } `json:"result"`
			Error  *struct{ Code string }                   `json:"error"`
		} `json:"chart"`
	}
	u := "https://query2.finance.yahoo.com/v8/finance/chart/" + url.PathEscape(ticker) + "?range=1d&interval=1d"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return yahooQuote{}, false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return yahooQuote{}, false
	}
	defer resp.Body.Close()
	var cr chartResp
	if json.NewDecoder(resp.Body).Decode(&cr) != nil {
		return yahooQuote{}, false
	}
	if cr.Chart.Error != nil || len(cr.Chart.Result) == 0 {
		return yahooQuote{}, false
	}
	price := cr.Chart.Result[0].Meta.RegularMarketPrice
	if price == 0 {
		return yahooQuote{}, false
	}
	return yahooQuote{Price: price, Currency: cr.Chart.Result[0].Meta.Currency}, true
}

// fetchYahooPrices resolves ISINs via OpenFIGI, then fetches prices from Yahoo.
// Returns results keyed by the original symbol (ISIN or ticker).
func fetchYahooPrices(ctx context.Context, symbols []string) map[string]yahooQuote {
	// Step 1: batch-resolve ISINs → tickers via OpenFIGI.
	var isins []string
	for _, sym := range symbols {
		if isinRe.MatchString(sym) {
			isins = append(isins, sym)
		}
	}
	isinMap := resolveISINs(ctx, isins) // ISIN → ticker

	tickers := make(map[string]string, len(symbols))
	for _, sym := range symbols {
		if t, ok := isinMap[sym]; ok {
			tickers[sym] = t
		} else {
			tickers[sym] = sym
		}
	}

	// Step 2: fetch prices for all unique tickers concurrently.
	unique := map[string]struct{}{}
	for _, t := range tickers {
		unique[t] = struct{}{}
	}
	prices := make(map[string]yahooQuote, len(unique))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for t := range unique {
		t := t
		wg.Add(1)
		go func() {
			defer wg.Done()
			if q, ok := fetchYahooPrice(ctx, t); ok {
				mu.Lock()
				prices[t] = q
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Step 3: map results back to original symbols.
	results := make(map[string]yahooQuote, len(symbols))
	for sym, ticker := range tickers {
		if q, ok := prices[ticker]; ok {
			results[sym] = q
		}
	}
	return results
}

// getPerfTR serves result.json (cache); falls back to full computation on miss.
// Positions are enriched with live Yahoo prices on every request.
func (s *Server) getPerfTR(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r)
	var resp *PerfTRResponse
	if data, err := os.ReadFile(perfTRResultPath(userID)); err == nil {
		var cached PerfTRResponse
		if json.Unmarshal(data, &cached) == nil && cached.Positions != nil {
			resp = &cached
		}
	}
	if resp == nil {
		var err error
		resp, err = s.computeAndCachePerfTR(userID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Enrich positions with prices (cached or fresh from Yahoo).
	if len(resp.Positions) > 0 {
		syms := make([]string, len(resp.Positions))
		for i, p := range resp.Positions {
			syms[i] = p.Symbol
		}
		var quotes map[string]yahooQuote
		if c := loadPricesCache(userID); c != nil && len(c.Quotes) > 0 {
			quotes = c.Quotes
		} else {
			fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			quotes = fetchYahooPrices(fetchCtx, syms)
			if len(quotes) > 0 {
				savePricesCache(userID, quotes)
			}
		}

		// Collect all non-EUR currencies that need conversion.
		nonEURCurrencies := map[string]struct{}{}
		for _, p := range resp.Positions {
			if q, ok := quotes[p.Symbol]; ok && q.Currency != "" && q.Currency != "EUR" {
				nonEURCurrencies[q.Currency] = struct{}{}
			}
		}
		// Fetch EUR/{CUR}=X rates concurrently (e.g. EURUSD=X gives USD per 1 EUR).
		fxRates := map[string]float64{} // currency → EUR{CUR}=X rate
		if len(nonEURCurrencies) > 0 {
			fxCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var fxMu sync.Mutex
			var fxWg sync.WaitGroup
			for cur := range nonEURCurrencies {
				cur := cur
				fxWg.Add(1)
				go func() {
					defer fxWg.Done()
					if q, ok := fetchYahooPrice(fxCtx, "EUR"+cur+"=X"); ok {
						fxMu.Lock()
						fxRates[cur] = q.Price
						fxMu.Unlock()
					}
				}()
			}
			fxWg.Wait()
		}

		for i, p := range resp.Positions {
			q, ok := quotes[p.Symbol]
			if !ok {
				continue
			}
			// priceEUR = price / (EUR/CUR rate); for EUR-denominated prices rate is 1.
			priceEUR := q.Price
			if q.Currency != "EUR" {
				if rate, ok := fxRates[q.Currency]; ok && rate > 0 {
					priceEUR = q.Price / rate
				}
			}
			resp.Positions[i].Price = q.Price
			resp.Positions[i].Value = priceEUR * p.Shares
			resp.Positions[i].PnL = resp.Positions[i].Value - p.TotalCost
			resp.Positions[i].PnLPct = resp.Positions[i].PnL / p.TotalCost * 100
			resp.Positions[i].Currency = q.Currency
			resp.Positions[i].HasPrice = true
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) postPerfTR(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	txs, year, err := parseTRCSV(string(body))
	if err != nil {
		http.Error(w, "invalid CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(txs) == 0 || year == "" {
		http.Error(w, "no transactions found", http.StatusBadRequest)
		return
	}
	userID := userIDFromCtx(r)
	if err := os.MkdirAll(perfTRDir(userID), 0o755); err != nil {
		http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(perfTRCSVPath(userID, year), body, 0o644); err != nil {
		http.Error(w, "write: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := s.computeAndCachePerfTR(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) deletePerfTRYear(w http.ResponseWriter, r *http.Request) {
	year := strings.TrimPrefix(r.URL.Path, "/api/performance/tr/")
	if len(year) != 4 {
		http.Error(w, "year required (4 digits)", http.StatusBadRequest)
		return
	}
	userID := userIDFromCtx(r)
	_ = os.Remove(perfTRCSVPath(userID, year))
	_ = os.Remove(perfTRResultPath(userID)) // invalidate cache
	resp, err := s.computeAndCachePerfTR(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ---- routing ----

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	// Public auth routes
	mux.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.authLoginPost(w, r)
		} else {
			s.authLoginPage(w, r)
		}
	})
	mux.HandleFunc("/auth/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.authRegisterPost(w, r)
		} else {
			s.authLoginPage(w, r) // same page, handled via JS tab
		}
	})
	mux.HandleFunc("/account/deleted", func(w http.ResponseWriter, r *http.Request) {
		var data []byte
		if os.Getenv("DEV") == "1" {
			data, _ = os.ReadFile(filepath.Join("static", "deleted.html"))
		} else {
			data, _ = staticFiles.ReadFile("static/deleted.html")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.HandleFunc("/auth/google", s.authGoogleLogin)
	mux.HandleFunc("/auth/google/callback", s.authGoogleCallback)
	mux.HandleFunc("/auth/verify", s.authVerify)
	mux.HandleFunc("/auth/logout", s.authLogout)

	// Protected routes
	devMode := os.Getenv("DEV") == "1"
	var fileServer http.Handler
	if devMode {
		log.Println("DEV mode: serving static files from disk (no rebuild needed)")
		fileServer = http.FileServer(http.Dir("static"))
	} else {
		staticFS, _ := fs.Sub(staticFiles, "static")
		fileServer = http.FileServer(http.FS(staticFS))
	}
	protected := http.NewServeMux()
	protected.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve static files that exist; fall back to index.html for SPA routes
		if r.URL.Path != "/" {
			exists := false
			if devMode {
				_, err := os.Stat(filepath.Join("static", r.URL.Path))
				exists = err == nil
			} else {
				f, err := staticFiles.Open("static" + r.URL.Path)
				if err == nil { f.Close(); exists = true }
			}
			if exists {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		var data []byte
		if devMode {
			data, _ = os.ReadFile(filepath.Join("static", "index.html"))
		} else {
			data, _ = staticFiles.ReadFile("static/index.html")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	protected.HandleFunc("/api/portfolio", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.getPortfolio(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/stocks/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			s.deleteStock(w, r)
		case http.MethodPatch:
			s.patchStock(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/stocks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.postStock(w, r)
		case http.MethodPut:
			s.putStocks(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/categories/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			s.deleteCategory(w, r)
		case http.MethodPatch:
			s.patchCategory(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/categories", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			s.postCategory(w, r)
		case http.MethodPut:
			s.putCategories(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getMe(w, r)
		case http.MethodPatch:
			s.updateMe(w, r)
		case http.MethodDelete:
			s.deleteMe(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/quotes", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.quotesYahoo(w, r)
		}
	})
	protected.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.searchYahoo(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/save", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			s.saveConfig(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	protected.HandleFunc("/api/x/validate", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.validateXHandle(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	protected.HandleFunc("/api/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.getMetrics(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	protected.HandleFunc("/api/stock-data", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.getStockData(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	protected.HandleFunc("/api/report", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getReport(w, r)
		case http.MethodPut:
			s.putReport(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	protected.HandleFunc("/api/prompt", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getPrompt(w, r)
		case http.MethodPut:
			s.putPrompt(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	protected.HandleFunc("/api/performance/tr/ai-rec", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getAIRec(w, r)
		case http.MethodPut:
			s.putAIRec(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/performance/tr/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.deletePerfTRYear(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/performance/tr", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getPerfTR(w, r)
		case http.MethodPost:
			s.postPerfTR(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.Handle("/", s.requireAuth(protected))
	return logRequests(mux)
}

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		h.ServeHTTP(rw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start).Round(time.Millisecond))
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// ---- misc ----

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
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
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	loadDotEnv(".env")

	ctx := context.Background()
	srv, err := NewServer(ctx)
	if err != nil {
		log.Fatalf("failed to start server: %v", err)
	}

	log.Printf("Portfolio editor running at http://localhost%s", *addr)
	switch backend := os.Getenv("STORAGE_BACKEND"); backend {
	case "gist":
		log.Printf("Storage: gist (%s)", os.Getenv("GIST_ID"))
	default:
		endpoint := os.Getenv("DYNAMODB_ENDPOINT")
		if endpoint != "" {
			log.Printf("Storage: dynamodb %s (local)", endpoint)
		} else {
			log.Printf("Storage: dynamodb table=%s region=%s", os.Getenv("DYNAMODB_TABLE"), os.Getenv("AWS_REGION"))
		}
	}

	if err := http.ListenAndServe(*addr, srv.routes()); err != nil {
		log.Fatal(err)
	}
}

// keep time import used (via time.Time in auth.go — it's in same package)
var _ = time.Now
