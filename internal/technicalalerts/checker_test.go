package technicalalerts

import (
	"testing"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/models"
)

func TestCheckCandlesKeepsEMAProximityAndDetectsReclaim(t *testing.T) {
	candles := risingCandles(60, 100, 0)
	candles[58].Close = 99
	candles[59].Close = 102
	candles[59].Low = 99
	candles[59].High = 103

	alerts := CheckCandles(testStock(), Daily, candles, 5)

	if !hasKind(alerts, EMAReclaim) {
		t.Fatal("expected EMA reclaim signal")
	}
	if hasKindPeriod(alerts, EMAProximity, 50) {
		t.Fatal("did not expect EMA50 proximity when EMA50 reclaim is present")
	}
}

func TestCheckCandlesDetectsRSIRegimeUp(t *testing.T) {
	candles := make([]chartcalc.Candle, 20)
	for i := range candles {
		close := 100.0 - float64(i)
		if i >= 15 {
			close = 85 + float64(i-15)*4
		}
		candles[i] = chartcalc.Candle{Time: int64(i + 1), Open: close, High: close + 1, Low: close - 1, Close: close}
	}

	alerts := CheckCandles(testStock(), Daily, candles, 2)
	if !hasKind(alerts, RSIRegimeUp) {
		t.Fatalf("expected RSI regime up, got %#v", alerts)
	}
}

func TestCheckCandlesDetectsBreakout(t *testing.T) {
	candles := risingCandles(25, 100, 0)
	for i := 4; i < 24; i++ {
		candles[i].High = 110
		candles[i].Close = 105
	}
	candles[24].Close = 112
	candles[24].High = 113

	alerts := CheckCandles(testStock(), Daily, candles, 2)
	if !hasKind(alerts, Breakout) {
		t.Fatalf("expected breakout, got %#v", alerts)
	}
}

func TestWeeklyStateScope(t *testing.T) {
	scope := StateScope(Weekly, time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	if scope != "2026-W33" {
		t.Fatalf("expected ISO week scope, got %q", scope)
	}
}

func TestCalcDailyAndWeeklyChangesUsesFiveSessions(t *testing.T) {
	candles := []chartcalc.Candle{
		{Close: 100},
		{Close: 102},
		{Close: 104},
		{Close: 106},
		{Close: 108},
		{Close: 110},
	}

	change := CalcDailyAndWeeklyChanges(candles)
	if !change.HasDailyChange || change.DailyChange < 1.85 || change.DailyChange > 1.86 {
		t.Fatalf("unexpected daily change: %+v", change)
	}
	if !change.HasWeeklyChange || change.WeeklyChange != 10 {
		t.Fatalf("expected weekly change from five sessions ago, got %+v", change)
	}
}

func TestMACDCrossFilteredByEMA50(t *testing.T) {
	candles := risingCandles(80, 100, 0.2)
	for i := 72; i < 79; i++ {
		candles[i].Close -= float64(i-71) * 1.5
		candles[i].High = candles[i].Close + 1
		candles[i].Low = candles[i].Close - 1
	}
	candles[79].Close = candles[78].Close + 20
	candles[79].High = candles[79].Close + 1
	candles[79].Low = candles[79].Close - 1

	alerts := CheckCandles(testStock(), Daily, candles, 1)
	if !hasKind(alerts, MACDBullish) {
		t.Fatalf("expected bullish MACD cross, got %#v", alerts)
	}
}

func risingCandles(n int, base float64, step float64) []chartcalc.Candle {
	candles := make([]chartcalc.Candle, n)
	for i := range candles {
		close := base + float64(i)*step
		candles[i] = chartcalc.Candle{Time: int64(i + 1), Open: close, High: close + 1, Low: close - 1, Close: close, Volume: 1000}
	}
	return candles
}

func testStock() models.Stock {
	return models.Stock{Ticker: "TEST", Name: "Test", Category: "USA"}
}

func hasKind(alerts []Alert, kind Kind) bool {
	for _, alert := range alerts {
		if alert.Kind == kind {
			return true
		}
	}
	return false
}

func hasKindPeriod(alerts []Alert, kind Kind, period int) bool {
	for _, alert := range alerts {
		if alert.Kind == kind && alert.Period == period {
			return true
		}
	}
	return false
}
