// Package decisions evaluates swing setups using one deterministic policy for all views.
package decisions

import (
	"fmt"
	"math"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/models"
)

type Series struct {
	Candles    []chartcalc.Candle
	Timezone   string
	SessionEnd time.Time
}

type Input struct {
	QuoteType      string
	Daily, Weekly  Series
	PEG, PSG, EVGP float64
	TargetPrice    float64
	Quality        *models.FinancialQuality
	Earnings       *time.Time
}

func finite(x float64) bool   { return !math.IsNaN(x) && !math.IsInf(x, 0) }
func positive(x float64) bool { return finite(x) && x > 0 }

func validate(s Series, now time.Time, days int) bool {
	if len(s.Candles) < 54 {
		return false
	}
	for i, c := range s.Candles {
		if c.Time <= 0 || !positive(c.Close) || !positive(c.Open) || !positive(c.High) || !positive(c.Low) || c.High < c.Low || c.High < c.Close || c.Low > c.Close || (i > 0 && c.Time <= s.Candles[i-1].Time) || time.Unix(c.Time, 0).After(now.Add(5*time.Minute)) {
			return false
		}
	}
	return !time.Unix(s.Candles[len(s.Candles)-1].Time, 0).Before(now.AddDate(0, 0, -days))
}

func ema(c []chartcalc.Candle, period int) []float64 {
	out := make([]float64, len(c))
	for i := range out {
		out[i] = math.NaN()
	}
	if len(c) < period {
		return out
	}
	var v float64
	for _, b := range c[:period] {
		v += b.Close
	}
	v /= float64(period)
	out[period-1] = v
	for i := period; i < len(c); i++ {
		v += (c[i].Close - v) * 2 / float64(period+1)
		out[i] = v
	}
	return out
}

func momentum(c []chartcalc.Candle) (float64, float64, float64) {
	rsi := chartcalc.CalcRSI14(c)
	fast, slow := ema(c, 12), ema(c, 26)
	macd := make([]chartcalc.Candle, 0, len(c)-25)
	for i := 25; i < len(c); i++ {
		macd = append(macd, chartcalc.Candle{Close: fast[i] - slow[i]})
	}
	signal := ema(macd, 9)
	return rsi[len(rsi)-1].Value, macd[len(macd)-1].Close, signal[len(signal)-1]
}

func technical(s Series, weekly bool) (float64, string) {
	c := s.Candles
	n := len(c) - 1
	e20, e50 := ema(c, 20), ema(c, 50)
	price := c[n].Close
	var structure, mom float64
	rsi, macd, signal := momentum(c)
	if weekly {
		switch {
		case price > e20[n] && e20[n] > e50[n] && e20[n] > e20[n-4]:
			structure = 3
		case price < e20[n] && e20[n] < e50[n] && e20[n] < e20[n-4]:
			structure = -3
		case price > e50[n]:
			structure = 1
		case price < e50[n]:
			structure = -1
		}
		if rsi >= 55 {
			mom = 1
		} else if rsi <= 45 {
			mom = -1
		}
	} else {
		if price > e20[n] && e20[n] > e20[n-1] {
			structure = 1
		} else if price < e20[n] && e20[n] < e20[n-1] {
			structure = -1
		}
		if rsi >= 55 && macd > signal {
			mom = 1
		} else if rsi <= 45 && macd < signal {
			mom = -1
		}
	}
	period := "Daily"
	if weekly {
		period = "Weekly"
	}
	return structure + mom, fmt.Sprintf("%s : structure %+.0f · momentum %+.0f (RSI %.1f)", period, structure, mom, rsi)
}

func bucket(v float64, thresholds [4]float64) float64 {
	for i, t := range thresholds {
		if v < t {
			return float64(2 - i)
		}
	}
	return -2
}

func fundamentals(in Input, now time.Time) (float64, bool) {
	q := in.Quality
	if q == nil || q.CollectedAt.IsZero() || q.CollectedAt.After(now.Add(5*time.Minute)) || q.CollectedAt.Before(now.AddDate(0, 0, -7)) {
		return 0, false
	}
	values := []struct {
		v          float64
		thresholds [4]float64
	}{
		{in.PEG, [4]float64{1, 1.7, 2.5, 4}}, {in.PSG, [4]float64{.15, .30, .45, .65}}, {in.EVGP, [4]float64{8, 15, 25, 35}},
	}
	var valuation, quality float64
	count := 0
	for _, v := range values {
		if positive(v.v) {
			count++
			valuation += bucket(v.v, v.thresholds)
		}
	}
	if count < 2 {
		return 0, false
	}
	for _, v := range []*float64{q.ProfitMargin, q.RevenueGrowth, q.FreeCashflow} {
		if v == nil || !finite(*v) {
			return 0, false
		}
		if *v > 0 {
			quality += 2
		} else if *v < 0 {
			quality -= 2
		}
	}
	return valuation/float64(count) + quality/3, true
}

func provisional(s Series, now time.Time, weekly bool) bool {
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil || s.Timezone == "" {
		return true
	}
	at := time.Unix(s.Candles[len(s.Candles)-1].Time, 0).In(loc)
	now = now.In(loc)
	if weekly {
		y, w := now.ISOWeek()
		ay, aw := at.ISOWeek()
		return y == ay && w == aw
	}
	if at.Format("2006-01-02") != now.Format("2006-01-02") {
		return false
	}
	return s.SessionEnd.IsZero() || s.SessionEnd.In(loc).Format("2006-01-02") != at.Format("2006-01-02") || now.Before(s.SessionEnd)
}

