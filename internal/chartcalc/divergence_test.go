package chartcalc

import "testing"

func TestRecentEdgeHighCanTriggerBearishDivergence(t *testing.T) {
	candles := []Candle{
		{Time: 1, High: 80, Low: 70, Close: 75},
		{Time: 2, High: 82, Low: 71, Close: 76},
		{Time: 3, High: 85, Low: 72, Close: 78},
		{Time: 4, High: 90, Low: 75, Close: 86},
		{Time: 5, High: 95, Low: 80, Close: 92},
		{Time: 6, High: 100, Low: 86, Close: 95},
		{Time: 7, High: 93, Low: 82, Close: 86},
		{Time: 8, High: 91, Low: 80, Close: 84},
		{Time: 9, High: 92, Low: 81, Close: 85},
		{Time: 10, High: 88, Low: 78, Close: 82},
		{Time: 11, High: 90, Low: 79, Close: 84},
		{Time: 12, High: 94, Low: 82, Close: 90},
		{Time: 13, High: 97, Low: 85, Close: 94},
		{Time: 14, High: 102, Low: 89, Close: 100},
		{Time: 15, High: 110, Low: 95, Close: 104},
	}
	rsi := []LinePoint{
		{Time: 6, Value: 75},
		{Time: 15, Value: 66},
	}

	pivots := CalcPivots(candles, rsi)
	divs := CalcRSIDivergences(candles, pivots)

	if len(divs) != 1 {
		t.Fatalf("expected one divergence, got %d: %#v", len(divs), divs)
	}
	if divs[0].Kind != "bearish" || divs[0].FromTime != 6 || divs[0].ToTime != 15 {
		t.Fatalf("unexpected divergence: %#v", divs[0])
	}
}
