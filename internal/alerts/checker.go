package alerts

import (
	"math"
	"stock-portfolio/internal/models"
)

// IntradayPrice holds the intraday price data for a stock.
type IntradayPrice struct {
	Stock         models.Stock
	OpenPrice     float64
	CurrentPrice  float64
	ChangePercent float64
}

// Alert represents a triggered price alert.
type Alert struct {
	Stock         models.Stock
	OpenPrice     float64
	CurrentPrice  float64
	ChangePercent float64
	Threshold     float64 // signed: +3.0 means up, -3.0 means down
}

// Check returns new alerts that have not been sent yet.
// It also updates the state for each triggered alert.
func Check(prices []IntradayPrice, thresholds []float64, state *State) []Alert {
	var triggered []Alert

	for _, p := range prices {
		for _, t := range thresholds {
			// Check upward threshold
			if p.ChangePercent >= t && !state.HasBeenSent(p.Stock.Ticker, t) {
				triggered = append(triggered, Alert{
					Stock:         p.Stock,
					OpenPrice:     p.OpenPrice,
					CurrentPrice:  p.CurrentPrice,
					ChangePercent: p.ChangePercent,
					Threshold:     t,
				})
				state.MarkSent(p.Stock.Ticker, t)
			}

			// Check downward threshold (symmetric)
			neg := -math.Abs(t)
			if p.ChangePercent <= neg && !state.HasBeenSent(p.Stock.Ticker, neg) {
				triggered = append(triggered, Alert{
					Stock:         p.Stock,
					OpenPrice:     p.OpenPrice,
					CurrentPrice:  p.CurrentPrice,
					ChangePercent: p.ChangePercent,
					Threshold:     neg,
				})
				state.MarkSent(p.Stock.Ticker, neg)
			}
		}
	}

	return triggered
}
