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
	Ticker   string `json:"ticker"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

type Category struct {
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
	Order int    `json:"order"`
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

func (s *Server) saveConfig(w http.ResponseWriter, _ *http.Request) {
	// Portfolio is persisted immediately on every write — nothing to do.
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "saved"})
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
		data, _ := staticFiles.ReadFile("static/deleted.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})
	mux.HandleFunc("/auth/google", s.authGoogleLogin)
	mux.HandleFunc("/auth/google/callback", s.authGoogleCallback)
	mux.HandleFunc("/auth/verify", s.authVerify)
	mux.HandleFunc("/auth/logout", s.authLogout)

	// Protected routes
	staticFS, _ := fs.Sub(staticFiles, "static")
	protected := http.NewServeMux()
	protected.Handle("/", http.FileServer(http.FS(staticFS)))

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
		if r.Method == http.MethodDelete {
			s.deleteCategory(w, r)
		} else {
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
