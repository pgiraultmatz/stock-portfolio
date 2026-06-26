package yahoo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// IntradayPrice holds today's open price and current price for a ticker.
type IntradayPrice struct {
	Ticker        string
	OpenPrice     float64
	CurrentPrice  float64
	ChangePercent float64
	// Stale is true when Yahoo returned data from a previous session (market closed/not yet opened).
	// Alerts should be skipped for stale data to avoid re-firing on yesterday's moves.
	Stale bool
}

// GetIntradayPrice fetches today's opening price and current price for a ticker.
// It uses range=1d&interval=1d which returns a single OHLC bar for the current session.
func (c *Client) GetIntradayPrice(ctx context.Context, ticker string) (*IntradayPrice, error) {
	url := fmt.Sprintf("%s/%s?range=1d&interval=5m", c.config.BaseURL, ticker)

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
		return nil, fmt.Errorf("API error: %s - %s", chartResp.Chart.Error.Code, chartResp.Chart.Error.Description)
	}
	if len(chartResp.Chart.Result) == 0 {
		return nil, fmt.Errorf("no data returned for %s", ticker)
	}

	result := chartResp.Chart.Result[0]
	currentPrice := result.Meta.RegularMarketPrice

	if len(result.Indicators.Quote) == 0 || len(result.Indicators.Quote[0].Open) == 0 {
		return nil, fmt.Errorf("no intraday quote data for %s", ticker)
	}
	openPrice := result.Indicators.Quote[0].Open[0]

	if openPrice == 0 {
		return nil, fmt.Errorf("open price is zero for %s", ticker)
	}

	// Check if the bar is from today — if not, the market hasn't opened yet
	// and the data is from the previous session. Alerts should be skipped.
	stale := true
	if len(result.Timestamp) > 0 {
		barTime := time.Unix(result.Timestamp[0], 0)
		today := time.Now()
		stale = barTime.Year() != today.Year() ||
			barTime.Month() != today.Month() ||
			barTime.Day() != today.Day()
	}

	// Use previous close as the reference so that gap-up/gap-down moves are captured.
	// Fall back to today's open if the previous close is unavailable.
	referencePrice := result.Meta.ChartPreviousClose
	if referencePrice == 0 {
		referencePrice = openPrice
	}

	changePercent := (currentPrice - referencePrice) / referencePrice * 100

	return &IntradayPrice{
		Ticker:        ticker,
		OpenPrice:     referencePrice,
		CurrentPrice:  currentPrice,
		ChangePercent: changePercent,
		Stale:         stale,
	}, nil
}
