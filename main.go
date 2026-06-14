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
	"math"
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

const ctxUserID contextKey = "userID"
const ctxUser contextKey = "user"

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
				Meta       chartMeta `json:"meta"`
				Timestamp  []int64   `json:"timestamp"`
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

type ChartCandle struct {
	Time   int64   `json:"time"`
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume int64   `json:"volume"`
}

type ChartLinePoint struct {
	Time  int64   `json:"time"`
	Value float64 `json:"value"`
}

type ChartMACDPoint struct {
	Time      int64   `json:"time"`
	MACD      float64 `json:"macd"`
	Signal    float64 `json:"signal"`
	Histogram float64 `json:"histogram"`
}

type ChartPivot struct {
	Time  int64   `json:"time"`
	Kind  string  `json:"kind"`
	Price float64 `json:"price"`
	RSI   float64 `json:"rsi,omitempty"`
}

type ChartLevel struct {
	Kind     string  `json:"kind"`
	Price    float64 `json:"price"`
	Strength int     `json:"strength"`
	Touches  []int64 `json:"touches"`
}

type ChartDivergence struct {
	Kind      string  `json:"kind"`
	FromTime  int64   `json:"fromTime"`
	ToTime    int64   `json:"toTime"`
	FromPrice float64 `json:"fromPrice"`
	ToPrice   float64 `json:"toPrice"`
	FromRSI   float64 `json:"fromRsi"`
	ToRSI     float64 `json:"toRsi"`
}

type ChartChannel struct {
	Kind           string  `json:"kind"`
	SupportStart   int64   `json:"supportStart"`
	SupportEnd     int64   `json:"supportEnd"`
	SupportStartPx float64 `json:"supportStartPrice"`
	SupportEndPx   float64 `json:"supportEndPrice"`
	ResStart       int64   `json:"resistanceStart"`
	ResEnd         int64   `json:"resistanceEnd"`
	ResStartPx     float64 `json:"resistanceStartPrice"`
	ResEndPx       float64 `json:"resistanceEndPrice"`
	Slope          float64 `json:"slope"`
	Touches        int     `json:"touches"`
}

type ChartSetup struct {
	Kind              string  `json:"kind"`
	Title             string  `json:"title"`
	Bias              string  `json:"bias"`
	Confidence        string  `json:"confidence"`
	Detail            string  `json:"detail"`
	TriggerPrice      float64 `json:"triggerPrice,omitempty"`
	InvalidationPrice float64 `json:"invalidationPrice,omitempty"`
	PositiveOutcome   string  `json:"positiveOutcome"`
	NegativeOutcome   string  `json:"negativeOutcome"`
}

type ChartAnalysis struct {
	Bias    string       `json:"bias"`
	Summary string       `json:"summary"`
	Setups  []ChartSetup `json:"setups"`
}

type ChartResponse struct {
	Symbol      string            `json:"symbol"`
	Range       string            `json:"range"`
	Interval    string            `json:"interval"`
	Currency    string            `json:"currency"`
	Candles     []ChartCandle     `json:"candles"`
	RSI14       []ChartLinePoint  `json:"rsi14"`
	SMA50       []ChartLinePoint  `json:"sma50"`
	SMA100      []ChartLinePoint  `json:"sma100"`
	SMA200      []ChartLinePoint  `json:"sma200"`
	MACD        []ChartMACDPoint  `json:"macd"`
	Pivots      []ChartPivot      `json:"pivots"`
	Levels      []ChartLevel      `json:"levels"`
	Divergences []ChartDivergence `json:"divergences"`
	Channels    []ChartChannel    `json:"channels"`
	Analysis    ChartAnalysis     `json:"analysis"`
	Cached      bool              `json:"cached"`
	Stale       bool              `json:"stale"`
	UpdatedAt   time.Time         `json:"updatedAt"`
	ValidUntil  time.Time         `json:"validUntil"`
}

func chartCacheDir() string {
	return filepath.Join("files", "charts")
}

func chartCachePath(symbol, rng, interval string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, strings.ToUpper(symbol))
	return filepath.Join(chartCacheDir(), safe+"_"+rng+"_"+interval+".json")
}

func chartCacheTTL(rng, interval string) time.Duration {
	if rng == "1d" || interval == "5m" || interval == "15m" {
		return 10 * time.Minute
	}
	return 6 * time.Hour
}

func chartFetchRange(displayRange, interval string) string {
	if interval == "1wk" {
		switch displayRange {
		case "1mo", "3mo", "6mo", "1y", "2y":
			return "5y"
		default:
			return displayRange
		}
	}
	switch displayRange {
	case "1mo", "3mo", "6mo":
		return "1y"
	case "1y":
		return "2y"
	case "2y":
		return "5y"
	default:
		return displayRange
	}
}

func loadChartCache(path string, ttl time.Duration) (ChartResponse, bool) {
	var cr ChartResponse
	data, err := os.ReadFile(path)
	if err != nil {
		return cr, false
	}
	if err := json.Unmarshal(data, &cr); err != nil {
		return cr, false
	}
	cr.Cached = true
	cr.Stale = time.Since(cr.UpdatedAt) > ttl
	cr.ValidUntil = cr.UpdatedAt.Add(ttl)
	if len(cr.RSI14) == 0 && len(cr.Candles) > 14 {
		cr.RSI14 = calcRSI14(cr.Candles)
	}
	enrichChartIndicators(&cr)
	return cr, true
}

