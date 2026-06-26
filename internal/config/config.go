// Package config handles application configuration loading and validation.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"stock-portfolio/internal/macro"
	"stock-portfolio/internal/models"
)

// Config holds the application configuration.
type Config struct {
	Stocks      []models.Stock    `json:"stocks"`
	Categories  []models.Category `json:"categories"`
	YahooAPI    YahooAPIConfig    `json:"yahoo_api"`
	AI          AIConfig          `json:"ai"`
	Twitter     TwitterConfig     `json:"twitter"`
	XGroups     []XGroup          `json:"xGroups"`
	Alerts      AlertConfig       `json:"alerts"`
	Concurrency int               `json:"concurrency"`
}

// AIConfig holds AI/Anthropic API configuration.
type AIConfig struct {
	Enabled   bool   `json:"enabled"`
	Mode      string `json:"mode"`     // "api" or "manual_prompt" (default: "manual_prompt")
	Provider  string `json:"provider"` // "gemini" or "anthropic"
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
}

// AlertConfig holds price alert configuration.
type AlertConfig struct {
	Enabled    bool      `json:"enabled"`
	Thresholds []float64 `json:"thresholds"` // e.g. [3.0, 5.0, 10.0] — absolute %, up and down
	Tickers    []string  `json:"tickers"`    // optional override — empty = all stocks in config
	StatePath  string    `json:"state_path"` // default: ".alert-state.json"
}

// XGroup represents a named group of Twitter/X accounts.
type XGroup struct {
	Name     string   `json:"name"`
	Accounts []string `json:"accounts"`
}

// TwitterConfig holds Twitter/X fetching configuration.
type TwitterConfig struct {
	Enabled             bool     `json:"enabled"`
	MaxTweets           int      `json:"max_tweets"`
	Provider            string   `json:"provider"`              // "nitter" (default) or "api"
	NitterInstances     []string `json:"nitter_instances"`      // tried in order until one works
	RequestDelaySeconds int      `json:"request_delay_seconds"` // delay between user fetches to avoid rate limiting (default: 1)
}

