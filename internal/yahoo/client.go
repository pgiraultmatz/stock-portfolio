// Package yahoo provides a client for the Yahoo Finance API.
package yahoo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"

	"stock-portfolio/internal/config"
)

// Client is a Yahoo Finance API client.
type Client struct {
	httpClient *http.Client
	config     config.YahooAPIConfig
	crumb      string
	crumbOnce  sync.Once
}

// NewClient creates a new Yahoo Finance API client.
func NewClient(cfg config.YahooAPIConfig) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.Timeout) * time.Second,
			Jar:     jar,
		},
		config: cfg,
	}
}

// initCrumb fetches the Yahoo Finance session cookie + crumb (called once).
func (c *Client) initCrumb(ctx context.Context) error {
	var initErr error
	c.crumbOnce.Do(func() {
		// 1. Hit the consent/login page to get session cookies.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://fc.yahoo.com", nil)
		if err != nil {
			initErr = err
			return
		}
		req.Header.Set("User-Agent", c.config.UserAgent)
		resp, err := c.httpClient.Do(req)
		if err != nil {
			initErr = err
			return
		}
		resp.Body.Close()

		// 2. Fetch the crumb.
		req2, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://query2.finance.yahoo.com/v1/test/getcrumb", nil)
		if err != nil {
			initErr = err
			return
		}
		req2.Header.Set("User-Agent", c.config.UserAgent)
		resp2, err := c.httpClient.Do(req2)
		if err != nil {
			initErr = err
			return
		}
		defer resp2.Body.Close()
		body, err := io.ReadAll(resp2.Body)
		if err != nil {
			initErr = err
			return
		}
		c.crumb = string(body)
	})
	return initErr
}

// ChartResponse represents the Yahoo Finance chart API response.
type ChartResponse struct {
	Chart struct {
		Result []ChartResult `json:"result"`
		Error  *ChartError   `json:"error"`
	} `json:"chart"`
}

// ChartResult contains the data for a single stock.
type ChartResult struct {
	Meta       ChartMeta       `json:"meta"`
	Timestamp  []int64         `json:"timestamp"`
	Indicators ChartIndicators `json:"indicators"`
}

// ChartMeta contains metadata about the stock.
type ChartMeta struct {
	Symbol                     string  `json:"symbol"`
	Currency                   string  `json:"currency"`
	ExchangeName               string  `json:"exchangeName"`
	RegularMarketPrice         float64 `json:"regularMarketPrice"`
	RegularMarketPreviousClose float64 `json:"regularMarketPreviousClose"`
	ChartPreviousClose         float64 `json:"chartPreviousClose"`
}

// ChartIndicators contains the quote data.
type ChartIndicators struct {
	Quote []QuoteData `json:"quote"`
}

// QuoteData contains OHLCV data.
type QuoteData struct {
	Open   []float64 `json:"open"`
	High   []float64 `json:"high"`
	Low    []float64 `json:"low"`
	Close  []float64 `json:"close"`
	Volume []int64   `json:"volume"`
}

// ChartError represents an error from the Yahoo Finance API.
type ChartError struct {
	Code        string `json:"code"`
	Description string `json:"description"`
}

// StockData contains the processed data for a stock.
type StockData struct {
	Symbol        string
	Currency      string
	Exchange      string
	Closes        []float64
	Timestamps    []time.Time
	CurrentPrice  float64
	PreviousClose float64
}

// GetPreviousDayClose fetches the last two trading days' close prices (J-1 and J-2).
func (c *Client) GetPreviousDayClose(ctx context.Context, ticker string) (j1, j2 float64, err error) {
	url := fmt.Sprintf("%s/%s?range=5d&interval=1d", c.config.BaseURL, ticker)

	req, err2 := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err2 != nil {
		return 0, 0, fmt.Errorf("creating request: %w", err2)
	}
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err2 := c.httpClient.Do(req)
	if err2 != nil {
		return 0, 0, fmt.Errorf("executing request: %w", err2)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, 0, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err2 := io.ReadAll(resp.Body)
	if err2 != nil {
		return 0, 0, fmt.Errorf("reading response: %w", err2)
	}

	var chartResp ChartResponse
	if err2 := json.Unmarshal(body, &chartResp); err2 != nil {
		return 0, 0, fmt.Errorf("parsing response: %w", err2)
	}

	if chartResp.Chart.Error != nil {
		return 0, 0, fmt.Errorf("API error: %s - %s", chartResp.Chart.Error.Code, chartResp.Chart.Error.Description)
	}

	if len(chartResp.Chart.Result) == 0 {
		return 0, 0, fmt.Errorf("no data returned for %s", ticker)
	}

	closes := chartResp.Chart.Result[0].Indicators.Quote[0].Close
	if len(closes) < 3 {
		return 0, 0, fmt.Errorf("insufficient daily data for %s", ticker)
	}

	return closes[len(closes)-2], closes[len(closes)-3], nil
}

// GetChartData fetches chart data for a ticker symbol.
func (c *Client) GetChartData(ctx context.Context, ticker string) (*StockData, error) {
	url := fmt.Sprintf("%s/%s?range=%s&interval=%s",
		c.config.BaseURL,
		ticker,
		c.config.Range,
		c.config.Interval,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var chartResp ChartResponse
	if err := json.Unmarshal(body, &chartResp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if chartResp.Chart.Error != nil {
		return nil, fmt.Errorf("API error: %s - %s",
			chartResp.Chart.Error.Code,
			chartResp.Chart.Error.Description,
		)
	}

	if len(chartResp.Chart.Result) == 0 {
		return nil, fmt.Errorf("no data returned for %s", ticker)
	}

	result := chartResp.Chart.Result[0]

	var closes []float64
	if len(result.Indicators.Quote) > 0 {
		closes = result.Indicators.Quote[0].Close
	}

	var timestamps []time.Time
	for _, ts := range result.Timestamp {
		timestamps = append(timestamps, time.Unix(ts, 0))
	}

	return &StockData{
		Symbol:        result.Meta.Symbol,
		Currency:      result.Meta.Currency,
		Exchange:      result.Meta.ExchangeName,
		Closes:        closes,
		Timestamps:    timestamps,
		CurrentPrice:  result.Meta.RegularMarketPrice,
		PreviousClose: result.Meta.RegularMarketPreviousClose,
	}, nil
}
