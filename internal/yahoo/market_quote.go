package yahoo

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"
)

// MarketQuote keeps the source timestamp and previous close, without an open-price fallback.
type MarketQuote struct {
	Price         float64
	PreviousClose float64
	AsOf          time.Time
}

func (q MarketQuote) ChangePercent() float64 {
	return (q.Price/q.PreviousClose - 1) * 100
}

func (c *Client) GetMarketQuote(ctx context.Context, ticker string) (MarketQuote, error) {
	u := fmt.Sprintf("%s/%s?range=1d&interval=5m", c.config.BaseURL, url.PathEscape(ticker))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return MarketQuote{}, err
	}
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return MarketQuote{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return MarketQuote{}, fmt.Errorf("market quote %s: HTTP %d", ticker, resp.StatusCode)
	}
	var payload struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol        string  `json:"symbol"`
					Price         float64 `json:"regularMarketPrice"`
					PreviousClose float64 `json:"chartPreviousClose"`
					Timestamp     int64   `json:"regularMarketTime"`
				} `json:"meta"`
			} `json:"result"`
			Error *ChartError `json:"error"`
		} `json:"chart"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return MarketQuote{}, fmt.Errorf("market quote %s: %w", ticker, err)
	}
	if payload.Chart.Error != nil || len(payload.Chart.Result) != 1 {
		return MarketQuote{}, fmt.Errorf("market quote %s unavailable", ticker)
	}
	m := payload.Chart.Result[0].Meta
	if m.Symbol != ticker || m.Timestamp <= 0 || !positiveFinite(m.Price) || !positiveFinite(m.PreviousClose) {
		return MarketQuote{}, fmt.Errorf("market quote %s: invalid price, reference or timestamp", ticker)
	}
	return MarketQuote{Price: m.Price, PreviousClose: m.PreviousClose, AsOf: time.Unix(m.Timestamp, 0)}, nil
}

func positiveFinite(v float64) bool {
	return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0)
}