// YahooAPIConfig holds Yahoo Finance API configuration.
type YahooAPIConfig struct {
	BaseURL   string `json:"base_url"`
	Range     string `json:"range"`
	Interval  string `json:"interval"`
	UserAgent string `json:"user_agent"`
	Timeout   int    `json:"timeout_seconds"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		YahooAPI: YahooAPIConfig{
			BaseURL:   "https://query1.finance.yahoo.com/v8/finance/chart",
			Range:     "1y",
			Interval:  "1wk",
			UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
			Timeout:   30,
		},
		AI: AIConfig{
			Enabled:   true,
			Mode:      "manual_prompt",
			Provider:  "gemini",
			Model:     "gemini-2.0-flash",
			MaxTokens: 2000,
		},
		Concurrency: 10,
		Categories: []models.Category{
			{Name: "Metals", Emoji: "gold", Order: 1},
			{Name: "Cryptos", Emoji: "bitcoin", Order: 2},
			{Name: "Energy", Emoji: "zap", Order: 3},
			{Name: "USA", Emoji: "us", Order: 4},
			{Name: "Defense", Emoji: "shield", Order: 5},
			{Name: "France", Emoji: "fr", Order: 6},
			{Name: "Others", Emoji: "globe", Order: 7},
		},
	}
}

// Load reads configuration from a JSON file.
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// Validate checks if the configuration is valid.
func (c *Config) Validate() error {
	if len(c.Stocks) == 0 {
		return fmt.Errorf("no stocks configured")
	}

	for i, stock := range c.Stocks {
		if stock.Ticker == "" {
			return fmt.Errorf("stock %d: ticker is required", i)
		}
		if stock.Name == "" {
			return fmt.Errorf("stock %d (%s): name is required", i, stock.Ticker)
		}
	}

	if c.Concurrency < 1 {
		c.Concurrency = 10
	}

	return nil
}

// GetCategoryOrder returns a map of category name to order for sorting.
func (c *Config) GetCategoryOrder() map[string]int {
	order := make(map[string]int)
	for _, cat := range c.Categories {
		order[cat.Name] = cat.Order
	}
	return order
}

// GetCategoryEmoji returns a map of category name to emoji.
func (c *Config) GetCategoryEmoji() map[string]string {
	emojis := make(map[string]string)
	for _, cat := range c.Categories {
		emojis[cat.Name] = cat.Emoji
	}
	return emojis
}

// GetCategoryNarrative returns a map of category name to narrative description.
func (c *Config) GetCategoryNarrative() map[string]string {
	m := make(map[string]string)
	for _, cat := range c.Categories {
		if cat.Narrative != "" {
			m[cat.Name] = cat.Narrative
		}
	}
	return m
}

// GetCategoryNarrativeScore returns a map of category name to narrative score.
func (c *Config) GetCategoryNarrativeScore() map[string]int {
	m := make(map[string]int)
	for _, cat := range c.Categories {
		if cat.NarrativeScore != 0 {
			m[cat.Name] = cat.NarrativeScore
		}
	}
	return m
}

// LoadFromGist fetches configuration from a GitHub Gist file named "stock-config.json".
func LoadFromGist(gistID, token string) (*Config, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/gists/"+gistID, nil)
	if err != nil {
		return nil, fmt.Errorf("creating gist request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching gist: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var gist struct {
		Files map[string]struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return nil, fmt.Errorf("decoding gist response: %w", err)
	}

	const filename = "stock-config.json"
	f, ok := gist.Files[filename]
	if !ok {
		return nil, fmt.Errorf("file %q not found in gist %s", filename, gistID)
	}

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(f.Content), cfg); err != nil {
		return nil, fmt.Errorf("parsing gist config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating gist config: %w", err)
	}
	return cfg, nil
}

// LoadAuto loads configuration from GitHub Gist when GIST_ID is set,
// otherwise falls back to the local file at path.
func LoadAuto(path string) (*Config, error) {
	gistID := os.Getenv("GIST_ID")
	if gistID != "" {
		token := os.Getenv("GH_TOKEN")
		if token == "" {
			return nil, fmt.Errorf("GIST_ID is set but GH_TOKEN is missing")
		}
		slog.Info("loading config from GitHub Gist", "gist_id", gistID)
		return LoadFromGist(gistID, token)
	}
	return Load(path)
}

// LoadPromptFromGist fetches the prompt template from a GitHub Gist file named "prompt.txt".
func LoadPromptFromGist(gistID, token string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/gists/"+gistID, nil)
	if err != nil {
		return "", fmt.Errorf("creating gist request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching gist: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("github API returned %d", resp.StatusCode)
	}

	var gist struct {
		Files map[string]struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return "", fmt.Errorf("decoding gist response: %w", err)
	}

	const filename = "prompt.txt"
	f, ok := gist.Files[filename]
	if !ok {
		return "", fmt.Errorf("file %q not found in gist %s", filename, gistID)
	}
	return f.Content, nil
}

// LoadPrompt fetches the prompt template from the GitHub Gist (GIST_ID + GH_TOKEN required).
func LoadPrompt() (string, error) {
	gistID := os.Getenv("GIST_ID")
	if gistID == "" {
		return "", fmt.Errorf("GIST_ID is required to load the prompt")
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		return "", fmt.Errorf("GH_TOKEN is required to load the prompt")
	}
	slog.Info("loading prompt from GitHub Gist", "gist_id", gistID)
	return LoadPromptFromGist(gistID, token)
}

// StockFundamentals holds the pre-calculated data for a single stock.
type StockFundamentals struct {
	RSI           float64 `json:"rsi,omitempty"`
	TargetPrice   float64 `json:"target_price,omitempty"`
	TargetPct     float64 `json:"target_pct,omitempty"` // (target - price) / price * 100
	PEGRatio      float64 `json:"peg_ratio,omitempty"`
	PSGRatio      float64 `json:"psg_ratio,omitempty"`
	EVGrossProfit float64 `json:"ev_gross_profit,omitempty"`
	NextEarnings  string  `json:"next_earnings,omitempty"` // RFC3339 datetime
	Signal        string  `json:"signal,omitempty"`
	SignalNote    string  `json:"signal_note,omitempty"`
}

// StockDataFile is the structure written to stock-data.json in the Gist.
type StockDataFile struct {
	UpdatedAt   time.Time                    `json:"updated_at"`
	Stocks      map[string]StockFundamentals `json:"stocks"`
	MacroEvents []macro.Event                `json:"macro_events,omitempty"`
}

// SaveFundamentals writes computed fundamental data to stock-data.json in the Gist.
// It is a no-op when GIST_ID is not set.
func SaveFundamentals(results []*models.StockResult, signals map[string][2]string, macroEvents []macro.Event) error {
	gistID := os.Getenv("GIST_ID")
	if gistID == "" {
		return nil
	}
	token := os.Getenv("GH_TOKEN")
	if token == "" {
		return fmt.Errorf("GH_TOKEN is required to save fundamentals")
	}
	if macroEvents == nil {
		if existing := LoadStockData(); existing != nil {
			macroEvents = existing.MacroEvents
		}
	}

	data := StockDataFile{
		UpdatedAt:   time.Now().UTC(),
		Stocks:      make(map[string]StockFundamentals, len(results)),
		MacroEvents: macroEvents,
	}
	for _, r := range results {
		if r.Error != nil {
			continue
		}
		f := StockFundamentals{
			RSI:           r.RSI,
			TargetPrice:   r.TargetPrice,
			PEGRatio:      r.PEGRatio,
			PSGRatio:      r.PSGRatio,
			EVGrossProfit: r.EVGrossProfit,
		}
		if r.TargetPrice > 0 && r.CurrentPrice > 0 {
			f.TargetPct = (r.TargetPrice - r.CurrentPrice) / r.CurrentPrice * 100
		}
		if r.NextEarningsDate != nil {
			f.NextEarnings = r.NextEarningsDate.UTC().Format(time.RFC3339)
		}
		if sig, ok := signals[r.Stock.Ticker]; ok {
			f.Signal = sig[0]
			f.SignalNote = sig[1]
		}
		data.Stocks[r.Stock.Ticker] = f
	}

	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling fundamentals: %w", err)
	}

	payload, _ := json.Marshal(map[string]any{
		"files": map[string]any{
			"stock-data.json": map[string]any{"content": string(content)},
		},
	})

	req, err := http.NewRequest(http.MethodPatch, "https://api.github.com/gists/"+gistID, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating gist request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("patching gist: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github API returned %d: %s", resp.StatusCode, body)
	}
	return nil
}

// LoadFundamentals reads stock-data.json from the Gist and returns the existing data.
// Returns an empty map if GIST_ID is not set or the file is not found.
func LoadFundamentals() map[string]StockFundamentals {
	data := LoadStockData()
	if data == nil {
		return nil
	}
	return data.Stocks
}

// LoadStockData reads stock-data.json from the Gist.
func LoadStockData() *StockDataFile {
	gistID := os.Getenv("GIST_ID")
	token := os.Getenv("GH_TOKEN")
	if gistID == "" || token == "" {
		return nil
	}

	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/gists/"+gistID, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil
	}
	defer resp.Body.Close()

	var gist struct {
		Files map[string]struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&gist); err != nil {
		return nil
	}

	f, ok := gist.Files["stock-data.json"]
	if !ok {
		return nil
	}

	var data StockDataFile
	if err := json.Unmarshal([]byte(f.Content), &data); err != nil {
		return nil
	}
	return &data
}

// FindConfigFile searches for a config file in common locations.
func FindConfigFile() (string, error) {
	locations := []string{
		"config.json",
		"config/config.json",
		filepath.Join(os.Getenv("HOME"), ".stock-checker", "config.json"),
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			return loc, nil
		}
	}

	return "", fmt.Errorf("config file not found in: %v", locations)
}
