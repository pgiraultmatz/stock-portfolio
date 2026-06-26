package analysis

import (
	"math"
	"testing"
)

func TestRSICalculator_Calculate(t *testing.T) {
	tests := []struct {
		name    string
		closes  []float64
		period  int
		wantMin float64
		wantMax float64
	}{
		{
			name:    "insufficient data",
			closes:  []float64{100, 101, 102},
			period:  14,
			wantMin: 0,
			wantMax: 0,
		},
		{
			name: "all gains (bullish)",
			closes: []float64{
				100, 101, 102, 103, 104, 105, 106, 107,
				108, 109, 110, 111, 112, 113, 114, 115,
			},
			period:  14,
			wantMin: 95,
			wantMax: 100,
		},
		{
			name: "all losses (bearish)",
			closes: []float64{
				115, 114, 113, 112, 111, 110, 109, 108,
				107, 106, 105, 104, 103, 102, 101, 100,
			},
			period:  14,
			wantMin: 0,
			wantMax: 5,
		},
		{
			name: "mixed movement with upward bias",
			closes: []float64{
				100, 102, 101, 103, 102, 104, 103, 105,
				104, 106, 105, 107, 106, 108, 107, 109,
			},
			period:  14,
			wantMin: 60,
			wantMax: 70,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewRSICalculator(tt.period)
			got := calc.Calculate(tt.closes)

			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("RSI = %v, want between %v and %v", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestRSICalculator_WilderSmoothing(t *testing.T) {
	// Test that Wilder's smoothing is applied correctly
	// With more data points after the initial period, RSI should be smoothed
	calc := NewRSICalculator(14)

	// Create a series with initial gains then some losses
	closes := []float64{
		44, 44.34, 44.09, 43.61, 44.33, 44.83, 45.10, 45.42,
		45.84, 46.08, 45.89, 46.03, 45.61, 46.28, 46.28, 46.00,
		46.03, 46.41, 46.22, 45.64,
	}

	rsi := calc.Calculate(closes)

	// RSI should be in a reasonable range for this mixed data
	if rsi < 30 || rsi > 70 {
		t.Errorf("RSI = %v, expected moderate value between 30 and 70", rsi)
	}
}

func TestCalculatePriceChange(t *testing.T) {
	tests := []struct {
		name              string
		closes            []float64
		wantPrice         float64
		wantChangePercent float64
	}{
		{
			name:              "empty closes",
			closes:            []float64{},
			wantPrice:         0,
			wantChangePercent: 0,
		},
		{
			name:              "single close",
			closes:            []float64{100},
			wantPrice:         0,
			wantChangePercent: 0,
		},
		{
			name:              "price increase",
			closes:            []float64{100, 110},
			wantPrice:         110,
			wantChangePercent: 10,
		},
		{
			name:              "price decrease",
			closes:            []float64{100, 90},
			wantPrice:         90,
			wantChangePercent: -10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			price, change := CalculatePriceChange(tt.closes)

			if price != tt.wantPrice {
				t.Errorf("price = %v, want %v", price, tt.wantPrice)
			}

			// Allow small floating point differences
			diff := math.Abs(change - tt.wantChangePercent)
			if diff > 0.01 {
				t.Errorf("change = %v, want %v", change, tt.wantChangePercent)
			}
		})
	}
}
