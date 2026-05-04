package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// ---- structs ----------------------------------------------------------------

type StockMetrics struct {
	RSI           float64 `json:"rsi"`
	RSILabel      string  `json:"rsiLabel"`
	RSIClass      string  `json:"rsiClass"`
	TargetPrice   string  `json:"targetPrice"`
	TargetPct     float64 `json:"targetPct"`
	Currency      string  `json:"currency"`
	PEGRatio      float64 `json:"pegRatio"`
	PEGLabel      string  `json:"pegLabel"`
	PEGClass      string  `json:"pegClass"`
	PSGRatio      float64 `json:"psgRatio"`
	PSGLabel      string  `json:"psgLabel"`
	PSGClass      string  `json:"psgClass"`
	EVGPRatio     float64 `json:"evgpRatio"`
	EVGPLabel     string  `json:"evgpLabel"`
	EVGPClass     string  `json:"evgpClass"`
	Earnings      string  `json:"earnings"`
	EarningsClass string  `json:"earningsClass"`
	Signal        string  `json:"signal"`
	SignalClass   string  `json:"signalClass"`
	SignalNote    string  `json:"signalNote"`
	Error         string  `json:"error,omitempty"`
}

// ---- Yahoo Finance client ---------------------------------------------------

const yahooUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
const yahooBaseURL   = "https://query1.finance.yahoo.com/v8/finance/chart"

type yahooClient struct {
	http      *http.Client
	crumb     string
	crumbOnce sync.Once
}

func newYahooClient() *yahooClient {
	jar, _ := cookiejar.New(nil)
	return &yahooClient{
		http: &http.Client{Timeout: 30 * time.Second, Jar: jar},
	}
}

func (c *yahooClient) initCrumb(ctx context.Context) error {
	var initErr error
	c.crumbOnce.Do(func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://fc.yahoo.com", nil)
		if err != nil { initErr = err; return }
		req.Header.Set("User-Agent", yahooUserAgent)
		resp, err := c.http.Do(req)
		if err != nil { initErr = err; return }
		resp.Body.Close()

		req2, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://query2.finance.yahoo.com/v1/test/getcrumb", nil)
		if err != nil { initErr = err; return }
		req2.Header.Set("User-Agent", yahooUserAgent)
		resp2, err := c.http.Do(req2)
		if err != nil { initErr = err; return }
		defer resp2.Body.Close()
		body, err := io.ReadAll(resp2.Body)
		if err != nil { initErr = err; return }
		c.crumb = string(body)
	})
	return initErr
}

// ---- RSI --------------------------------------------------------------------

func calcRSI(closes []float64) float64 {
	valid := make([]float64, 0, len(closes))
	for _, c := range closes {
		if !math.IsNaN(c) && c > 0 {
			valid = append(valid, c)
		}
	}
	const period = 14
	if len(valid) < period+1 {
		return 0
	}
	gains := make([]float64, len(valid)-1)
	losses := make([]float64, len(valid)-1)
	for i := 1; i < len(valid); i++ {
		d := valid[i] - valid[i-1]
		if d > 0 {
			gains[i-1] = d
		} else {
			losses[i-1] = math.Abs(d)
		}
	}
	var ag, al float64
	for i := 0; i < period; i++ {
		ag += gains[i]; al += losses[i]
	}
	ag /= period; al /= period
	for i := period; i < len(gains); i++ {
		ag = (ag*float64(period-1) + gains[i]) / period
		al = (al*float64(period-1) + losses[i]) / period
	}
	if al == 0 {
		if ag == 0 { return 50 }
		return 100
	}
	return 100 - (100 / (1 + ag/al))
}

// ---- Chart data fetch -------------------------------------------------------

