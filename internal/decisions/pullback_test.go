package decisions

import (
	"math"
	"testing"
	"time"
)

func dipFixture(drop float64) Input {
	in := fixture()
	in.Quality.OperatingMargin = ptr(.2)
	n := len(in.Daily.Candles)
	last := &in.Daily.Candles[n-1]
	last.Close = in.Daily.Candles[n-2].Close * (1 - drop/100)
	last.Open, last.High, last.Low = last.Close, last.Close+1, last.Close-1
	return in
}

func TestPullbackScreenAllowsWeakChartsWithoutChangingSwingSignal(t *testing.T) {
	in := dipFixture(20)
	in.Weekly = trend(testNow.AddDate(0, 0, -7), 7*24*time.Hour, false)
	e := Evaluate(in, testNow)
	if e.Pullback == nil || !e.Pullback.Eligible || math.Abs(e.Pullback.Drawdown20-20) > 1e-8 || e.Pullback.Quality != 2 || e.Pullback.Valuation != 2 {
		t.Fatalf("missing fundamentally strong pullback: %+v", e.Pullback)
	}
	if e.Signal != "LIGHT TRIM" || e.Score != -2 || e.Daily != -2 || e.Weekly != -4 {
		t.Fatalf("pullback changed swing score: %+v", e)
	}
}

func TestPullbackScreenRejectsMissingWeakOrExpensiveFundamentals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Input)
	}{
		{"small correction", func(in *Input) { *in = dipFixture(9) }},
		{"negative operating margin", func(in *Input) { in.Quality.OperatingMargin = ptr(-.1) }},
		{"missing operating margin", func(in *Input) { in.Quality.OperatingMargin = nil }},
		{"invalid operating margin", func(in *Input) { in.Quality.OperatingMargin = ptr(math.NaN()) }},
		{"zero operating margin", func(in *Input) { in.Quality.OperatingMargin = ptr(0) }},
		{"shrinking revenue", func(in *Input) { in.Quality.RevenueGrowth = ptr(-.1) }},
		{"zero cashflow", func(in *Input) { in.Quality.FreeCashflow = ptr(0) }},
		{"missing cashflow", func(in *Input) { in.Quality.FreeCashflow = nil }},
		{"expensive", func(in *Input) { in.PEG, in.PSG, in.EVGP = 5, 1, 50 }},
		{"old financials", func(in *Input) { in.Quality.CollectedAt = testNow.AddDate(0, 0, -8) }},
		{"ETF", func(in *Input) { in.QuoteType = "ETF" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := dipFixture(20)
			tc.mutate(&in)
			e := Evaluate(in, testNow)
			if e.Pullback != nil && e.Pullback.Eligible {
				t.Fatalf("unexpected pullback: %+v", e.Pullback)
			}
		})
	}
}

func TestPullbackUsesRecentClosesAndIncludesTenPercentBoundary(t *testing.T) {
	in := dipFixture(10)
	// A historical spike or intraday wick must not inflate this closing-price drawdown.
	in.Daily.Candles[0].High, in.Daily.Candles[0].Close = 10000, 10000
	in.Daily.Candles[len(in.Daily.Candles)-2].High = 20000
	e := Evaluate(in, testNow)
	if e.Pullback == nil || !e.Pullback.Eligible || math.Abs(e.Pullback.Drawdown20-10) > 1e-8 {
		t.Fatalf("incorrect drawdown: %+v", e.Pullback)
	}
}

func TestPullbackAcceptsOperatingProfitDespiteNetLoss(t *testing.T) {
	in := dipFixture(33.6)
	in.Quality.ProfitMargin = ptr(-2.3)
	in.Quality.OperatingMargin = ptr(.278)
	in.PEG, in.PSG, in.EVGP = 0, .09, 49.8
	e := Evaluate(in, testNow)
	if e.Pullback == nil || !e.Pullback.Eligible || !e.Pullback.NetLoss || e.Pullback.Valuation != 0 || e.Pullback.Quality != 2 {
		t.Fatalf("operationally profitable net-loss company excluded: %+v", e.Pullback)
	}
	if math.Abs(*e.Fundamental-2.0/3) > 1e-9 {
		t.Fatalf("swing fundamentals must retain net-margin penalty: %+v", e)
	}
	in.PSG = .8
	if e = Evaluate(in, testNow); e.Pullback.Eligible {
		t.Fatal("net-loss exception must not bypass valuation filter")
	}
}
