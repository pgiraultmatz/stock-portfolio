// Package analysis provides financial analysis functions.
package analysis

import "math"

// RSICalculator calculates the Relative Strength Index using Wilder's smoothing method.
type RSICalculator struct {
	Period int
}

// NewRSICalculator creates a new RSI calculator with the specified period.
func NewRSICalculator(period int) *RSICalculator {
	if period <= 0 {
		period = 14 // Default RSI period
	}
	return &RSICalculator{Period: period}
}

// Calculate computes the RSI for a series of closing prices using Wilder's smoothing.
// This matches TradingView's RSI calculation method.
// Returns 0 if there's insufficient data.
func (r *RSICalculator) Calculate(closes []float64) float64 {
	// Filter out any NaN or invalid values
	validCloses := make([]float64, 0, len(closes))
	for _, c := range closes {
		if !math.IsNaN(c) && c > 0 {
			validCloses = append(validCloses, c)
		}
	}

	// Need at least period + 1 data points
	if len(validCloses) < r.Period+1 {
		return 0
	}

	// Calculate price changes (gains and losses)
	gains := make([]float64, len(validCloses)-1)
	losses := make([]float64, len(validCloses)-1)

	for i := 1; i < len(validCloses); i++ {
		change := validCloses[i] - validCloses[i-1]
		if change > 0 {
			gains[i-1] = change
			losses[i-1] = 0
		} else {
			gains[i-1] = 0
			losses[i-1] = math.Abs(change)
		}
	}

	// Calculate initial SMA for first 'period' values
	var avgGain, avgLoss float64
	for i := 0; i < r.Period; i++ {
		avgGain += gains[i]
		avgLoss += losses[i]
	}
	avgGain /= float64(r.Period)
	avgLoss /= float64(r.Period)

	// Apply Wilder's smoothing for remaining values
	// Formula: avgGain = (prevAvgGain * (period - 1) + currentGain) / period
	for i := r.Period; i < len(gains); i++ {
		avgGain = (avgGain*float64(r.Period-1) + gains[i]) / float64(r.Period)
		avgLoss = (avgLoss*float64(r.Period-1) + losses[i]) / float64(r.Period)
	}

	// Calculate RSI
	if avgLoss == 0 {
		if avgGain == 0 {
			return 50 // Neutral if no movement
		}
		return 100 // All gains, no losses
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))

	return rsi
}

// CalculatePriceChange calculates the percentage change between current and previous price.
func CalculatePriceChange(closes []float64) (currentPrice, changePercent float64) {
	if len(closes) < 2 {
		return 0, 0
	}

	// Filter out invalid values
	validCloses := make([]float64, 0, len(closes))
	for _, c := range closes {
		if !math.IsNaN(c) && c > 0 {
			validCloses = append(validCloses, c)
		}
	}

	if len(validCloses) < 2 {
		return 0, 0
	}

	currentPrice = validCloses[len(validCloses)-1]

	// Find the last different price for change calculation
	for i := len(validCloses) - 2; i >= 0; i-- {
		if validCloses[i] != currentPrice && validCloses[i] > 0 {
			previousPrice := validCloses[i]
			changePercent = ((currentPrice - previousPrice) / previousPrice) * 100

			if math.Abs(changePercent) > 0.01 {
				return currentPrice, changePercent
			}
		}
	}

	return currentPrice, 0
}
