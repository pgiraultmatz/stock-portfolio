package main

import (
	"math"
	"testing"
	"time"
)

func TestTrimChartResponseKeepsEMA200WarmedFromFullHistory(t *testing.T) {
	candles := make([]ChartCandle, 0, 760)
	start := time.Date(2024, time.May, 20, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 760; i++ {
		close := 100 + float64(i)*0.45 + math.Sin(float64(i)/9)*4
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 0.7,
			High:   close + 1.4,
			Low:    close - 1.6,
			Close:  close,
			Volume: int64(1_000_000 + i*1000),
		})
	}

	fullEMA200 := calcEMALine(candles, 200)
	trimmed := trimChartResponse(ChartResponse{
		Symbol:   "TEST",
		Range:    "2y",
		Interval: "1d",
		Candles:  candles,
	}, "1y", "1d", "ema")

	if len(trimmed.Candles) == 0 {
		t.Fatal("expected visible candles after trim")
	}
	if len(trimmed.EMA200) == 0 {
		t.Fatal("expected EMA200 after trim")
	}

	firstVisibleTime := trimmed.Candles[0].Time
	if trimmed.EMA200[0].Time != firstVisibleTime {
		t.Fatalf("EMA200 should be warmed before trim: first EMA time %d, first visible candle time %d", trimmed.EMA200[0].Time, firstVisibleTime)
	}

	expected, ok := chartLineValueAtOrBefore(fullEMA200, firstVisibleTime)
	if !ok {
		t.Fatal("expected full-history EMA200 at first visible candle")
	}
	if math.Abs(trimmed.EMA200[0].Value-expected) > 0.000001 {
		t.Fatalf("EMA200 should match full-history value: got %.8f want %.8f", trimmed.EMA200[0].Value, expected)
	}
}

