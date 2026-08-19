package technicalalerts

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

type Kind string

const (
	EMAProximity Kind = "ema_proximity"
	EMAReclaim   Kind = "ema_reclaim"
	EMALoss      Kind = "ema_loss"
	RSIRegimeUp  Kind = "rsi_regime_up"
	RSIRegimeDn  Kind = "rsi_regime_down"
	Breakout     Kind = "breakout"
	Breakdown    Kind = "breakdown"
	MACDBullish  Kind = "macd_bullish_cross"
	MACDBearish  Kind = "macd_bearish_cross"
)

func ParseTimeframe(value string) (Timeframe, error) {
	switch Timeframe(value) {
	case "", Daily:
		return Daily, nil
	case Weekly:
		return Weekly, nil
	default:
		return "", fmt.Errorf("unsupported technical alert timeframe %q", value)
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
		return ".technical-weekly-state.json"
	}
	return ".technical-state.json"
}

func (tf Timeframe) yahooRangeInterval() (string, string) {
	if tf == Weekly {
		return "5y", "1wk"
	}
	return "2y", "1d"
}

func (tf Timeframe) BreakoutLookback() int {
	if tf == Weekly {
		return 10
	}
	return 20
}

type Alert struct {
	Stock           models.Stock
	Timeframe       Timeframe
	Kind            Kind
	Label           string
	Bias            string
	Period          int
	CandleTime      int64
	LastClose       float64
	PreviousClose   float64
	ChangePercent   float64
	LastLow         float64
	LastHigh        float64
	Level           float64
	DistancePercent float64
	PreviousValue   float64
	CurrentValue    float64
	Detail          string
}