type chartResp struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency           string  `json:"currency"`
				RegularMarketPrice float64 `json:"regularMarketPrice"`
			} `json:"meta"`
			Indicators struct {
				Quote []struct {
					Close []float64 `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct{ Description string `json:"description"` } `json:"error"`
	} `json:"chart"`
}

func (c *yahooClient) fetchCloses(ctx context.Context, ticker string) (closes []float64, price float64, currency string, err error) {
	url := fmt.Sprintf("%s/%s?range=1y&interval=1wk", yahooBaseURL, ticker)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", yahooUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil { return }
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var cr chartResp
	if err = json.Unmarshal(body, &cr); err != nil { return }
	if cr.Chart.Error != nil { err = fmt.Errorf("%s", cr.Chart.Error.Description); return }
	if len(cr.Chart.Result) == 0 { err = fmt.Errorf("no data"); return }
	r := cr.Chart.Result[0]
	price = r.Meta.RegularMarketPrice
	currency = r.Meta.Currency
	if len(r.Indicators.Quote) > 0 {
		closes = r.Indicators.Quote[0].Close
	}
	return
}

// ---- Valuation data ---------------------------------------------------------

type valuationData struct {
	targetPrice   float64
	pegRatio      float64
	psgRatio      float64
	evGrossProfit float64
}

type quoteSummaryResp struct {
	QuoteSummary struct {
		Result []struct {
			FinancialData struct {
				TargetMeanPrice struct{ Raw float64 `json:"raw"` } `json:"targetMeanPrice"`
				GrossProfits    struct{ Raw float64 `json:"raw"` } `json:"grossProfits"`
			} `json:"financialData"`
			DefaultKeyStatistics struct {
				EnterpriseValue struct{ Raw float64 `json:"raw"` } `json:"enterpriseValue"`
			} `json:"defaultKeyStatistics"`
			EarningsTrend struct {
				Trend []struct {
					Period          string `json:"period"`
					RevenueEstimate struct {
						Avg    struct{ Raw float64 `json:"raw"` } `json:"avg"`
						Growth struct{ Raw float64 `json:"raw"` } `json:"growth"`
					} `json:"revenueEstimate"`
				} `json:"trend"`
			} `json:"earningsTrend"`
		} `json:"result"`
		Error *struct{ Code string `json:"code"` } `json:"error"`
	} `json:"quoteSummary"`
}

type pegTSResp struct {
	Timeseries struct {
		Result []struct {
			TrailingPegRatio []struct {
				ReportedValue struct{ Raw float64 `json:"raw"` } `json:"reportedValue"`
			} `json:"trailingPegRatio"`
		} `json:"result"`
	} `json:"timeseries"`
}

func (c *yahooClient) fetchValuation(ctx context.Context, ticker string) (valuationData, error) {
	var vd valuationData
	if err := c.initCrumb(ctx); err != nil {
		return vd, err
	}

	url := fmt.Sprintf("https://query2.finance.yahoo.com/v10/finance/quoteSummary/%s?modules=financialData,defaultKeyStatistics,earningsTrend&crumb=%s", ticker, c.crumb)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", yahooUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil { return vd, err }
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		var q quoteSummaryResp
		if json.Unmarshal(body, &q) == nil && q.QuoteSummary.Error == nil && len(q.QuoteSummary.Result) > 0 {
			r := q.QuoteSummary.Result[0]
			vd.targetPrice = r.FinancialData.TargetMeanPrice.Raw
			ev := r.DefaultKeyStatistics.EnterpriseValue.Raw
			if gp := r.FinancialData.GrossProfits.Raw; ev > 0 && gp > 0 {
				vd.evGrossProfit = ev / gp
			}
			for _, t := range r.EarningsTrend.Trend {
				if t.Period == "0y" {
					fwdRev := t.RevenueEstimate.Avg.Raw
					fwdGrowth := t.RevenueEstimate.Growth.Raw
					if ev > 0 && fwdRev > 0 && fwdGrowth > 0 {
						vd.psgRatio = (ev / fwdRev) / (fwdGrowth * 100)
					}
					break
				}
			}
		}
	}

	now := time.Now()
	tsURL := fmt.Sprintf("https://query2.finance.yahoo.com/ws/fundamentals-timeseries/v1/finance/timeseries/%s?type=trailingPegRatio&period1=%d&period2=%d&crumb=%s",
		ticker, now.AddDate(0, -3, 0).Unix(), now.Unix(), c.crumb)
	req2, _ := http.NewRequestWithContext(ctx, http.MethodGet, tsURL, nil)
	req2.Header.Set("User-Agent", yahooUserAgent)
	req2.Header.Set("Accept", "application/json")
	resp2, err := c.http.Do(req2)
	if err != nil { return vd, nil }
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		body2, _ := io.ReadAll(resp2.Body)
		var ts pegTSResp
		if json.Unmarshal(body2, &ts) == nil && len(ts.Timeseries.Result) > 0 {
			entries := ts.Timeseries.Result[0].TrailingPegRatio
			if len(entries) > 0 {
				vd.pegRatio = entries[len(entries)-1].ReportedValue.Raw
			}
		}
	}
	return vd, nil
}

// ---- Earnings date ----------------------------------------------------------

var earningsRe = regexp.MustCompile(`earningsDate.{0,15}raw.{0,15}:(\d{10})`)

func (c *yahooClient) fetchEarnings(ctx context.Context, ticker string) (*time.Time, error) {
	url := fmt.Sprintf("https://finance.yahoo.com/quote/%s", ticker)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := c.http.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return nil, nil }
	body, _ := io.ReadAll(resp.Body)
	now := time.Now()
	for _, m := range earningsRe.FindAllSubmatch(body, -1) {
		ts, err := strconv.ParseInt(string(m[1]), 10, 64)
		if err != nil { continue }
		t := time.Unix(ts, 0)
		if t.After(now) { return &t, nil }
	}
	return nil, nil
}

// ---- Signal computation (ported from stock-checker) -----------------------

func computeSignal(rsi, peg, psg, evgp, targetPrice, currentPrice float64, earningsDate *time.Time) (signal, signalClass, signalNote string) {
	available := 0
	if peg > 0 { available++ }
	if psg > 0 { available++ }
	if evgp > 0 { available++ }
	if targetPrice > 0 { available++ }
	if available < 3 { return "", "", "" }

	score := 0

	switch {
	case rsi < 35: score++
	case rsi < 55: score++
	case rsi < 65:
	case rsi < 75: score--
	case rsi < 85: score -= 2
	default:       score -= 3
	}

	if peg > 0 {
		switch {
		case peg < 1.0: score += 2
		case peg < 1.7: score++
		case peg < 2.5:
		case peg < 4.0: score--
		default:        score -= 2
		}
	}

	if psg > 0 {
		switch {
		case psg < 0.15: score += 2
		case psg < 0.30: score++
		case psg < 0.45:
		case psg < 0.65: score--
		default:         score -= 2
		}
	}

	if evgp > 0 {
		switch {
		case evgp < 8:  score += 2
		case evgp < 15: score++
		case evgp < 25:
		case evgp < 35: score--
		default:        score -= 2
		}
	}

	var upside float64
	if targetPrice > 0 && currentPrice > 0 {
		upside = (targetPrice - currentPrice) / currentPrice * 100
		switch {
		case upside > 40: score += 2
		case upside > 20: score++
		case upside > 5:
		case upside > -10: score--
		default:           score -= 2
		}
	}

	daysToEarnings := -1
	if earningsDate != nil {
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		d := *earningsDate
		day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, now.Location())
		daysToEarnings = int(day.Sub(today).Hours() / 24)
		switch {
		case daysToEarnings <= 3: score -= 2
		case daysToEarnings <= 10: score--
		}
	}

	if rsi > 85 && daysToEarnings >= 0 && daysToEarnings < 7 && score > -4 { score = -4 }
	if rsi > 75 && psg > 0.65 && evgp > 25 && score > -2                   { score = -2 }
	if targetPrice > 0 && upside < 0 && peg > 2.5 && psg > 0.45 && score > -6 { score = -6 }
	if rsi < 55 && psg > 0 && psg < 0.15 && evgp > 0 && evgp < 8 && targetPrice > 0 && upside > 20 && score < 3 { score = 3 }
	if rsi > 75 && score > 5                                                 { score = 5 }
	if daysToEarnings >= 0 && daysToEarnings < 7 && score > 5               { score = 5 }
	if evgp > 50 && daysToEarnings >= 0 && daysToEarnings < 7 && score > 0  { score = 0 }

	switch {
	case score >= 6:  signal, signalClass = "STRONG BUY", "signal-strong-buy"
	case score >= 3:  signal, signalClass = "BUY", "signal-buy"
	case score >= 1:  signal, signalClass = "ACCUMULATE", "signal-accumulate"
	case score >= -1: signal, signalClass = "HOLD", "signal-hold"
	case score >= -3: signal, signalClass = "LIGHT TRIM", "signal-light-trim"
	case score >= -5: signal, signalClass = "TRIM", "signal-trim"
	case score >= -7: signal, signalClass = "SELL", "signal-sell"
	default:          signal, signalClass = "STRONG SELL", "signal-strong-sell"
	}

	if score >= 1 {
		cautionDays := 7
		if evgp > 25 { cautionDays = 21 }
		if daysToEarnings >= 0 && daysToEarnings < cautionDays {
			signalNote = "WAIT EARNINGS"
		}
	}
	return
}

// ---- Label helpers ----------------------------------------------------------

func rsiLabel(rsi float64) (label, class string) {
	switch {
	case rsi < 30: return "STRONG OVERSOLD", "rsi-strong-oversold"
	case rsi < 40: return "ACCUMULATION", "rsi-accumulation"
	case rsi < 55: return "NEUTRAL", "rsi-neutral"
	case rsi < 65: return "MOMENTUM", "rsi-momentum"
	case rsi < 75: return "EXTENDED", "rsi-extended"
	case rsi < 85: return "OVERBOUGHT", "rsi-overbought"
	default:       return "VERY OVERBOUGHT", "rsi-very-overbought"
	}
}

func pegLabel(peg float64) (label, class string) {
	switch {
	case peg < 1.0: return "UNDERVALUED", "val-under"
	case peg < 1.5: return "REASONABLE", "val-reasonable"
	case peg < 2.2: return "FAIR", "val-fair"
	case peg < 3.0: return "EXPENSIVE", "val-expensive"
	default:        return "OVERVALUED", "val-over"
	}
}

func psgLabel(psg float64) (label, class string) {
	switch {
	case psg < 0.15: return "ATTRACTIVE", "psg-very-attractive"
	case psg < 0.30: return "REASONABLE", "psg-reasonable"
	case psg < 0.50: return "EXPENSIVE", "psg-expensive"
	default:         return "PREMIUM", "psg-very-expensive"
	}
}

func evgpLabel(evgp float64) (label, class string) {
	switch {
	case evgp < 8:  return "ATTRACTIVE", "evgp-attractive"
	case evgp < 15: return "REASONABLE", "evgp-reasonable"
	case evgp < 25: return "EXPENSIVE", "evgp-expensive"
	default:        return "VERY EXPENSIVE", "evgp-very-expensive"
	}
}

// ---- Cache ------------------------------------------------------------------

const metricsCacheTTL = 30 * time.Minute

type metricsCacheEntry struct {
	data      map[string]StockMetrics
	fetchedAt time.Time
}

var (
	metricsCacheMu sync.Mutex
	metricsCache   = map[string]metricsCacheEntry{}
)

func getCachedMetrics(userID string) (map[string]StockMetrics, bool) {
	metricsCacheMu.Lock()
	defer metricsCacheMu.Unlock()
	e, ok := metricsCache[userID]
	if !ok || time.Since(e.fetchedAt) > metricsCacheTTL {
		return nil, false
	}
	return e.data, true
}

func setCachedMetrics(userID string, data map[string]StockMetrics) {
	metricsCacheMu.Lock()
	defer metricsCacheMu.Unlock()
	metricsCache[userID] = metricsCacheEntry{data: data, fetchedAt: time.Now()}
}

// ---- Per-ticker analysis (chart + valuation only) --------------------------

type partialMetrics struct {
	ticker   string
	closes   []float64
	price    float64
	currency string
	vd       valuationData
	err      error
}

func fetchPartial(ctx context.Context, client *yahooClient, ticker string) partialMetrics {
	closes, price, currency, err := client.fetchCloses(ctx, ticker)
	if err != nil {
		return partialMetrics{ticker: ticker, err: err}
	}
	vd, _ := client.fetchValuation(ctx, ticker)
	return partialMetrics{ticker: ticker, closes: closes, price: price, currency: currency, vd: vd}
}

func buildMetrics(p partialMetrics, earningsDate *time.Time) StockMetrics {
	if p.err != nil {
		return StockMetrics{Error: p.err.Error()}
	}

	rsi := calcRSI(p.closes)
	rsiLbl, rsiCls := rsiLabel(rsi)

	var targetPriceStr string
	var targetPct float64
	if p.vd.targetPrice > 0 && p.price > 0 {
		targetPct = (p.vd.targetPrice - p.price) / p.price * 100
		targetPriceStr = fmt.Sprintf("%.0f %s", p.vd.targetPrice, p.currency)
	}

	var pegLbl, pegCls, psgLbl, psgCls, evgpLbl, evgpCls string
	if p.vd.pegRatio > 0      { pegLbl, pegCls = pegLabel(p.vd.pegRatio) }
	if p.vd.psgRatio > 0      { psgLbl, psgCls = psgLabel(p.vd.psgRatio) }
	if p.vd.evGrossProfit > 0 { evgpLbl, evgpCls = evgpLabel(p.vd.evGrossProfit) }

	var earningsStr, earningsCls string
	if earningsDate != nil {
		now := time.Now()
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		d := *earningsDate
		day := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, now.Location())
		daysAway := int(day.Sub(today).Hours() / 24)
		earningsStr = d.Format("Jan 2")
		switch {
		case daysAway <= 7:  earningsCls = "earnings-soon"
		case daysAway <= 30: earningsCls = "earnings-upcoming"
		default:             earningsCls = "earnings-later"
		}
	}

	signal, signalCls, signalNote := computeSignal(rsi, p.vd.pegRatio, p.vd.psgRatio, p.vd.evGrossProfit, p.vd.targetPrice, p.price, earningsDate)

	return StockMetrics{
		RSI: rsi, RSILabel: rsiLbl, RSIClass: rsiCls,
		TargetPrice: targetPriceStr, TargetPct: targetPct, Currency: p.currency,
		PEGRatio: p.vd.pegRatio, PEGLabel: pegLbl, PEGClass: pegCls,
		PSGRatio: p.vd.psgRatio, PSGLabel: psgLbl, PSGClass: psgCls,
		EVGPRatio: p.vd.evGrossProfit, EVGPLabel: evgpLbl, EVGPClass: evgpCls,
		Earnings: earningsStr, EarningsClass: earningsCls,
		Signal: signal, SignalClass: signalCls, SignalNote: signalNote,
	}
}

// ---- HTTP handler -----------------------------------------------------------

func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(ctxUserID).(string)

	if cached, ok := getCachedMetrics(userID); ok {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cached)
		return
	}

	stocks, _, err := s.store.GetPortfolio(r.Context(), userID)
	if err != nil {
		http.Error(w, "failed to load portfolio", http.StatusInternalServerError)
		return
	}

	client := newYahooClient()

	// Phase 1: chart + valuation in parallel (max 3 concurrent)
	const maxConcurrent = 3
	sem := make(chan struct{}, maxConcurrent)
	partials := make([]partialMetrics, len(stocks))
	var wg sync.WaitGroup
	for i, stock := range stocks {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, ticker string) {
			defer wg.Done()
			defer func() { <-sem }()
			partials[i] = fetchPartial(r.Context(), client, ticker)
		}(i, stock.Ticker)
	}
	wg.Wait()

	// Phase 2: earnings sequentially with 200ms delay to avoid rate-limiting
	earningsDates := make(map[string]*time.Time, len(stocks))
	for _, p := range partials {
		if p.err != nil {
			continue
		}
		t, _ := client.fetchEarnings(r.Context(), p.ticker)
		earningsDates[p.ticker] = t
		time.Sleep(200 * time.Millisecond)
	}

	// Phase 3: assemble results
	results := make(map[string]StockMetrics, len(stocks))
	for _, p := range partials {
		results[p.ticker] = buildMetrics(p, earningsDates[p.ticker])
	}

	setCachedMetrics(userID, results)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}