func TestLowerHighsRemainVisibleAfterReclaimButSetupIsInactive(t *testing.T) {
	candles := make([]ChartCandle, 0, 60)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 60; i++ {
		close := 80.0
		switch i {
		case 10:
			close = 100
		case 20:
			close = 90
		case 28:
			close = 92
		case 59:
			close = 88
		}
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   close + 2,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	pivots := []ChartPivot{
		{Time: candles[10].Time, Kind: "high", Price: 100},
		{Time: candles[20].Time, Kind: "high", Price: 90},
	}

	lowerHighs := calcChartPivotStructures(candles, pivots, "lower_high")
	if len(lowerHighs) == 0 {
		t.Fatal("expected lower highs to remain visible after reclaim")
	}

	analysis := calcChartAnalysis(ChartResponse{
		Symbol:     "TEST",
		Range:      "1y",
		Interval:   "1d",
		Candles:    candles,
		Pivots:     pivots,
		LowerHighs: lowerHighs,
	})
	for _, setup := range analysis.Setups {
		if setup.Kind == "lower_highs_pressure" {
			t.Fatalf("expected reclaimed lower-high structure to be inactive, got setup %+v", setup)
		}
	}
}

func TestCalcChartPivotStructuresDetectsAllDirections(t *testing.T) {
	candles := make([]ChartCandle, 0, 70)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 70; i++ {
		close := 100.0
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   close + 2,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	pivots := []ChartPivot{
		{Time: candles[10].Time, Kind: "high", Price: 100},
		{Time: candles[15].Time, Kind: "low", Price: 50},
		{Time: candles[20].Time, Kind: "high", Price: 90},
		{Time: candles[25].Time, Kind: "low", Price: 45},
		{Time: candles[30].Time, Kind: "high", Price: 110},
		{Time: candles[35].Time, Kind: "low", Price: 55},
		{Time: candles[40].Time, Kind: "high", Price: 125},
		{Time: candles[45].Time, Kind: "low", Price: 65},
	}

	cases := []string{"lower_high", "lower_low", "higher_high", "higher_low"}
	for _, kind := range cases {
		got := calcChartPivotStructures(candles, pivots, kind)
		if len(got) < 2 {
			t.Fatalf("expected %s structure, got %+v", kind, got)
		}
		if got[len(got)-1].Kind != kind {
			t.Fatalf("expected kind %s, got %s", kind, got[len(got)-1].Kind)
		}
	}
}

func TestCalcChartPivotStructuresDetectsTimeframeATHAsHigherHigh(t *testing.T) {
	candles := make([]ChartCandle, 0, 70)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 70; i++ {
		close := 650.0
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   close + 2,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	pivots := []ChartPivot{
		{Time: candles[20].Time, Kind: "high", Price: 688},
		{Time: candles[45].Time, Kind: "high", Price: 698},
	}

	higherHighs := calcChartPivotStructures(candles, pivots, "higher_high")
	if len(higherHighs) != 2 {
		t.Fatalf("expected timeframe ATH to be accepted as higher high below normal threshold, got %+v", higherHighs)
	}
	if higherHighs[1].Price != 698 {
		t.Fatalf("expected 698 ATH to be retained, got %+v", higherHighs[1])
	}
}

func TestCalcChartPivotStructuresPrefersRecentAdjacentLowerPair(t *testing.T) {
	candles := make([]ChartCandle, 0, 90)
	start := time.Date(2026, time.January, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 90; i++ {
		close := 100.0
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   close + 2,
			Low:    close - 2,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	highs := []ChartPivot{
		{Time: candles[10].Time, Kind: "high", Price: 100},
		{Time: candles[20].Time, Kind: "high", Price: 120},
		{Time: candles[40].Time, Kind: "high", Price: 140},
		{Time: candles[70].Time, Kind: "high", Price: 130},
	}
	lows := []ChartPivot{
		{Time: candles[12].Time, Kind: "low", Price: 80},
		{Time: candles[22].Time, Kind: "low", Price: 90},
		{Time: candles[42].Time, Kind: "low", Price: 100},
		{Time: candles[72].Time, Kind: "low", Price: 95},
	}

	lowerHighs := calcChartPivotStructures(candles, highs, "lower_high")
	if len(lowerHighs) != 2 || lowerHighs[1].Price != 130 {
		t.Fatalf("expected recent adjacent lower high pair ending at 130, got %+v", lowerHighs)
	}
	lowerLows := calcChartPivotStructures(candles, lows, "lower_low")
	if len(lowerLows) != 2 || lowerLows[1].Price != 95 {
		t.Fatalf("expected recent adjacent lower low pair ending at 95, got %+v", lowerLows)
	}
}

func TestCalcChartPivotStructuresDetectsRecentVOOLowerHighAndLowerLow(t *testing.T) {
	candles := make([]ChartCandle, 0, 70)
	start := time.Date(2026, time.March, 1, 21, 0, 0, 0, time.UTC)
	for i := 0; i < 70; i++ {
		close := 680.0
		high := close + 2
		low := close - 2
		switch i {
		case 35:
			close, high, low = 688, 689.10, 684
		case 38:
			close, high, low = 674, 680, 672.55
		case 49:
			close, high, low = 698, 699.15, 695
		case 52:
			close, high, low = 696, 697, 690
		case 54:
			close, high, low = 684, 692, 676
		case 56:
			close, high, low = 678, 686, 664.32
		case 62:
			close, high, low = 693, 695.75, 691
		case 63:
			close, high, low = 689, 694.57, 688
		case 64:
			close, high, low = 681, 691.53, 679
		}
		candles = append(candles, ChartCandle{
			Time:   start.AddDate(0, 0, i).Unix(),
			Open:   close - 1,
			High:   high,
			Low:    low,
			Close:  close,
			Volume: 1_000_000,
		})
	}
	pivots := []ChartPivot{
		{Time: candles[35].Time, Kind: "high", Price: 689.10},
		{Time: candles[38].Time, Kind: "low", Price: 672.55},
		{Time: candles[49].Time, Kind: "high", Price: 699.15},
		{Time: candles[56].Time, Kind: "low", Price: 664.32},
	}

	lowerLows := calcChartPivotStructures(candles, pivots, "lower_low")
	if len(lowerLows) != 2 || math.Abs(lowerLows[1].Price-664.32) > 0.001 {
		t.Fatalf("expected recent VOO lower low ending at 664.32, got %+v", lowerLows)
	}

	lowerHighs := calcChartPivotStructures(candles, pivots, "lower_high")
	if len(lowerHighs) != 2 || math.Abs(lowerHighs[1].Price-695.75) > 0.001 {
		t.Fatalf("expected recent VOO lower high ending at 695.75, got %+v", lowerHighs)
	}
}
