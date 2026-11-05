package technicalalerts

import (
	"math"
	"time"

	"stock-portfolio/internal/chartcalc"
)

// Yahoo may publish today's OHLC before filling its close array. The regular
// quote can complete that last daily candle only when it belongs to the same
// exchange session and agrees with the reported trading range. Never use an
// extended-hours quote or carry a newer price into an older candle.
func recoverDailyClose(timestamp int64, open, high, low *float64, meta yahooSeriesMeta, now time.Time) (chartcalc.Candle, bool) {
	if timestamp <= 0 || open == nil || high == nil || low == nil || meta.Timezone == "" {
		return chartcalc.Candle{}, false
	}
	for _, price := range []float64{*open, *high, *low, meta.RegularMarketPrice} {
		if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
			return chartcalc.Candle{}, false
		}
	}
	loc, err := time.LoadLocation(meta.Timezone)
	if err != nil {
		return chartcalc.Candle{}, false
	}
	bar, quote := time.Unix(timestamp, 0).In(loc), time.Unix(meta.RegularMarketTime, 0).In(loc)
	if quote.Before(bar) || quote.After(now.Add(5*time.Minute)) || bar.Format("2006-01-02") != quote.Format("2006-01-02") {
		return chartcalc.Candle{}, false
	}
	price := meta.RegularMarketPrice
	// OHLC arrays often use float32 while the metadata quote uses decimal prices.
	tolerance := price * 1e-6
	if *high < *low || *open > *high || *open < *low || price > *high+tolerance || price < *low-tolerance {
		return chartcalc.Candle{}, false
	}
	return chartcalc.Candle{Time: timestamp, Open: *open, High: max(*high, price), Low: min(*low, price), Close: price}, true
}
