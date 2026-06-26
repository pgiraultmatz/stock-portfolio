package divergencealerts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/config"
	"stock-portfolio/internal/models"
)

type Alert struct {
	Stock      models.Stock
	Divergence chartcalc.Divergence
	LastClose  float64
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

func (c *Client) Check(ctx context.Context, stock models.Stock) ([]Alert, error) {
	candles, err := c.fetchCandles(ctx, stock.Ticker)
	if err != nil {
		return nil, err
	}
	if len(candles) < 40 {
		return nil, nil
	}

	rsi := chartcalc.CalcRSI14(candles)
	pivots := chartcalc.CalcPivots(candles, rsi)
	levels := calcLevels(pivots)
	ma50 := calcSMA(candles, 50)
	ma100 := calcSMA(candles, 100)
	ma200 := calcSMA(candles, 200)
	divs := chartcalc.CalcRSIDivergences(candles, pivots)
	divs = chartcalc.FilterDivergencesByContext(divs, candles, ma50, ma100, ma200, levels)

	last := candles[len(candles)-1]
	alerts := make([]Alert, 0, len(divs))
	for _, div := range divs {
		if !divergenceOnLatestCandle(div, last.Time) || !divergenceStillActionable(div, last.Close) {
			continue
		}
		alerts = append(alerts, Alert{
			Stock:      stock,
			Divergence: div,
			LastClose:  last.Close,
		})
	}
	return alerts, nil
}

func Key(ticker string, div chartcalc.Divergence) string {
	return fmt.Sprintf("%s:%s:%d:%d", ticker, div.Kind, div.FromTime, div.ToTime)
}

func (c *Client) fetchCandles(ctx context.Context, ticker string) ([]chartcalc.Candle, error) {
	u := fmt.Sprintf("%s/%s?range=1y&interval=1d", c.baseURL, url.PathEscape(ticker))
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

func divergenceOnLatestCandle(div chartcalc.Divergence, lastTime int64) bool {
	return div.ToTime > 0 && lastTime > 0 && div.ToTime == lastTime
}

func divergenceStillActionable(div chartcalc.Divergence, lastClose float64) bool {
	if lastClose <= 0 || div.ToPrice <= 0 {
		return true
	}
	switch div.Kind {
	case "bearish":
		return lastClose <= div.ToPrice*1.01
	case "bullish":
		return lastClose >= div.ToPrice*0.99
	default:
		return true
	}
}

func calcSMA(candles []chartcalc.Candle, period int) []chartcalc.LinePoint {
	if period <= 0 || len(candles) < period {
		return nil
	}
	points := make([]chartcalc.LinePoint, 0, len(candles)-period+1)
	var sum float64
	for i, c := range candles {
		sum += c.Close
		if i >= period {
			sum -= candles[i-period].Close
		}
		if i >= period-1 {
			points = append(points, chartcalc.LinePoint{Time: c.Time, Value: sum / float64(period)})
		}
	}
	return points
}

func calcLevels(pivots []chartcalc.Pivot) []chartcalc.Level {
	if len(pivots) == 0 {
		return nil
	}
	var levels []chartcalc.Level
	for _, kind := range []string{"support", "resistance"} {
		pivotKind := "low"
		if kind == "resistance" {
			pivotKind = "high"
		}
		var relevant []chartcalc.Pivot
		for _, p := range pivots {
			if p.Kind == pivotKind {
				relevant = append(relevant, p)
			}
		}
		if len(relevant) == 0 {
			continue
		}
		sort.Slice(relevant, func(i, j int) bool { return relevant[i].Price < relevant[j].Price })
		tolerance := levelTolerance(relevant)
		for _, p := range relevant {
			merged := false
			for i := range levels {
				if levels[i].Kind == kind && abs(levels[i].Price-p.Price) <= tolerance {
					n := float64(levels[i].Strength)
					levels[i].Price = (levels[i].Price*n + p.Price) / (n + 1)
					levels[i].Strength++
					levels[i].Touches = append(levels[i].Touches, p.Time)
					merged = true
					break
				}
			}
			if !merged {
				levels = append(levels, chartcalc.Level{Kind: kind, Price: p.Price, Strength: 1, Touches: []int64{p.Time}})
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

func levelTolerance(pivots []chartcalc.Pivot) float64 {
	minP, maxP := pivots[0].Price, pivots[0].Price
	for _, p := range pivots[1:] {
		if p.Price < minP {
			minP = p.Price
		}
		if p.Price > maxP {
			maxP = p.Price
		}
	}
	return maxFloat((maxP-minP)*0.012, maxP*0.004)
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
