package yahoo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// DailyBar preserves missing prices as zero so callers cannot bridge a known gap.
// Yahoo chart OHLC prices are split-adjusted, without reinvested dividends.
type DailyBar struct {
	Date        time.Time
	Open, Close float64
}

// GetDailyHistory fetches daily prices independently of the technical-analysis interval.
// The bounds are inclusive start, exclusive end, as in the Yahoo chart endpoint.
func (c *Client) GetDailyHistory(ctx context.Context, ticker string, start, end time.Time) ([]DailyBar, error) {
	if !start.Before(end) {
		return nil, fmt.Errorf("history %s: invalid bounds", ticker)
	}
	params := url.Values{
		"period1": {fmt.Sprint(start.Unix())}, "period2": {fmt.Sprint(end.Unix())},
		"interval": {"1d"}, "includePrePost": {"false"},
	}
	u := fmt.Sprintf("%s/%s?%s", c.config.BaseURL, url.PathEscape(ticker), params.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("history %s: HTTP %d", ticker, resp.StatusCode)
	}
	var payload ChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("history %s: %w", ticker, err)
	}
	if payload.Chart.Error != nil || len(payload.Chart.Result) != 1 {
		return nil, fmt.Errorf("history %s unavailable", ticker)
	}
	r := payload.Chart.Result[0]
	if r.Meta.Symbol != ticker || len(r.Timestamp) == 0 || len(r.Indicators.Quote) != 1 {
		return nil, fmt.Errorf("history %s: missing or mismatched data", ticker)
	}
	q := r.Indicators.Quote[0]
	if len(q.Open) != len(r.Timestamp) || len(q.Close) != len(r.Timestamp) {
		return nil, fmt.Errorf("history %s: misaligned prices", ticker)
	}
	bars := make([]DailyBar, 0, len(r.Timestamp))
	for i, ts := range r.Timestamp {
		if ts <= 0 || (i > 0 && ts <= r.Timestamp[i-1]) {
			return nil, fmt.Errorf("history %s: invalid timestamp order", ticker)
		}
		date := time.Unix(ts, 0)
		if date.Before(start) || !date.Before(end) {
			continue
		}
		bars = append(bars, DailyBar{Date: date, Open: q.Open[i], Close: q.Close[i]})
	}
	return bars, nil
}
