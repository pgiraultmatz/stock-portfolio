package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

// ---- Google OAuth ----

func (s *Server) authGoogleLogin(w http.ResponseWriter, r *http.Request) {
	if s.googleOAuth == nil {
		http.Error(w, "Google OAuth not configured", http.StatusServiceUnavailable)
		return
	}
	state := randomHex(16)
	s.pendingStates.Store(state, time.Now().Add(10*time.Minute))
	url := s.googleOAuth.AuthCodeURL(state, oauth2.AccessTypeOnline, oauth2.SetAuthURLParam("prompt", "select_account"))
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) authGoogleCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	expiry, ok := s.pendingStates.LoadAndDelete(state)
	if !ok || time.Now().After(expiry.(time.Time)) {
		http.Error(w, "invalid or expired state", http.StatusBadRequest)
		return
	}

	token, err := s.googleOAuth.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "failed to exchange code", http.StatusInternalServerError)
		return
	}

	client := s.googleOAuth.Client(r.Context(), token)
	resp, err := client.Get("https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		http.Error(w, "failed to get user info", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	var info struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	json.NewDecoder(resp.Body).Decode(&info)

	user, err := s.store.GetUserByGoogleID(r.Context(), info.ID)
	if errors.Is(err, ErrNotFound) {
		user = &User{
			ID:          randomHex(16),
			Email:       info.Email,
			GoogleID:    info.ID,
			DisplayName: info.Name,
			Verified:    true, // Google already verified the email
		}
		if err := s.store.CreateUser(r.Context(), user); errors.Is(err, ErrConflict) {
			// Email/password account already exists — link Google ID to it.
			existing, lookupErr := s.store.GetUserByEmail(r.Context(), info.Email)
			if lookupErr != nil {
				http.Error(w, "failed to link account", http.StatusInternalServerError)
				return
			}
			if linkErr := s.store.LinkGoogleID(r.Context(), existing.ID, info.ID); linkErr != nil {
				http.Error(w, "failed to link google account", http.StatusInternalServerError)
				return
			}
			user = existing
		} else if err != nil {
			http.Error(w, "failed to create user", http.StatusInternalServerError)
			return
		}
		if err := seedDefaultPortfolio(r.Context(), s.store, user.ID); err != nil {
			log.Printf("failed to seed default portfolio for %s: %v", user.ID, err)
		}
	} else if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}

	if err := s.setSessionCookie(w, r.Context(), user.ID); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// ---- Email / password ----

func (s *Server) authLoginPage(w http.ResponseWriter, r *http.Request) {
	data, err := staticFiles.ReadFile("static/login.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}

func (s *Server) authLoginPost(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	user, err := s.store.GetUserByEmail(r.Context(), email)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	if user.PasswordHash == "" {
		http.Error(w, "this account uses Google login", http.StatusBadRequest)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	if err := s.setSessionCookie(w, r.Context(), user.ID); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) authRegisterPost(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	name := strings.TrimSpace(r.FormValue("name"))

	if email == "" || password == "" {
		http.Error(w, "email and password required", http.StatusBadRequest)
		return
	}
	if len(password) < 8 {
		http.Error(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	user := &User{
		ID:           randomHex(16),
		Email:        email,
		DisplayName:  name,
		PasswordHash: string(hash),
		Verified:     false,
	}
	if err := s.store.CreateUser(r.Context(), user); err != nil {
		if errors.Is(err, ErrConflict) {
			http.Error(w, "email already registered", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := seedDefaultPortfolio(r.Context(), s.store, user.ID); err != nil {
		log.Printf("failed to seed default portfolio for %s: %v", user.ID, err)
	}

	token := randomHex(32)
	if err := s.store.CreateVerificationToken(r.Context(), token, user.ID, time.Now().Add(24*time.Hour)); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	verifyURL := s.baseURL + "/auth/verify?token=" + token
	go func() {
		if err := s.mailer.SendVerificationEmail(context.Background(), user.Email, user.DisplayName, verifyURL); err != nil {
			log.Printf("failed to send verification email: %v", err)
		}
	}()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Check your email</title></head><body>
<h2>Check your email</h2>
<p>We sent a confirmation link to <strong>%s</strong>.</p>
<p>Click the link to activate your account.</p>
</body></html>`, email)
}

func (s *Server) authVerify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}

	userID, err := s.store.ConsumeVerificationToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "invalid or expired link", http.StatusBadRequest)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := s.store.VerifyUser(r.Context(), userID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := s.setSessionCookie(w, r.Context(), userID); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?welcome=1", http.StatusFound)
}

// ---- Default portfolio ----

func seedDefaultPortfolio(ctx context.Context, store Store, userID string) error {
	categories := []Category{
		{Name: "Metals", Emoji: "🥇", Order: 0},
		{Name: "Cryptos", Emoji: "🪙", Order: 1},
		{Name: "Energy", Emoji: "⚡", Order: 2},
		{Name: "USA", Emoji: "🇺🇸", Order: 3},
	}
	stocks := []Stock{
		{Ticker: "SGLD.L", Name: "Invesco Physical Gold", Category: "Metals"},
		{Ticker: "BTC-USD", Name: "Bitcoin USD", Category: "Cryptos"},
		{Ticker: "IQQE.DE", Name: "iShares MSCI World Energy", Category: "Energy"},
		{Ticker: "VUSA.L", Name: "Vanguard S&P 500 UCITS ETF", Category: "USA"},
	}
	if err := store.ReplaceCategories(ctx, userID, categories); err != nil {
		return err
	}
	return store.ReplaceStocks(ctx, userID, stocks)
}

// ---- Logout ----

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		s.store.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:    "session",
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
		MaxAge:  -1,
	})
	http.Redirect(w, r, "/auth/login", http.StatusFound)
}

// ---- Session helpers ----

func (s *Server) setSessionCookie(w http.ResponseWriter, ctx context.Context, userID string) error {
	token := randomHex(32)
	expiresAt := time.Now().Add(30 * 24 * time.Hour)
	if err := s.store.CreateSession(ctx, token, userID, expiresAt); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) getSession(r *http.Request) (string, bool) {
	cookie, err := r.Cookie("session")
	if err != nil {
		return "", false
	}
	sess, err := s.store.GetSession(r.Context(), cookie.Value)
	if err != nil {
		return "", false
	}
	return sess.UserID, true
}