func saveChartCache(path string, cr ChartResponse) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(cr, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

func (s *Server) getChart(w http.ResponseWriter, r *http.Request) {
	symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	if symbol == "" {
		symbol = "BTC-USD"
	}
	rng := strings.TrimSpace(r.URL.Query().Get("range"))
	if rng == "" {
		rng = "1y"
	}
	interval := strings.TrimSpace(r.URL.Query().Get("interval"))
	if interval == "" {
		interval = "1d"
	}
	allowedRanges := map[string]bool{"1mo": true, "3mo": true, "6mo": true, "1y": true, "2y": true, "5y": true, "max": true}
	allowedIntervals := map[string]bool{"1d": true, "1wk": true}
	if !allowedRanges[rng] || !allowedIntervals[interval] {
		http.Error(w, "invalid range or interval", http.StatusBadRequest)
		return
	}

	ttl := chartCacheTTL(rng, interval)
	fetchRange := chartFetchRange(rng, interval)
	cachePath := chartCachePath(symbol, fetchRange, interval)
	if cached, ok := loadChartCache(cachePath, ttl); ok && !cached.Stale {
		cached = trimChartResponse(cached, rng, interval)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached)
		return
	}

	cr, err := fetchYahooChart(r.Context(), symbol, fetchRange, interval)
	if err != nil {
		if cached, ok := loadChartCache(cachePath, ttl); ok {
			cached.Stale = true
			cached = trimChartResponse(cached, rng, interval)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(cached)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	cr.Range = fetchRange
	cr.Interval = interval
	cr.UpdatedAt = time.Now()
	cr.ValidUntil = cr.UpdatedAt.Add(ttl)
	saveChartCache(cachePath, cr)
	cr = trimChartResponse(cr, rng, interval)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cr)
}

func trimChartResponse(cr ChartResponse, displayRange, interval string) ChartResponse {
	cr.Range = displayRange
	cr.Interval = interval
	if displayRange == "max" || len(cr.Candles) == 0 {
		return cr
	}
	cutoff := chartRangeCutoff(cr.Candles[len(cr.Candles)-1].Time, displayRange)
	filterCandles := func(candles []ChartCandle) []ChartCandle {
		idx := sort.Search(len(candles), func(i int) bool { return candles[i].Time >= cutoff })
		return candles[idx:]
	}
	filterLine := func(points []ChartLinePoint) []ChartLinePoint {
		idx := sort.Search(len(points), func(i int) bool { return points[i].Time >= cutoff })
		return points[idx:]
	}
	filterMACD := func(points []ChartMACDPoint) []ChartMACDPoint {
		idx := sort.Search(len(points), func(i int) bool { return points[i].Time >= cutoff })
		return points[idx:]
	}
	filterPivots := func(points []ChartPivot) []ChartPivot {
		idx := sort.Search(len(points), func(i int) bool { return points[i].Time >= cutoff })
		return points[idx:]
	}
	cr.Candles = filterCandles(cr.Candles)
	cr.RSI14 = filterLine(cr.RSI14)
	cr.SMA50 = filterLine(cr.SMA50)
	cr.SMA100 = filterLine(cr.SMA100)
	cr.SMA200 = filterLine(cr.SMA200)
	cr.MACD = filterMACD(cr.MACD)
	cr.Pivots = filterPivots(cr.Pivots)
	cr.Levels = calcChartLevels(cr.Pivots)
	cr.Divergences = calcRSIDivergences(cr.Candles, cr.Pivots)
	cr.Divergences = filterDivergencesByContext(cr.Divergences, cr.Candles, cr.SMA50, cr.SMA100, cr.SMA200, cr.Levels)
	cr.Channels = calcChartChannels(cr.Candles, cr.Pivots)
	cr.Analysis = calcChartAnalysis(cr)
	return cr
}

func chartRangeCutoff(lastTS int64, displayRange string) int64 {
	last := time.Unix(lastTS, 0).UTC()
	switch displayRange {
	case "1mo":
		return last.AddDate(0, -1, 0).Unix()
	case "3mo":
		return last.AddDate(0, -3, 0).Unix()
	case "6mo":
		return last.AddDate(0, -6, 0).Unix()
	case "1y":
		return last.AddDate(-1, 0, 0).Unix()
	case "2y":
		return last.AddDate(-2, 0, 0).Unix()
	case "5y":
		return last.AddDate(-5, 0, 0).Unix()
	default:
		return 0
	}
}

func fetchYahooChart(ctx context.Context, symbol, rng, interval string) (ChartResponse, error) {
	type chartResp struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol   string `json:"symbol"`
					Currency string `json:"currency"`
				} `json:"meta"`
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Open   []*float64 `json:"open"`
						High   []*float64 `json:"high"`
						Low    []*float64 `json:"low"`
						Close  []*float64 `json:"close"`
						Volume []*int64   `json:"volume"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error *struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}

	u := "https://query2.finance.yahoo.com/v8/finance/chart/" + url.PathEscape(symbol) +
		"?range=" + url.QueryEscape(rng) + "&interval=" + url.QueryEscape(interval)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ChartResponse{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ChartResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ChartResponse{}, fmt.Errorf("yahoo returned %s", resp.Status)
	}
	var yr chartResp
	if err := json.NewDecoder(resp.Body).Decode(&yr); err != nil {
		return ChartResponse{}, err
	}
	if yr.Chart.Error != nil {
		return ChartResponse{}, fmt.Errorf("yahoo error: %s", yr.Chart.Error.Description)
	}
	if len(yr.Chart.Result) == 0 || len(yr.Chart.Result[0].Indicators.Quote) == 0 {
		return ChartResponse{}, fmt.Errorf("no chart data")
	}
	res := yr.Chart.Result[0]
	q := res.Indicators.Quote[0]
	var candles []ChartCandle
	for i, ts := range res.Timestamp {
		if i >= len(q.Open) || i >= len(q.High) || i >= len(q.Low) || i >= len(q.Close) {
			continue
		}
		if q.Open[i] == nil || q.High[i] == nil || q.Low[i] == nil || q.Close[i] == nil {
			continue
		}
		var volume int64
		if i < len(q.Volume) && q.Volume[i] != nil {
			volume = *q.Volume[i]
		}
		candles = append(candles, ChartCandle{
			Time:   ts,
			Open:   *q.Open[i],
			High:   *q.High[i],
			Low:    *q.Low[i],
			Close:  *q.Close[i],
			Volume: volume,
		})
	}
	if len(candles) == 0 {
		return ChartResponse{}, fmt.Errorf("no candles")
	}
	cr := ChartResponse{Symbol: res.Meta.Symbol, Currency: res.Meta.Currency, Candles: candles}
	enrichChartIndicators(&cr)
	return cr, nil
}

func enrichChartIndicators(cr *ChartResponse) {
	if len(cr.Candles) == 0 {
		return
	}
	if len(cr.RSI14) == 0 {
		cr.RSI14 = calcRSI14(cr.Candles)
	}
	if len(cr.SMA50) == 0 {
		cr.SMA50 = calcSMA(cr.Candles, 50)
	}
	if len(cr.SMA100) == 0 {
		cr.SMA100 = calcSMA(cr.Candles, 100)
	}
	if len(cr.SMA200) == 0 {
		cr.SMA200 = calcSMA(cr.Candles, 200)
	}
	if len(cr.MACD) == 0 {
		cr.MACD = calcMACD(cr.Candles)
	}
	if len(cr.Pivots) == 0 {
		cr.Pivots = calcChartPivots(cr.Candles, cr.RSI14)
	}
	if len(cr.Levels) == 0 {
		cr.Levels = calcChartLevels(cr.Pivots)
	}
	cr.Divergences = calcRSIDivergences(cr.Candles, cr.Pivots)
	cr.Divergences = filterDivergencesByContext(cr.Divergences, cr.Candles, cr.SMA50, cr.SMA100, cr.SMA200, cr.Levels)
	if len(cr.Channels) == 0 {
		cr.Channels = calcChartChannels(cr.Candles, cr.Pivots)
	}
	cr.Analysis = calcChartAnalysis(*cr)
}

func calcRSI14(candles []ChartCandle) []ChartLinePoint {
	const period = 14
	if len(candles) <= period {
		return nil
	}
	gains := make([]float64, len(candles))
	losses := make([]float64, len(candles))
	for i := 1; i < len(candles); i++ {
		diff := candles[i].Close - candles[i-1].Close
		if diff >= 0 {
			gains[i] = diff
		} else {
			losses[i] = -diff
		}
	}

	var avgGain, avgLoss float64
	for i := 1; i <= period; i++ {
		avgGain += gains[i]
		avgLoss += losses[i]
	}
	avgGain /= period
	avgLoss /= period

	points := make([]ChartLinePoint, 0, len(candles)-period)
	points = append(points, ChartLinePoint{Time: candles[period].Time, Value: rsiValue(avgGain, avgLoss)})
	for i := period + 1; i < len(candles); i++ {
		avgGain = (avgGain*float64(period-1) + gains[i]) / period
		avgLoss = (avgLoss*float64(period-1) + losses[i]) / period
		points = append(points, ChartLinePoint{Time: candles[i].Time, Value: rsiValue(avgGain, avgLoss)})
	}
	return points
}

func calcSMA(candles []ChartCandle, period int) []ChartLinePoint {
	if period <= 0 || len(candles) < period {
		return nil
	}
	points := make([]ChartLinePoint, 0, len(candles)-period+1)
	var sum float64
	for i, c := range candles {
		sum += c.Close
		if i >= period {
			sum -= candles[i-period].Close
		}
		if i >= period-1 {
			points = append(points, ChartLinePoint{Time: c.Time, Value: sum / float64(period)})
		}
	}
	return points
}

func calcMACD(candles []ChartCandle) []ChartMACDPoint {
	const fast = 12
	const slow = 26
	const signalPeriod = 9
	if len(candles) < slow+signalPeriod-1 {
		return nil
	}
	emaFast := calcEMA(candles, fast)
	emaSlow := calcEMA(candles, slow)

	type macdBase struct {
		time  int64
		value float64
	}
	var base []macdBase
	for i := range candles {
		if math.IsNaN(emaFast[i]) || math.IsNaN(emaSlow[i]) {
			continue
		}
		base = append(base, macdBase{time: candles[i].Time, value: emaFast[i] - emaSlow[i]})
	}
	if len(base) < signalPeriod {
		return nil
	}

	var sum float64
	for i := 0; i < signalPeriod; i++ {
		sum += base[i].value
	}
	signal := sum / signalPeriod
	points := make([]ChartMACDPoint, 0, len(base)-signalPeriod+1)
	for i := signalPeriod - 1; i < len(base); i++ {
		if i > signalPeriod-1 {
			k := 2.0 / float64(signalPeriod+1)
			signal = base[i].value*k + signal*(1-k)
		}
		points = append(points, ChartMACDPoint{
			Time:      base[i].time,
			MACD:      base[i].value,
			Signal:    signal,
			Histogram: base[i].value - signal,
		})
	}
	return points
}

func calcEMA(candles []ChartCandle, period int) []float64 {
	values := make([]float64, len(candles))
	for i := range values {
		values[i] = math.NaN()
	}
	if period <= 0 || len(candles) < period {
		return values
	}
	var sum float64
	for i := 0; i < period; i++ {
		sum += candles[i].Close
	}
	ema := sum / float64(period)
	values[period-1] = ema
	k := 2.0 / float64(period+1)
	for i := period; i < len(candles); i++ {
		ema = candles[i].Close*k + ema*(1-k)
		values[i] = ema
	}
	return values
}

func calcChartPivots(candles []ChartCandle, rsi []ChartLinePoint) []ChartPivot {
	const left = 3
	const right = 3
	if len(candles) < left+right+1 {
		return nil
	}
	rsiByTime := make(map[int64]float64, len(rsi))
	for _, p := range rsi {
		rsiByTime[p.Time] = p.Value
	}
	var pivots []ChartPivot
	for i := left; i < len(candles)-right; i++ {
		hi := candles[i].High
		lo := candles[i].Low
		isHigh := true
		isLow := true
		for j := i - left; j <= i+right; j++ {
			if j == i {
				continue
			}
			if candles[j].High >= hi {
				isHigh = false
			}
			if candles[j].Low <= lo {
				isLow = false
			}
		}
		if isHigh {
			pivots = append(pivots, ChartPivot{Time: candles[i].Time, Kind: "high", Price: hi, RSI: rsiByTime[candles[i].Time]})
		}
		if isLow {
			pivots = append(pivots, ChartPivot{Time: candles[i].Time, Kind: "low", Price: lo, RSI: rsiByTime[candles[i].Time]})
		}
	}
	sort.SliceStable(pivots, func(i, j int) bool { return pivots[i].Time < pivots[j].Time })
	return pivots
}

func calcChartLevels(pivots []ChartPivot) []ChartLevel {
	if len(pivots) == 0 {
		return nil
	}
	var levels []ChartLevel
	for _, kind := range []string{"support", "resistance"} {
		var relevant []ChartPivot
		pivotKind := "low"
		if kind == "resistance" {
			pivotKind = "high"
		}
		for _, p := range pivots {
			if p.Kind == pivotKind {
				relevant = append(relevant, p)
			}
		}
		if len(relevant) == 0 {
			continue
		}
		sort.Slice(relevant, func(i, j int) bool { return relevant[i].Price < relevant[j].Price })
		tolerance := chartLevelTolerance(relevant)
		for _, p := range relevant {
			merged := false
			for i := range levels {
				if levels[i].Kind != kind {
					continue
				}
				if math.Abs(levels[i].Price-p.Price) <= tolerance {
					n := float64(levels[i].Strength)
					levels[i].Price = (levels[i].Price*n + p.Price) / (n + 1)
					levels[i].Strength++
					levels[i].Touches = append(levels[i].Touches, p.Time)
					merged = true
					break
				}
			}
			if !merged {
				levels = append(levels, ChartLevel{Kind: kind, Price: p.Price, Strength: 1, Touches: []int64{p.Time}})
			}
		}
	}
	sort.Slice(levels, func(i, j int) bool {
		if levels[i].Strength == levels[j].Strength {
			return levels[i].Price < levels[j].Price
		}
		return levels[i].Strength > levels[j].Strength
	})
	if len(levels) > 8 {
		levels = levels[:8]
	}
	sort.Slice(levels, func(i, j int) bool { return levels[i].Price < levels[j].Price })
	return levels
}

func chartLevelTolerance(pivots []ChartPivot) float64 {
	minP, maxP := pivots[0].Price, pivots[0].Price
	for _, p := range pivots[1:] {
		minP = math.Min(minP, p.Price)
		maxP = math.Max(maxP, p.Price)
	}
	return math.Max((maxP-minP)*0.012, maxP*0.004)
}

func calcRSIDivergences(candles []ChartCandle, pivots []ChartPivot) []ChartDivergence {
	var divs []ChartDivergence
	lastLow := ChartPivot{}
	lastHigh := ChartPivot{}
	for _, p := range pivots {
		if p.RSI == 0 {
			continue
		}
		switch p.Kind {
		case "low":
			if lastLow.Time != 0 && p.Price < lastLow.Price && p.RSI > lastLow.RSI+2 && divergenceSameSwing(candles, lastLow, p, "bullish") {
				divs = append(divs, ChartDivergence{
					Kind: "bullish", FromTime: lastLow.Time, ToTime: p.Time,
					FromPrice: lastLow.Price, ToPrice: p.Price, FromRSI: lastLow.RSI, ToRSI: p.RSI,
				})
			}
			lastLow = p
		case "high":
			if lastHigh.Time != 0 && p.Price > lastHigh.Price && p.RSI < lastHigh.RSI-2 && divergenceSameSwing(candles, lastHigh, p, "bearish") {
				divs = append(divs, ChartDivergence{
					Kind: "bearish", FromTime: lastHigh.Time, ToTime: p.Time,
					FromPrice: lastHigh.Price, ToPrice: p.Price, FromRSI: lastHigh.RSI, ToRSI: p.RSI,
				})
			}
			lastHigh = p
		}
	}
	return divs
}

func divergenceSameSwing(candles []ChartCandle, from, to ChartPivot, kind string) bool {
	if len(candles) == 0 || from.Time == 0 || to.Time == 0 || to.Time <= from.Time {
		return true
	}
	const maxSwingCandles = 70
	count := 0
	maxHigh := math.Max(from.Price, to.Price)
	minLow := math.Min(from.Price, to.Price)
	for _, c := range candles {
		if c.Time < from.Time || c.Time > to.Time {
			continue
		}
		count++
		maxHigh = math.Max(maxHigh, c.High)
		minLow = math.Min(minLow, c.Low)
	}
	if count == 0 {
		return true
	}
	if count > maxSwingCandles {
		return false
	}
	switch kind {
	case "bullish":
		base := math.Max(from.Price, to.Price)
		return base > 0 && maxHigh <= base*1.35
	case "bearish":
		base := math.Min(from.Price, to.Price)
		return base > 0 && minLow >= base*0.65
	default:
		return true
	}
}

func filterDivergencesByContext(divs []ChartDivergence, candles []ChartCandle, sma50, sma100, sma200 []ChartLinePoint, levels []ChartLevel) []ChartDivergence {
	if len(divs) == 0 {
		return nil
	}
	out := make([]ChartDivergence, 0, len(divs))
	for _, div := range divs {
		if div.Kind == "bullish" && divergenceOverlapsMASupportBounce(div, candles, sma50, sma100, sma200, levels) {
			continue
		}
		out = append(out, div)
	}
	return out
}

func divergenceOverlapsMASupportBounce(div ChartDivergence, candles []ChartCandle, sma50, sma100, sma200 []ChartLinePoint, levels []ChartLevel) bool {
	pivotCandle, ok := chartCandleAtOrAfter(candles, div.ToTime)
	if !ok || div.ToPrice <= 0 {
		return false
	}
	nearMA := priceNearLineAt(sma50, div.ToTime, div.ToPrice, 0.08) ||
		priceNearLineAt(sma100, div.ToTime, div.ToPrice, 0.08) ||
		priceNearLineAt(sma200, div.ToTime, div.ToPrice, 0.08)
	nearSupport := priceNearSupport(levels, div.ToPrice, 0.06)
	if !nearMA && !nearSupport {
		return false
	}
	recovered := pivotCandle.Close > pivotCandle.Open
	for _, c := range candles {
		if c.Time <= div.ToTime {
			continue
		}
		if c.Time > div.ToTime+int64(14*24*time.Hour/time.Second) {
			break
		}
		if c.Close >= div.ToPrice*1.08 || priceAboveLineAt(sma50, c.Time, c.Close, 0.01) {
			recovered = true
			break
		}
	}
	return recovered
}

func chartCandleAtOrAfter(candles []ChartCandle, ts int64) (ChartCandle, bool) {
	for _, c := range candles {
		if c.Time >= ts {
			return c, true
		}
	}
	return ChartCandle{}, false
}

func priceNearLineAt(points []ChartLinePoint, ts int64, price, tolerance float64) bool {
	value, ok := chartLineValueAtOrBefore(points, ts)
	return ok && value > 0 && math.Abs(price-value)/value <= tolerance
}

func priceAboveLineAt(points []ChartLinePoint, ts int64, price, tolerance float64) bool {
	value, ok := chartLineValueAtOrBefore(points, ts)
	return ok && value > 0 && price >= value*(1-tolerance)
}

func chartLineValueAtOrBefore(points []ChartLinePoint, ts int64) (float64, bool) {
	idx := sort.Search(len(points), func(i int) bool { return points[i].Time > ts }) - 1
	if idx < 0 {
		return 0, false
	}
	return points[idx].Value, true
}

func priceNearSupport(levels []ChartLevel, price, tolerance float64) bool {
	if price <= 0 {
		return false
	}
	for _, lvl := range levels {
		if lvl.Kind == "support" && lvl.Price > 0 && math.Abs(price-lvl.Price)/price <= tolerance {
			return true
		}
	}
	return false
}

func calcChartChannels(candles []ChartCandle, pivots []ChartPivot) []ChartChannel {
	var lows, highs []ChartPivot
	for _, p := range pivots {
		switch p.Kind {
		case "low":
			lows = append(lows, p)
		case "high":
			highs = append(highs, p)
		}
	}
	if len(lows) < 2 || len(highs) < 2 {
		return nil
	}

	var best *ChartChannel
	for li := maxInt(0, len(lows)-12); li < len(lows)-1; li++ {
		for lj := li + 1; lj < len(lows); lj++ {
			if lows[lj].Time == lows[li].Time {
				continue
			}
			window := chartChannelWindow(candles, lows[li].Time, lows[lj].Time)
			if len(window) < 20 {
				continue
			}
			avgPrice := chartAvgClose(window)
			if avgPrice == 0 {
				continue
			}
			slope := chartSlope(lows[li], lows[lj])
			tol := math.Max(avgPrice*0.018, 0.000001)

			offset := 0.0
			violations := 0
			for _, c := range window {
				base := chartLineAt(lows[li], slope, c.Time)
				if c.Low < base-tol {
					violations++
				}
				offset = math.Max(offset, c.High-base)
			}
			if offset < avgPrice*0.04 || violations > maxInt(1, len(window)/12) {
				continue
			}

			supportTouches := chartLineTouches(lows, lows[li], lows[lj], avgPrice)
			resTouches := chartOffsetLineTouches(highs, lows[li], slope, offset, avgPrice, lows[li].Time, lows[lj].Time)
			if supportTouches < 2 || resTouches < 2 {
				continue
			}

			kind := "horizontal"
			if slope > avgPrice*0.00000001 {
				kind = "ascending"
			} else if slope < -avgPrice*0.00000001 {
				kind = "descending"
			}
			touches := supportTouches + resTouches
			ch := ChartChannel{
				Kind:           kind,
				SupportStart:   window[0].Time,
				SupportEnd:     window[len(window)-1].Time,
				SupportStartPx: chartLineAt(lows[li], slope, window[0].Time),
				SupportEndPx:   chartLineAt(lows[li], slope, window[len(window)-1].Time),
				ResStart:       window[0].Time,
				ResEnd:         window[len(window)-1].Time,
				ResStartPx:     chartLineAt(lows[li], slope, window[0].Time) + offset,
				ResEndPx:       chartLineAt(lows[li], slope, window[len(window)-1].Time) + offset,
				Slope:          slope,
				Touches:        touches,
			}
			if best == nil || ch.Touches > best.Touches || (ch.Touches == best.Touches && ch.SupportEnd > best.SupportEnd) {
				best = &ch
			}
		}
	}
	if best == nil || best.Touches < 5 {
		return nil
	}
	return []ChartChannel{*best}
}

func chartChannelWindow(candles []ChartCandle, start, end int64) []ChartCandle {
	var out []ChartCandle
	for _, c := range candles {
		if c.Time >= start && c.Time <= end {
			out = append(out, c)
		}
	}
	return out
}

func chartAvgClose(candles []ChartCandle) float64 {
	if len(candles) == 0 {
		return 0
	}
	var sum float64
	for _, c := range candles {
		sum += c.Close
	}
	return sum / float64(len(candles))
}

func chartLineAt(anchor ChartPivot, slope float64, ts int64) float64 {
	return anchor.Price + slope*float64(ts-anchor.Time)
}

func chartSlope(a, b ChartPivot) float64 {
	dt := float64(b.Time - a.Time)
	if dt == 0 {
		return 0
	}
	return (b.Price - a.Price) / dt
}

func chartLineTouches(points []ChartPivot, a, b ChartPivot, avgPrice float64) int {
	tol := math.Max(avgPrice*0.018, 0.000001)
	slope := chartSlope(a, b)
	touches := 0
	for _, p := range points {
		if p.Time < a.Time || p.Time > b.Time {
			continue
		}
		expected := a.Price + slope*float64(p.Time-a.Time)
		if math.Abs(p.Price-expected) <= tol {
			touches++
		}
	}
	return touches
}

func chartOffsetLineTouches(points []ChartPivot, anchor ChartPivot, slope, offset, avgPrice float64, start, end int64) int {
	tol := math.Max(avgPrice*0.018, 0.000001)
	touches := 0
	for _, p := range points {
		if p.Time < start || p.Time > end {
			continue
		}
		expected := chartLineAt(anchor, slope, p.Time) + offset
		if math.Abs(p.Price-expected) <= tol {
			touches++
		}
	}
	return touches
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func calcChartAnalysis(cr ChartResponse) ChartAnalysis {
	if len(cr.Candles) < 30 {
		return ChartAnalysis{Bias: "neutral", Summary: "Not enough candles for a reliable technical setup."}
	}
	candles := cr.Candles
	last := candles[len(candles)-1]
	prev := candles[len(candles)-2]
	lastClose := last.Close
	if lastClose <= 0 {
		return ChartAnalysis{Bias: "neutral", Summary: "No usable close price for technical analysis."}
	}

	var setups []ChartSetup
	latestSMA50, hasSMA50 := latestChartLine(cr.SMA50)
	latestSMA200, hasSMA200 := latestChartLine(cr.SMA200)
	latestRSI, hasRSI := latestChartLine(cr.RSI14)
	latestMACD, hasMACD := latestChartMACD(cr.MACD)
	recent := candles[maxInt(0, len(candles)-30):]
	shortRecent := candles[maxInt(0, len(candles)-12):]
	recentHigh := chartRecentHigh(recent)
	recentLow := chartRecentLow(recent)
	shortRecentHigh := chartRecentHigh(shortRecent)
	shortRecentLow := chartRecentLow(shortRecent)

	if support, ok := nearestSupportLevel(cr.Levels, lastClose); ok && recentHigh > lastClose*1.18 {
		trigger := math.Max(prev.High, last.High)
		if hasSMA200 && latestSMA200 > trigger && latestSMA200 < recentHigh {
			trigger = latestSMA200
		}
		setups = append(setups, ChartSetup{
			Kind:              "breakout_rejection_support_test",
			Title:             "Breakout rejection / support test",
			Bias:              "decision under bearish pressure",
			Confidence:        "watch",
			Detail:            "Price rejected a recent expansion and is now testing a nearby support area.",
			TriggerPrice:      trigger,
			InvalidationPrice: support.Price * 0.97,
			PositiveOutcome:   "A reclaim of the nearest moving-average/rejection zone can stabilize the structure.",
			NegativeOutcome:   "A confirmed break below support favors a return toward the prior range.",
		})
	}

	if hasSMA200 && latestSMA200 > 0 {
		dist := (lastClose - latestSMA200) / latestSMA200
		extendedHigh := recentHigh > latestSMA200*1.08
		testingMA := math.Abs(dist) <= 0.035 || (last.Low <= latestSMA200*1.02 && lastClose >= latestSMA200*0.985)
		if extendedHigh && testingMA {
			confidence := "watch"
			if lastClose >= latestSMA200 && lastClose >= prev.Close {
				confidence = "medium"
			}
			setups = append(setups, ChartSetup{
				Kind:              "sma200_pullback",
				Title:             "Pullback on SMA200",
				Bias:              "bullish if defended",
				Confidence:        confidence,
				Detail:            "Price is retesting the long-term moving average after a strong upside move.",
				TriggerPrice:      math.Max(prev.High, last.High),
				InvalidationPrice: latestSMA200 * 0.985,
				PositiveOutcome:   "Bounce and close back above the retest zone can confirm continuation.",
				NegativeOutcome:   "Clean close below the SMA200 weakens the breakout and can pull price back into the old range.",
			})
		}
	}

	if lvl, ok := nearestRetestedResistance(cr.Levels, lastClose, recentHigh); ok {
		setups = append(setups, ChartSetup{
			Kind:              "breakout_retest",
			Title:             "Breakout retest",
			Bias:              "bullish if support holds",
			Confidence:        "watch",
			Detail:            "Price is near an old resistance area that may now act as support.",
			TriggerPrice:      math.Max(prev.High, last.High),
			InvalidationPrice: lvl.Price * 0.97,
			PositiveOutcome:   "Hold above the retest area followed by a higher close favors a continuation attempt.",
			NegativeOutcome:   "Failure back below the retest area suggests the breakout may be rejected.",
		})
	}

	if hasSMA50 && hasSMA200 && latestSMA50 > latestSMA200 && lastClose > latestSMA50 {
		recentLowNearSMA50 := shortRecentLow <= latestSMA50*1.08 && shortRecentLow >= latestSMA50*0.84
		priceRecovered := lastClose > prev.Close || lastClose > latestSMA50*1.03
		if recentLowNearSMA50 && priceRecovered {
			setups = append(setups, ChartSetup{
				Kind:              "ma_support_bounce",
				Title:             "Moving-average support bounce",
				Bias:              "bullish",
				Confidence:        "medium",
				Detail:            "Price pulled back into the rising moving-average zone and has reclaimed the short-term average.",
				TriggerPrice:      shortRecentHigh,
				InvalidationPrice: latestSMA50 * 0.97,
				PositiveOutcome:   "Holding above the short-term average keeps the trend-continuation setup alive.",
				NegativeOutcome:   "A close back below the moving-average zone would weaken the rebound.",
			})
		}
	}

	if ch, ok := latestChartChannel(cr.Channels); ok {
		chTop := ch.ResEndPx
		chBottom := ch.SupportEndPx
		if ch.ResEnd > 0 {
			chTop = ch.ResEndPx + ch.Slope*float64(last.Time-ch.ResEnd)
		}
		if ch.SupportEnd > 0 {
			chBottom = ch.SupportEndPx + ch.Slope*float64(last.Time-ch.SupportEnd)
		}
		if chTop > 0 && chBottom > 0 {
			wasAboveChannel := recentHigh > chTop*1.08
			isRetestingTop := wasAboveChannel && last.Low <= chTop*1.04 && lastClose >= chTop*0.96
			switch {
			case isRetestingTop:
				confidence := "watch"
				if lastClose >= chTop && lastClose >= prev.Close {
					confidence = "medium"
				}
				setups = append(setups, ChartSetup{
					Kind:              "channel_breakout_retest",
					Title:             "Channel breakout retest",
					Bias:              "bullish if defended",
					Confidence:        confidence,
					Detail:            "Price broke above the detected channel and is now retesting the upper boundary.",
					TriggerPrice:      math.Max(prev.High, last.High),
					InvalidationPrice: chTop * 0.97,
					PositiveOutcome:   "A close back above the retest zone can validate the breakout attempt.",
					NegativeOutcome:   "Failure below the upper boundary puts price back into the old channel/range.",
				})
			case lastClose > chTop*1.01:
				setups = append(setups, ChartSetup{
					Kind:              "channel_breakout",
					Title:             "Channel breakout",
					Bias:              "bullish",
					Confidence:        "medium",
					Detail:            "Price has broken above the detected consolidation/channel.",
					TriggerPrice:      last.High,
					InvalidationPrice: chTop * 0.98,
					PositiveOutcome:   "Staying above the channel top keeps the breakout structure intact.",
					NegativeOutcome:   "Return inside the channel increases the risk of a false breakout.",
				})
			case lastClose < chBottom*0.99 && !hasMajorDecisionSetup(setups):
				setups = append(setups, ChartSetup{
					Kind:              "channel_breakdown",
					Title:             "Channel breakdown",
					Bias:              "bearish",
					Confidence:        "medium",
					Detail:            "Price has broken below the detected channel support.",
					TriggerPrice:      chBottom,
					InvalidationPrice: chTop,
					PositiveOutcome:   "A reclaim of the channel would neutralize the breakdown.",
					NegativeOutcome:   "Staying below channel support favors downside continuation.",
				})
			case lastClose >= chBottom*0.99 && lastClose <= chTop*1.01 && !wasAboveChannel:
				setups = append(setups, ChartSetup{
					Kind:              "inside_channel",
					Title:             "Inside channel",
					Bias:              ch.Kind,
					Confidence:        "watch",
					Detail:            "Price remains inside the detected channel/range.",
					TriggerPrice:      chTop,
					InvalidationPrice: chBottom,
					PositiveOutcome:   "Break and hold above the upper line favors expansion.",
					NegativeOutcome:   "Break below the lower line favors range failure.",
				})
			}
		}
	}

	if div := latestRecentDivergence(cr.Divergences, last.Time); div.Kind != "" && isDivergenceStillActionable(div, setups, lastClose) {
		bias := "bullish"
		title := "Bullish RSI divergence"
		if div.Kind == "bearish" {
			bias = "bearish"
			title = "Bearish RSI divergence"
		}
		setups = append(setups, ChartSetup{
			Kind:            div.Kind + "_rsi_divergence",
			Title:           title,
			Bias:            bias,
			Confidence:      "watch",
			Detail:          "Recent pivot structure shows price and RSI moving in opposite directions.",
			PositiveOutcome: "Confirmation comes from price reclaiming the latest pivot area.",
			NegativeOutcome: "No confirmation means the divergence remains only a warning signal.",
		})
	}

	sort.SliceStable(setups, func(i, j int) bool {
		return chartSetupPriority(setups[i]) < chartSetupPriority(setups[j])
	})

	bias := chartAnalysisBias(setups, lastClose, latestSMA50, hasSMA50, latestSMA200, hasSMA200, latestRSI, hasRSI, latestMACD, hasMACD)
	return ChartAnalysis{
		Bias:    bias,
		Summary: chartAnalysisSummary(setups, bias, recentLow, recentHigh),
		Setups:  setups,
	}
}

func latestChartLine(points []ChartLinePoint) (float64, bool) {
	if len(points) == 0 {
		return 0, false
	}
	return points[len(points)-1].Value, true
}

func latestChartMACD(points []ChartMACDPoint) (ChartMACDPoint, bool) {
	if len(points) == 0 {
		return ChartMACDPoint{}, false
	}
	return points[len(points)-1], true
}

func chartRecentHigh(candles []ChartCandle) float64 {
	if len(candles) == 0 {
		return 0
	}
	high := candles[0].High
	for _, c := range candles[1:] {
		high = math.Max(high, c.High)
	}
	return high
}

func chartRecentLow(candles []ChartCandle) float64 {
	if len(candles) == 0 {
		return 0
	}
	low := candles[0].Low
	for _, c := range candles[1:] {
		low = math.Min(low, c.Low)
	}
	return low
}

func nearestSupportLevel(levels []ChartLevel, lastClose float64) (ChartLevel, bool) {
	if lastClose <= 0 {
		return ChartLevel{}, false
	}
	var best ChartLevel
	bestDist := math.MaxFloat64
	for _, lvl := range levels {
		if lvl.Kind != "support" || lvl.Price <= 0 {
			continue
		}
		dist := math.Abs(lastClose-lvl.Price) / lastClose
		if dist <= 0.055 && dist < bestDist {
			best = lvl
			bestDist = dist
		}
	}
	return best, best.Price > 0
}

func nearestRetestedResistance(levels []ChartLevel, lastClose, recentHigh float64) (ChartLevel, bool) {
	if lastClose <= 0 || recentHigh <= lastClose*1.04 {
		return ChartLevel{}, false
	}
	var best ChartLevel
	bestDist := math.MaxFloat64
	for _, lvl := range levels {
		if lvl.Kind != "resistance" || lvl.Price <= 0 || lvl.Price > lastClose*1.04 {
			continue
		}
		dist := math.Abs(lastClose-lvl.Price) / lastClose
		if dist <= 0.045 && dist < bestDist {
			best = lvl
			bestDist = dist
		}
	}
	return best, best.Price > 0
}

func latestChartChannel(channels []ChartChannel) (ChartChannel, bool) {
	if len(channels) == 0 {
		return ChartChannel{}, false
	}
	return channels[len(channels)-1], true
}

func latestRecentDivergence(divs []ChartDivergence, lastTime int64) ChartDivergence {
	const maxAge = int64(90 * 24 * time.Hour / time.Second)
	for i := len(divs) - 1; i >= 0; i-- {
		if lastTime-divs[i].ToTime <= maxAge {
			return divs[i]
		}
	}
	return ChartDivergence{}
}

func hasMajorDecisionSetup(setups []ChartSetup) bool {
	for _, s := range setups {
		if s.Kind == "breakout_rejection_support_test" || s.Kind == "sma200_pullback" || s.Kind == "breakout_retest" {
			return true
		}
	}
	return false
}

func chartSetupPriority(setup ChartSetup) int {
	switch setup.Kind {
	case "breakout_rejection_support_test":
		return 10
	case "sma200_pullback":
		return 20
	case "ma_support_bounce":
		return 25
	case "breakout_retest":
		return 30
	case "channel_breakout_retest":
		return 40
	case "channel_breakout", "channel_breakdown":
		return 60
	case "inside_channel":
		return 70
	}
	if strings.Contains(setup.Kind, "rsi_divergence") {
		return 50
	}
	return 100
}

func isDivergenceStillActionable(div ChartDivergence, setups []ChartSetup, lastClose float64) bool {
	if div.Kind == "bearish" {
		if lastClose > div.ToPrice*1.01 {
			return false
		}
		for _, s := range setups {
			if s.Kind == "ma_support_bounce" || s.Kind == "channel_breakout" || s.Kind == "breakout_retest" {
				return false
			}
		}
	}
	if div.Kind == "bullish" && lastClose < div.ToPrice*0.99 {
		return false
	}
	return true
}

func chartAnalysisBias(setups []ChartSetup, lastClose, sma50 float64, hasSMA50 bool, sma200 float64, hasSMA200 bool, rsi float64, hasRSI bool, macd ChartMACDPoint, hasMACD bool) string {
	score := 0
	hasActiveRetest := false
	hasDecisionSetup := false
	for _, s := range setups {
		if strings.Contains(s.Bias, "bullish") {
			score += 2
		}
		if strings.Contains(s.Bias, "bearish") {
			score -= 2
		}
		if strings.Contains(s.Kind, "retest") || strings.Contains(s.Kind, "pullback") {
			hasActiveRetest = true
		}
		if strings.Contains(s.Bias, "decision") || s.Kind == "breakout_rejection_support_test" {
			hasDecisionSetup = true
		}
	}
	if hasSMA50 && lastClose > sma50 {
		score++
	} else if hasSMA50 && lastClose < sma50 {
		score--
	}
	if hasSMA200 && lastClose > sma200 {
		score++
	} else if hasSMA200 && lastClose < sma200 {
		score--
	}
	if hasRSI && rsi >= 55 && rsi <= 75 {
		score++
	} else if hasRSI && rsi < 45 {
		score--
	}
	if hasMACD && macd.Histogram > 0 {
		score++
	} else if hasMACD && macd.Histogram < 0 {
		score--
	}
	if score >= 3 {
		return "bullish"
	}
	if (hasActiveRetest || hasDecisionSetup) && score > -5 {
		return "decision"
	}
	if score <= -3 {
		return "bearish"
	}
	return "neutral"
}

func chartAnalysisSummary(setups []ChartSetup, bias string, recentLow, recentHigh float64) string {
	if len(setups) == 0 {
		return "No high-confidence setup detected. Price is between recent support and resistance zones."
	}
	switch bias {
	case "bullish":
		return "Bullish context: price has reclaimed an important support/moving-average area after a pullback."
	case "bearish":
		return "Bearish context. Reclaiming the failed level is needed before the setup improves."
	case "decision":
		return "Decision zone: price is testing support after a sharp rejection, so confirmation matters more than the current indicator bias."
	default:
		if recentLow > 0 && recentHigh > 0 {
			return "Decision zone: price is reacting around an important level after a recent expansion."
		}
		return "Decision zone: wait for confirmation from the active setup."
	}
}

func rsiValue(avgGain, avgLoss float64) float64 {
	if avgLoss == 0 {
		if avgGain == 0 {
			return 50
		}
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - (100 / (1 + rs))
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

// ── Apartment investment — filesystem storage ────────────────────────────────

func apartmentDir() string {
	return filepath.Join("files", "apartment")
}

func apartmentParamsPath() string {
	return filepath.Join(apartmentDir(), "params.json")
}

func (s *Server) getApartmentParams(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile(apartmentParamsPath())
	if os.IsNotExist(err) {
		data = []byte("{}")
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (s *Server) putApartmentParams(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	data, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		http.Error(w, "invalid params", http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(apartmentDir(), 0o755); err != nil {
		http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(apartmentParamsPath(), data, 0o644); err != nil {
		http.Error(w, "write: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── Yuh — filesystem storage ─────────────────────────────────────────────────

type PerfYuhResponse struct {
	Files     []string        `json:"files"`
	Positions []OpenPosition  `json:"positions"`
	Years     []YuhYearRecord `json:"years"`
	Cash      YuhCashSummary  `json:"cash"`
}

type YuhCashSummary struct {
	Deposited float64 `json:"deposited"`
	Withdrawn float64 `json:"withdrawn"`
}

func perfYuhDir(userID string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, userID)
	return filepath.Join("files", "performance", safe, "yuh")
}

func perfYuhResultPath(userID string) string {
	return filepath.Join(perfYuhDir(userID), "result.json")
}

func perfYuhPricesPath(userID string) string {
	return filepath.Join(perfYuhDir(userID), "prices.json")
}

func perfYuhCashPath(userID string) string {
	return filepath.Join(perfYuhDir(userID), "cash.json")
}

func loadYuhCash(userID string) YuhCashSummary {
	var cash YuhCashSummary
	data, err := os.ReadFile(perfYuhCashPath(userID))
	if err == nil {
		_ = json.Unmarshal(data, &cash)
	}
	return cash
}

func saveYuhCash(userID string, cash YuhCashSummary) error {
	if cash.Deposited < 0 || cash.Withdrawn < 0 {
		return fmt.Errorf("cash amounts must be positive")
	}
	if err := os.MkdirAll(perfYuhDir(userID), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cash, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(perfYuhCashPath(userID), data, 0o644)
}

func (s *Server) computeAndCacheYuh(userID string) (*PerfYuhResponse, error) {
	dir := perfYuhDir(userID)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return &PerfYuhResponse{Cash: loadYuhCash(userID)}, nil
	}
	if err != nil {
		return nil, err
	}
	var allTxs []YuhTransaction
	var fileNames []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(strings.ToUpper(name), ".CSV") || name == "result.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		txs, err := parseYuhCSV(string(data))
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		allTxs = append(allTxs, txs...)
		fileNames = append(fileNames, name)
	}
	sort.Slice(allTxs, func(i, j int) bool { return allTxs[i].Date < allTxs[j].Date })
	sort.Strings(fileNames)

	// Collect unique (currency, year) pairs from all transactions to fetch annual avg FX rates.
	type curYear struct{ cur, year string }
	pairs := map[curYear]struct{}{}
	addPair := func(cur, date string) {
		if cur != "" && cur != "EUR" && len(date) >= 4 {
			pairs[curYear{cur, date[:4]}] = struct{}{}
		}
	}
	for _, t := range allTxs {
		switch t.ActivityType {
		case "INVEST_ORDER_EXECUTED":
			addPair(t.DebitCurrency, t.Date)  // BUY cost currency
			addPair(t.CreditCurrency, t.Date) // SELL proceeds currency
		case "CASH_TRANSACTION_RELATED_OTHER", "CASH_TRANSACTION_OTHER":
			addPair(t.CreditCurrency, t.Date) // dividend currency
		}
	}
	fxByYearCur := map[string]float64{} // "CHF:2024" → avg EURCHF rate for 2024
	if len(pairs) > 0 {
		fxCtx, fxCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer fxCancel()
		var fxMu sync.Mutex
		var fxWg sync.WaitGroup
		for p := range pairs {
			p := p
			fxWg.Add(1)
			go func() {
				defer fxWg.Done()
				yr := 0
				fmt.Sscanf(p.year, "%d", &yr)
				rate := fetchAnnualAvgFXRate(fxCtx, "EUR"+p.cur+"=X", yr)
				if rate > 0 {
					fxMu.Lock()
					fxByYearCur[p.cur+":"+p.year] = rate
					fxMu.Unlock()
				}
			}()
		}
		fxWg.Wait()
	}

	positions, yearRecords := calcYuhData(allTxs, fxByYearCur)
	resp := &PerfYuhResponse{Files: fileNames, Positions: positions, Years: yearRecords, Cash: loadYuhCash(userID)}
	if data, err := json.Marshal(resp); err == nil {
		_ = os.WriteFile(perfYuhResultPath(userID), data, 0o644)
	}
	return resp, nil
}

func (s *Server) getYuh(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromCtx(r)
	var resp *PerfYuhResponse
	if data, err := os.ReadFile(perfYuhResultPath(userID)); err == nil {
		var cached PerfYuhResponse
		if json.Unmarshal(data, &cached) == nil && cached.Positions != nil && cached.Years != nil {
			resp = &cached
		}
	}
	if resp == nil {
		var err error
		resp, err = s.computeAndCacheYuh(userID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	resp.Cash = loadYuhCash(userID)

	if len(resp.Positions) > 0 {
		syms := make([]string, len(resp.Positions))
		for i, p := range resp.Positions {
			syms[i] = p.Symbol
		}

		pricesPath := perfYuhPricesPath(userID)
		var quotes map[string]yahooQuote
		if c := loadPricesCacheFrom(pricesPath); c != nil && len(c.Quotes) > 0 {
			quotes = c.Quotes
		} else {
			fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			quotes = fetchYahooPrices(fetchCtx, syms)
			if len(quotes) > 0 {
				savePricesCacheTo(pricesPath, quotes)
			}
		}

		// Costs are already in EUR (converted via annual avg FX rates at compute time).
		// Only price currency needs today's spot rate for EUR conversion.
		priceCurrencies := map[string]struct{}{}
		for _, p := range resp.Positions {
			if q, ok := quotes[p.Symbol]; ok && q.Currency != "" && q.Currency != "EUR" {
				priceCurrencies[q.Currency] = struct{}{}
			}
		}
		spotRates := map[string]float64{}
		if len(priceCurrencies) > 0 {
			fxCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var fxMu sync.Mutex
			var fxWg sync.WaitGroup
			for cur := range priceCurrencies {
				cur := cur
				fxWg.Add(1)
				go func() {
					defer fxWg.Done()
					if q, ok := fetchYahooPrice(fxCtx, "EUR"+cur+"=X"); ok {
						fxMu.Lock()
						spotRates[cur] = q.Price
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
			priceEUR := q.Price
			if q.Currency != "EUR" {
				if rate, ok := spotRates[q.Currency]; ok && rate > 0 {
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

func (s *Server) putYuhCash(w http.ResponseWriter, r *http.Request) {
	var body YuhCashSummary
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := saveYuhCash(userIDFromCtx(r), body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) postYuh(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	txs, err := parseYuhCSV(string(body))
	if err != nil {
		http.Error(w, "invalid CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(txs) == 0 {
		http.Error(w, "no transactions found", http.StatusBadRequest)
		return
	}

	// Derive a stable filename from the first line of the file (report ID in original name).
	// The multipart filename isn't available here, so use a hash of content.
	filename := fmt.Sprintf("yuh_%x.csv", fnv32(body))

	userID := userIDFromCtx(r)
	if err := os.MkdirAll(perfYuhDir(userID), 0o755); err != nil {
		http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(perfYuhDir(userID), filename), body, 0o644); err != nil {
		http.Error(w, "write: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = os.Remove(perfYuhResultPath(userID))
	resp, err := s.computeAndCacheYuh(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) deleteYuhFile(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimPrefix(r.URL.Path, "/api/performance/yuh/")
	if filename == "" || strings.Contains(filename, "/") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	userID := userIDFromCtx(r)
	_ = os.Remove(filepath.Join(perfYuhDir(userID), filename))
	_ = os.Remove(perfYuhResultPath(userID))
	resp, err := s.computeAndCacheYuh(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ── Vinted — filesystem storage ──────────────────────────────────────────────

type PerfVintedResponse struct {
	Files        []string            `json:"files"`
	Transactions []VintedTransaction `json:"transactions"`
	Summary      VintedSummary       `json:"summary"`
}

func perfVintedDir(userID string) string {
	safe := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, userID)
	return filepath.Join("files", "performance", safe, "vinted")
}

func computeVinted(userID string) (*PerfVintedResponse, error) {
	dir := perfVintedDir(userID)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return &PerfVintedResponse{}, nil
	}
	if err != nil {
		return nil, err
	}

	var txs []VintedTransaction
	var fileNames []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(strings.ToUpper(name), ".CSV") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		parsed, err := parseVintedCSV(string(data))
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", name, err)
		}
		for i := range parsed {
			parsed[i].SourceFile = name
		}
		txs = append(txs, parsed...)
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	sort.SliceStable(txs, func(i, j int) bool {
		if txs[i].Date == txs[j].Date {
			if txs[i].Type == txs[j].Type && txs[i].SourceOrder != txs[j].SourceOrder {
				return txs[i].SourceOrder < txs[j].SourceOrder
			}
			return txs[i].Item < txs[j].Item
		}
		return txs[i].Date > txs[j].Date
	})
	return &PerfVintedResponse{Files: fileNames, Transactions: txs, Summary: summarizeVinted(txs)}, nil
}

func (s *Server) getVinted(w http.ResponseWriter, r *http.Request) {
	resp, err := computeVinted(userIDFromCtx(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) postVinted(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	txs, err := parseVintedCSV(string(body))
	if err != nil {
		http.Error(w, "invalid CSV: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(txs) == 0 {
		http.Error(w, "no transactions found", http.StatusBadRequest)
		return
	}

	filename := fmt.Sprintf("vinted_%x.csv", fnv32(body))
	userID := userIDFromCtx(r)
	if err := os.MkdirAll(perfVintedDir(userID), 0o755); err != nil {
		http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(filepath.Join(perfVintedDir(userID), filename), body, 0o644); err != nil {
		http.Error(w, "write: "+err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := computeVinted(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) deleteVintedFile(w http.ResponseWriter, r *http.Request) {
	filename := strings.TrimPrefix(r.URL.Path, "/api/performance/vinted/")
	if filename == "" || strings.Contains(filename, "/") {
		http.Error(w, "invalid filename", http.StatusBadRequest)
		return
	}
	userID := userIDFromCtx(r)
	_ = os.Remove(filepath.Join(perfVintedDir(userID), filename))
	resp, err := computeVinted(userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func fnv32(data []byte) uint32 {
	h := uint32(2166136261)
	for _, b := range data {
		h ^= uint32(b)
		h *= 16777619
	}
	return h
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

func (s *Server) getYuhAIRec(w http.ResponseWriter, r *http.Request) {
	content, err := s.store.GetYuhAIRec(r.Context(), userIDFromCtx(r))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"content": content})
}

func (s *Server) putYuhAIRec(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if err := s.store.PutYuhAIRec(r.Context(), userIDFromCtx(r), body.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

const pricesCacheTTL = 4 * time.Hour

type pricesCache struct {
	UpdatedAt time.Time             `json:"updatedAt"`
	Quotes    map[string]yahooQuote `json:"quotes"`
}

func loadPricesCache(userID string) *pricesCache {
	return loadPricesCacheFrom(perfTRPricesPath(userID))
}

func savePricesCache(userID string, quotes map[string]yahooQuote) {
	savePricesCacheTo(perfTRPricesPath(userID), quotes)
}

func loadPricesCacheFrom(path string) *pricesCache {
	data, err := os.ReadFile(path)
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

func savePricesCacheTo(path string, quotes map[string]yahooQuote) {
	c := pricesCache{UpdatedAt: time.Now(), Quotes: quotes}
	if data, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(path, data, 0o644)
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
			if r.Error != "" || len(r.Data) == 0 {
				continue
			}
			// Prefer the first match whose exchange code is in our suffix map
			// (so e.g. FR ISINs resolve to the Paris listing, not a German one).
			// Fall back to the first result (covers US stocks with no suffix).
			best := r.Data[0]
			for _, m := range r.Data {
				if _, ok := openFIGIToYahooSuffix[m.ExchCode]; ok {
					best = m
					break
				}
			}
			mapping[isins[start+i]] = figiToYahoo(best.Ticker, best.ExchCode)
		}
	}
	return mapping
}

// fetchAnnualAvgFXRate returns the average of monthly closes for fxTicker (e.g. "EURCHF=X")
// over the given calendar year. For the current year it returns today's rate instead.
func fetchAnnualAvgFXRate(ctx context.Context, fxTicker string, year int) float64 {
	if year >= time.Now().Year() {
		if q, ok := fetchYahooPrice(ctx, fxTicker); ok {
			return q.Price
		}
		return 0
	}
	loc := time.UTC
	period1 := time.Date(year, 1, 1, 0, 0, 0, 0, loc).Unix()
	period2 := time.Date(year+1, 1, 1, 0, 0, 0, 0, loc).Unix()
	u := fmt.Sprintf("https://query2.finance.yahoo.com/v8/finance/chart/%s?period1=%d&period2=%d&interval=1mo",
		url.PathEscape(fxTicker), period1, period2)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return 0
	}
	defer resp.Body.Close()
	var cr struct {
		Chart struct {
			Result []struct {
				Indicators struct {
					Quote []struct {
						Close []float64 `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}
	if json.NewDecoder(resp.Body).Decode(&cr) != nil || len(cr.Chart.Result) == 0 {
		return 0
	}
	quotes := cr.Chart.Result[0].Indicators.Quote
	if len(quotes) == 0 {
		return 0
	}
	var sum float64
	var n int
	for _, c := range quotes[0].Close {
		if c > 0 {
			sum += c
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func fetchYahooPrice(ctx context.Context, ticker string) (yahooQuote, bool) {
	type chartMeta struct {
		RegularMarketPrice float64 `json:"regularMarketPrice"`
		Currency           string  `json:"currency"`
	}
	type chartResp struct {
		Chart struct {
			Result []struct {
				Meta chartMeta `json:"meta"`
			} `json:"result"`
			Error *struct{ Code string } `json:"error"`
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
	_ = os.Remove(perfTRPricesPath(userID)) // invalidate price cache so new ISINs are re-resolved
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
	_ = os.Remove(perfTRResultPath(userID)) // invalidate result cache
	_ = os.Remove(perfTRPricesPath(userID)) // invalidate price cache
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
				if err == nil {
					f.Close()
					exists = true
				}
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
	protected.HandleFunc("/api/charts", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.getChart(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

	protected.HandleFunc("/api/performance/yuh/ai-rec", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getYuhAIRec(w, r)
		case http.MethodPut:
			s.putYuhAIRec(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/performance/yuh/cash", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			s.putYuhCash(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/performance/yuh/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.deleteYuhFile(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/performance/yuh", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getYuh(w, r)
		case http.MethodPost:
			s.postYuh(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	protected.HandleFunc("/api/performance/vinted/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.deleteVintedFile(w, r)
		} else {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	protected.HandleFunc("/api/performance/vinted", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getVinted(w, r)
		case http.MethodPost:
			s.postVinted(w, r)
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

	protected.HandleFunc("/api/apartment/params", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.getApartmentParams(w, r)
		case http.MethodPut:
			s.putApartmentParams(w, r)
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
