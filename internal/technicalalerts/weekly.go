package technicalalerts

import (
	"fmt"
	"math"
	"time"

	"stock-portfolio/internal/chartcalc"
)

// Yahoo can append today's quote as a separate row after the current weekly
// candle. Combine those partial periods before computing returns or indicators.
// Calendar weeks must use the exchange timezone: Paris Monday is Sunday in UTC.
func mergeWeeklyQuoteRows(candles []chartcalc.Candle, timezone string) ([]chartcalc.Candle, error) {
	loc := time.UTC
	if timezone != "" {
		var err error
		loc, err = time.LoadLocation(timezone)
		if err != nil {
			return nil, err
		}
	}
	merged := make([]chartcalc.Candle, 0, len(candles))
	var previousTime int64
	for i, candle := range candles {
		if candle.Time <= 0 || (i > 0 && candle.Time <= previousTime) {
			return nil, fmt.Errorf("weekly timestamps are invalid or unordered")
		}
		previousTime = candle.Time
		for _, price := range []float64{candle.Open, candle.High, candle.Low, candle.Close} {
			if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
				return nil, fmt.Errorf("invalid weekly OHLC")
			}
		}
		if candle.High < max(candle.Open, candle.Close) || candle.Low > min(candle.Open, candle.Close) || candle.High < candle.Low {
			return nil, fmt.Errorf("inconsistent weekly OHLC")
		}
		if len(merged) > 0 {
			last := &merged[len(merged)-1]
			y, w := time.Unix(last.Time, 0).In(loc).ISOWeek()
			cy, cw := time.Unix(candle.Time, 0).In(loc).ISOWeek()
			if y == cy && w == cw {
				last.High = max(last.High, candle.High)
				last.Low = min(last.Low, candle.Low)
				last.Close = candle.Close
				last.Volume += candle.Volume
				continue
			}
		}
		merged = append(merged, candle)
	}
	return merged, nil
}
