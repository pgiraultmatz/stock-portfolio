package emaalerts

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/config"
	"stock-portfolio/internal/models"
)

type Timeframe string

const (
	Daily  Timeframe = "daily"
	Weekly Timeframe = "weekly"
)

func ParseTimeframe(value string) (Timeframe, error) {
	switch Timeframe(value) {
	case "", Daily:
		return Daily, nil
	case Weekly:
		return Weekly, nil
	default:
		return "", fmt.Errorf("unsupported EMA timeframe %q", value)
	}
}

func (tf Timeframe) String() string {
	if tf == "" {
		return string(Daily)
	}
	return string(tf)
}

func (tf Timeframe) DefaultStatePath() string {
	if tf == Weekly {
		return ".ema-weekly-state.json"
	}
	return ".ema-state.json"
}

func (tf Timeframe) yahooRangeInterval() (string, string) {
	if tf == Weekly {
		return "5y", "1wk"
	}
	return "2y", "1d"
}

type Alert struct {
	Stock           models.Stock
	Timeframe       Timeframe
	Period          int
	CandleTime      int64
	LastClose       float64
	LastLow         float64
	LastHigh        float64
	EMA             float64
	DistancePercent float64
	Touched         bool
}

type Client struct {
	baseURL    string
	userAgent  string
	httpClient *http.Client
}

func NewClient(cfg config.YahooAPIConfig) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = "https://query1.finance.yahoo.com/v8/finance/chart"
	}
	userAgent := cfg.UserAgent
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
	}
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL:    baseURL,
		userAgent:  userAgent,
		httpClient: &http.Client{Timeout: timeout},
	}
}

func (c *Client) Check(ctx context.Context, stock models.Stock, timeframe Timeframe, thresholdPercent float64) ([]Alert, error) {
	candles, err := c.fetchCandles(ctx, stock.Ticker, timeframe)
	if err != nil {
		return nil, err
	}
	if len(candles) == 0 {
		return nil, nil
	}
	last := candles[len(candles)-1]
	var triggered []Alert
	for _, period := range []int{50, 100, 200} {
		ema, ok := latestEMA(candles, period)
		if !ok || ema <= 0 {
			continue
		}
		distancePercent := ((last.Close - ema) / ema) * 100
		touched := last.Low <= ema && last.High >= ema
		near := math.Abs(distancePercent) <= thresholdPercent
		if !touched && !near {
			continue
		}
		triggered = append(triggered, Alert{
			Stock:           stock,
			Timeframe:       timeframe,
			Period:          period,
			CandleTime:      last.Time,
			LastClose:       last.Close,
			LastLow:         last.Low,
			LastHigh:        last.High,
			EMA:             ema,
			DistancePercent: distancePercent,
			Touched:         touched,
		})
	}
	return triggered, nil
}

func Key(alert Alert) string {
	return fmt.Sprintf("%s:%s:ema%d:%d", alert.Timeframe.String(), alert.Stock.Ticker, alert.Period, alert.CandleTime)
}

func latestEMA(candles []chartcalc.Candle, period int) (float64, bool) {
	if period <= 0 || len(candles) < period {
		return 0, false
	}
	var sum float64
	for i := 0; i < period; i++ {
		sum += candles[i].Close
	}
	ema := sum / float64(period)
	k := 2.0 / float64(period+1)
	for i := period; i < len(candles); i++ {
		ema = candles[i].Close*k + ema*(1-k)
	}
	return ema, true
}

func (c *Client) fetchCandles(ctx context.Context, ticker string, timeframe Timeframe) ([]chartcalc.Candle, error) {
	u := c.chartURL(ticker, timeframe)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("yahoo chart status %d", resp.StatusCode)
	}

	var payload yahooChartResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	if len(payload.Chart.Result) == 0 {
		return nil, fmt.Errorf("no chart data")
	}
	result := payload.Chart.Result[0]
	if len(result.Indicators.Quote) == 0 {
		return nil, fmt.Errorf("no quote data")
	}
	quote := result.Indicators.Quote[0]
	n := minInt(len(result.Timestamp), len(quote.Close))
	candles := make([]chartcalc.Candle, 0, n)
	for i := 0; i < n; i++ {
		if quote.Open[i] == nil || quote.High[i] == nil || quote.Low[i] == nil || quote.Close[i] == nil {
			continue
		}
		var volume int64
		if i < len(quote.Volume) && quote.Volume[i] != nil {
			volume = int64(*quote.Volume[i])
		}
		candles = append(candles, chartcalc.Candle{
			Time:   result.Timestamp[i],
			Open:   *quote.Open[i],
			High:   *quote.High[i],
			Low:    *quote.Low[i],
			Close:  *quote.Close[i],
			Volume: volume,
		})
	}
	return candles, nil
}

func (c *Client) chartURL(ticker string, timeframe Timeframe) string {
	r, interval := timeframe.yahooRangeInterval()
	return fmt.Sprintf("%s/%s?range=%s&interval=%s", c.baseURL, url.PathEscape(ticker), url.QueryEscape(r), url.QueryEscape(interval))
}

type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*float64 `json:"volume"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
	} `json:"chart"`
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