func badge(score float64) string {
	switch {
	case score >= 7:
		return "STRONG BUY"
	case score >= 4:
		return "BUY"
	case score >= 1:
		return "ACCUMULATE"
	case score > -1:
		return "HOLD"
	case score > -3:
		return "LIGHT TRIM"
	case score > -5:
		return "TRIM"
	case score > -7:
		return "SELL"
	default:
		return "STRONG SELL"
	}
}

func Evaluate(in Input, now time.Time) *models.Evaluation {
	e := &models.Evaluation{Version: models.EvaluationVersion, EvaluatedAt: now.UTC(), QuoteType: in.QuoteType, Basis: "Technique + fondamentaux"}
	e.EarningsAt = in.Earnings
	if positive(in.TargetPrice) {
		v := in.TargetPrice
		e.AnalystTarget = &v
	}
	if q := in.Quality; q != nil && q.TotalDebt != nil && q.TotalCash != nil && q.OperatingCashflow != nil && finite(*q.TotalDebt) && *q.TotalDebt >= 0 && finite(*q.TotalCash) && *q.TotalCash >= 0 && positive(*q.OperatingCashflow) {
		v := math.Max(0, *q.TotalDebt-*q.TotalCash) / *q.OperatingCashflow
		e.NetDebtToCashflow = &v
	}
	if in.QuoteType == "ETF" {
		e.Basis = "Technique"
	} else if in.QuoteType != "EQUITY" {
		e.Warnings = append(e.Warnings, "Type d’instrument non admissible ou inconnu")
		return e
	}
	if !validate(in.Daily, now, 7) {
		e.Warnings = append(e.Warnings, "Historique daily absent, insuffisant, ancien ou invalide")
	}
	if !validate(in.Weekly, now, 14) {
		e.Warnings = append(e.Warnings, "Historique weekly absent, insuffisant, ancien ou invalide")
	}
	if len(e.Warnings) > 0 {
		return e
	}
	c := in.Daily.Candles
	wc := in.Weekly.Candles
	e.Price = c[len(c)-1].Close
	e.PriceAt = time.Unix(c[len(c)-1].Time, 0).UTC()
	e.WeeklyAt = time.Unix(wc[len(wc)-1].Time, 0).UTC()
	dc := (e.Price/c[len(c)-2].Close - 1) * 100
	wchange := (wc[len(wc)-1].Close/wc[len(wc)-2].Close - 1) * 100
	e.DailyChange = &dc
	e.WeeklyChange = &wchange
	var wr, dr string
	e.Weekly, wr = technical(in.Weekly, true)
	e.Daily, dr = technical(in.Daily, false)
	e.Reasons = []string{wr, dr}
	e.Score = e.Weekly + e.Daily
	if in.QuoteType == "EQUITY" {
		f, ok := fundamentals(in, now)
		if !ok {
			e.Warnings = append(e.Warnings, "Fondamentaux incomplets ou anciens")
			return e
		}
		e.Fundamental = &f
		e.Score += f
		e.Pullback = evaluatePullback(in, f, e.Daily, e.Weekly)
	} else {
		e.Score *= 10.0 / 6
	}
	e.Eligible = true
	e.Signal = badge(e.Score)
	if e.Signal == "STRONG BUY" && (e.Daily <= 0 || (e.Fundamental != nil && *e.Fundamental <= 0)) {
		e.Signal = "BUY"
	}
	if (e.Signal == "BUY" || e.Signal == "STRONG BUY") && (e.Weekly <= 0 || e.Daily < 0) {
		e.Signal = "ACCUMULATE"
		e.Warnings = append(e.Warnings, "Confirmation technique insuffisante pour BUY")
	}
	if in.QuoteType == "EQUITY" && e.Score < 1 && *e.Fundamental >= 2 && e.Weekly > -3 && e.Daily > -2 {
		e.Signal = "ACCUMULATE"
		e.Warnings = append(e.Warnings, "Accumulation contre-tendance")
	}
	e.Provisional = provisional(in.Daily, now, false) || provisional(in.Weekly, now, true)
	if e.Provisional {
		e.Warnings = append(e.Warnings, "Provisoire")
		if e.Signal == "STRONG BUY" {
			e.Signal = "BUY"
		}
		if e.Signal == "STRONG SELL" {
			e.Signal = "SELL"
		}
	}
	if in.QuoteType == "EQUITY" {
		if in.Earnings == nil {
			e.Warnings = append(e.Warnings, "Date de résultats inconnue")
		} else if !in.Earnings.Before(now) && !in.Earnings.After(now.AddDate(0, 0, 7)) {
			e.Warnings = append(e.Warnings, "WAIT EARNINGS")
			if e.Signal == "STRONG BUY" {
				e.Signal = "BUY"
			}
		}
	}
	return e
}

// Direction and strength are based on the final badge, after all safeguards.
func Direction(signal string) int {
	switch signal {
	case "STRONG BUY", "BUY", "ACCUMULATE":
		return 1
	case "LIGHT TRIM", "TRIM", "SELL", "STRONG SELL":
		return -1
	}
	return 0
}
func Strength(signal string) int {
	switch signal {
	case "STRONG BUY", "STRONG SELL":
		return 4
	case "BUY", "SELL":
		return 3
	case "TRIM":
		return 2
	case "ACCUMULATE", "LIGHT TRIM":
		return 1
	}
	return 0
}