type ChangeSummary struct {
	DailyChange     float64
	HasDailyChange  bool
	WeeklyChange    float64
	HasWeeklyChange bool
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

func (c *Client) Check(ctx context.Context, stock models.Stock, timeframe Timeframe, emaThresholdPercent float64) ([]Alert, error) {
	candles, err := c.fetchCandles(ctx, stock.Ticker, timeframe)
	if err != nil {
		return nil, err
	}
	return CheckCandles(stock, timeframe, candles, emaThresholdPercent), nil
}

func (c *Client) DailyAndWeeklyChanges(ctx context.Context, ticker string) (ChangeSummary, error) {
	candles, err := c.fetchCandles(ctx, ticker, Daily)
	if err != nil {
		return ChangeSummary{}, err
	}
	return CalcDailyAndWeeklyChanges(candles), nil
}

func CalcDailyAndWeeklyChanges(candles []chartcalc.Candle) ChangeSummary {
	if len(candles) < 2 {
		return ChangeSummary{}
	}
	last := candles[len(candles)-1]
	prev := candles[len(candles)-2]
	out := ChangeSummary{}
	if prev.Close != 0 {
		out.DailyChange = ((last.Close - prev.Close) / prev.Close) * 100
		out.HasDailyChange = true
	}
	if len(candles) >= 6 {
		weekAgo := candles[len(candles)-6]
		if weekAgo.Close != 0 {
			out.WeeklyChange = ((last.Close - weekAgo.Close) / weekAgo.Close) * 100
			out.HasWeeklyChange = true
		}
	}
	return out
}

func CheckCandles(stock models.Stock, timeframe Timeframe, candles []chartcalc.Candle, emaThresholdPercent float64) []Alert {
	if len(candles) < 2 {
		return nil
	}
	last := candles[len(candles)-1]
	prev := candles[len(candles)-2]
	changePercent := 0.0
	if prev.Close != 0 {
		changePercent = ((last.Close - prev.Close) / prev.Close) * 100
	}
	var alerts []Alert

	emaLines := make(map[int][]float64)
	for _, period := range []int{50, 100, 200} {
		emaLines[period] = calcEMA(candles, period)
		latest := emaLines[period][len(candles)-1]
		previous := emaLines[period][len(candles)-2]
		if math.IsNaN(latest) {
			continue
		}

		distancePercent := ((last.Close - latest) / latest) * 100
		touched := last.Low <= latest && last.High >= latest
		near := math.Abs(distancePercent) <= emaThresholdPercent

		hasCross := false
		if math.IsNaN(previous) {
			hasCross = false
		} else if prev.Close <= previous && last.Close > latest {
			hasCross = true
			alerts = append(alerts, emaCrossAlert(stock, timeframe, last, prev.Close, changePercent, period, EMAReclaim, "bullish", latest, distancePercent, previous))
		} else if prev.Close >= previous && last.Close < latest {
			hasCross = true
			alerts = append(alerts, emaCrossAlert(stock, timeframe, last, prev.Close, changePercent, period, EMALoss, "bearish", latest, distancePercent, previous))
		}

		if !hasCross && (touched || near) {
			status := "near"
			if touched {
				status = "touch"
			}
			alerts = append(alerts, Alert{
				Stock: stock, Timeframe: timeframe, Kind: EMAProximity, Label: fmt.Sprintf("EMA%d %s", period, status),
				Bias: "watch", Period: period, CandleTime: last.Time, LastClose: last.Close, PreviousClose: prev.Close,
				ChangePercent: changePercent, LastLow: last.Low, LastHigh: last.High, Level: latest, DistancePercent: distancePercent,
				Detail: fmt.Sprintf("Close is %+.2f%% from EMA%d.", distancePercent, period),
			})
		}
	}

	alerts = append(alerts, rsiRegimeAlerts(stock, timeframe, candles)...)
	alerts = append(alerts, breakoutAlerts(stock, timeframe, candles)...)
	alerts = append(alerts, macdCrossAlerts(stock, timeframe, candles, emaLines[50])...)
	return alerts
}

func emaCrossAlert(stock models.Stock, timeframe Timeframe, last chartcalc.Candle, previousClose float64, changePercent float64, period int, kind Kind, bias string, level float64, distancePercent float64, previousValue float64) Alert {
	action := "reclaimed"
	labelKind := "reclaim"
	if kind == EMALoss {
		action = "lost"
		labelKind = "loss"
	}
	return Alert{
		Stock: stock, Timeframe: timeframe, Kind: kind, Label: fmt.Sprintf("EMA%d %s", period, labelKind),
		Bias: bias, Period: period, CandleTime: last.Time, LastClose: last.Close, PreviousClose: previousClose,
		ChangePercent: changePercent, Level: level,
		DistancePercent: distancePercent, PreviousValue: previousValue, CurrentValue: level,
		Detail: fmt.Sprintf("Close %s EMA%d after previously closing on the other side.", action, period),
	}
}

func Key(alert Alert) string {
	return fmt.Sprintf("%s:%s:%s:%d:%d", alert.Timeframe.String(), alert.Stock.Ticker, alert.Kind, alert.Period, alert.CandleTime)
}

func rsiRegimeAlerts(stock models.Stock, timeframe Timeframe, candles []chartcalc.Candle) []Alert {
	rsi := chartcalc.CalcRSI14(candles)
	if len(rsi) < 2 {
		return nil
	}
	prev := rsi[len(rsi)-2]
	last := rsi[len(rsi)-1]
	candle := candles[len(candles)-1]
	previousClose := candles[len(candles)-2].Close
	changePercent := 0.0
	if previousClose != 0 {
		changePercent = ((candle.Close - previousClose) / previousClose) * 100
	}
	if prev.Value < 50 && last.Value >= 50 {
		return []Alert{{
			Stock: stock, Timeframe: timeframe, Kind: RSIRegimeUp, Label: "RSI reclaimed 50",
			Bias: "bullish", CandleTime: candle.Time, LastClose: candle.Close, PreviousClose: previousClose,
			ChangePercent: changePercent, PreviousValue: prev.Value,
			CurrentValue: last.Value, Level: 50, Detail: "RSI moved back above the 50 momentum line.",
		}}
	}
	if prev.Value > 50 && last.Value <= 50 {
		return []Alert{{
			Stock: stock, Timeframe: timeframe, Kind: RSIRegimeDn, Label: "RSI lost 50",
			Bias: "bearish", CandleTime: candle.Time, LastClose: candle.Close, PreviousClose: previousClose,
			ChangePercent: changePercent, PreviousValue: prev.Value,
			CurrentValue: last.Value, Level: 50, Detail: "RSI moved below the 50 momentum line.",
		}}
	}
	return nil
}

func breakoutAlerts(stock models.Stock, timeframe Timeframe, candles []chartcalc.Candle) []Alert {
	lookback := timeframe.BreakoutLookback()
	if len(candles) < lookback+1 {
		return nil
	}
	last := candles[len(candles)-1]
	prev := candles[len(candles)-2]
	changePercent := 0.0
	if prev.Close != 0 {
		changePercent = ((last.Close - prev.Close) / prev.Close) * 100
	}
	window := candles[len(candles)-1-lookback : len(candles)-1]
	high := window[0].High
	low := window[0].Low
	for _, c := range window[1:] {
		high = math.Max(high, c.High)
		low = math.Min(low, c.Low)
	}
	if last.Close > high {
		return []Alert{{
			Stock: stock, Timeframe: timeframe, Kind: Breakout, Label: fmt.Sprintf("%d-candle breakout", lookback),
			Bias: "bullish", Period: lookback, CandleTime: last.Time, LastClose: last.Close, PreviousClose: prev.Close,
			ChangePercent: changePercent, Level: high,
			DistancePercent: ((last.Close - high) / high) * 100, Detail: "Close broke above the recent high range.",
		}}
	}
	if last.Close < low {
		return []Alert{{
			Stock: stock, Timeframe: timeframe, Kind: Breakdown, Label: fmt.Sprintf("%d-candle breakdown", lookback),
			Bias: "bearish", Period: lookback, CandleTime: last.Time, LastClose: last.Close, PreviousClose: prev.Close,
			ChangePercent: changePercent, Level: low,
			DistancePercent: ((last.Close - low) / low) * 100, Detail: "Close broke below the recent low range.",
		}}
	}
	return nil
}

func macdCrossAlerts(stock models.Stock, timeframe Timeframe, candles []chartcalc.Candle, ema50 []float64) []Alert {
	macd := calcMACD(candles)
	if len(macd) < 2 || len(ema50) != len(candles) {
		return nil
	}
	prev := macd[len(macd)-2]
	last := macd[len(macd)-1]
	candle := candles[len(candles)-1]
	prevCandle := candles[len(candles)-2]
	changePercent := 0.0
	if prevCandle.Close != 0 {
		changePercent = ((candle.Close - prevCandle.Close) / prevCandle.Close) * 100
	}
	lastEMA50 := ema50[len(ema50)-1]
	if math.IsNaN(lastEMA50) {
		return nil
	}
	if prev.MACD <= prev.Signal && last.MACD > last.Signal && candle.Close >= lastEMA50 {
		return []Alert{{
			Stock: stock, Timeframe: timeframe, Kind: MACDBullish, Label: "MACD bullish cross",
			Bias: "bullish", CandleTime: candle.Time, LastClose: candle.Close, PreviousClose: prevCandle.Close,
			ChangePercent: changePercent, Level: lastEMA50,
			PreviousValue: prev.MACD - prev.Signal, CurrentValue: last.MACD - last.Signal,
			Detail: "MACD crossed above signal while price is above EMA50.",
		}}
	}
	if prev.MACD >= prev.Signal && last.MACD < last.Signal && candle.Close <= lastEMA50 {
		return []Alert{{
			Stock: stock, Timeframe: timeframe, Kind: MACDBearish, Label: "MACD bearish cross",
			Bias: "bearish", CandleTime: candle.Time, LastClose: candle.Close, PreviousClose: prevCandle.Close,
			ChangePercent: changePercent, Level: lastEMA50,
			PreviousValue: prev.MACD - prev.Signal, CurrentValue: last.MACD - last.Signal,
			Detail: "MACD crossed below signal while price is below EMA50.",
		}}
	}
	return nil
}

func calcEMA(candles []chartcalc.Candle, period int) []float64 {
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

type macdPoint struct {
	MACD   float64
	Signal float64
}

func calcMACD(candles []chartcalc.Candle) []macdPoint {
	const fast = 12
	const slow = 26
	const signalPeriod = 9
	fastEMA := calcEMA(candles, fast)
	slowEMA := calcEMA(candles, slow)
	type basePoint struct {
		value float64
	}
	var base []basePoint
	for i := range candles {
		if math.IsNaN(fastEMA[i]) || math.IsNaN(slowEMA[i]) {
			continue
		}
		base = append(base, basePoint{value: fastEMA[i] - slowEMA[i]})
	}
	if len(base) < signalPeriod {
		return nil
	}
	var sum float64
	for i := 0; i < signalPeriod; i++ {
		sum += base[i].value
	}
	signal := sum / signalPeriod
	points := make([]macdPoint, 0, len(base)-signalPeriod+1)
	for i := signalPeriod - 1; i < len(base); i++ {
		if i > signalPeriod-1 {
			k := 2.0 / float64(signalPeriod+1)
			signal = base[i].value*k + signal*(1-k)
		}
		points = append(points, macdPoint{MACD: base[i].value, Signal: signal})
	}
	return points
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
