package decisions

import (
	"math"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/models"
)

var testNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func ptr(v float64) *float64 { return &v }
func trend(last time.Time, step time.Duration, up bool) Series {
	s := Series{Timezone: "America/New_York"}
	for i := 0; i < 90; i++ {
		v := 100 + float64(i*i)/100
		if !up {
			v = 300 - v
		}
		s.Candles = append(s.Candles, chartcalc.Candle{Time: last.Add(-time.Duration(89-i) * step).Unix(), Open: v, High: v + 1, Low: v - 1, Close: v})
	}
	return s
}
func fixture() Input {
	return Input{QuoteType: "EQUITY", Daily: trend(testNow.AddDate(0, 0, -1), 24*time.Hour, true), Weekly: trend(testNow.AddDate(0, 0, -7), 7*24*time.Hour, true), PEG: .7, PSG: .1, EVGP: 5, Quality: &models.FinancialQuality{CollectedAt: testNow, ProfitMargin: ptr(.2), RevenueGrowth: ptr(.3), FreeCashflow: ptr(100)}}
}

func TestEvaluateConfirmedAndProvisional(t *testing.T) {
	in := fixture()
	e := Evaluate(in, testNow)
	if !e.Eligible || e.Signal != "STRONG BUY" || e.Score != 10 || e.Provisional {
		t.Fatalf("confirmed: %+v", e)
	}
	in.Weekly.Candles[len(in.Weekly.Candles)-1].Time = testNow.AddDate(0, 0, -3).Unix()
	e = Evaluate(in, testNow)
	if e.Signal != "BUY" || !e.Provisional || e.Score != 10 {
		t.Fatalf("provisional: %+v", e)
	}
	in = fixture()
	in.QuoteType = "ETF"
	in.Quality = nil
	e = Evaluate(in, testNow)
	if !e.Eligible || e.Signal != "STRONG BUY" || e.Score != 10 || e.Fundamental != nil {
		t.Fatalf("ETF: %+v", e)
	}
	in.Daily = trend(testNow.AddDate(0, 0, -1), 24*time.Hour, false)
	in.Weekly = trend(testNow.AddDate(0, 0, -7), 7*24*time.Hour, false)
	e = Evaluate(in, testNow)
	if e.Score != -10 || e.Signal != "STRONG SELL" {
		t.Fatalf("bearish ETF: %+v", e)
	}
	in.Weekly.Candles[len(in.Weekly.Candles)-1].Time = testNow.AddDate(0, 0, -3).Unix()
	if e = Evaluate(in, testNow); e.Signal != "SELL" || !e.Provisional {
		t.Fatalf("provisional sell: %+v", e)
	}
}

func TestEarningsAndMissingData(t *testing.T) {
	for _, days := range []int{-1, 0, 3, 7, 8} {
		in := fixture()
		date := testNow.AddDate(0, 0, days)
		in.Earnings = &date
		e := Evaluate(in, testNow)
		wait := days >= 0 && days <= 7
		if strings.Contains(strings.Join(e.Warnings, " "), "WAIT EARNINGS") != wait || (e.Signal == "BUY") != wait {
			t.Errorf("days=%d: %+v", days, e)
		}
	}
	for _, mutate := range []func(*Input){
		func(i *Input) { i.Quality = nil }, func(i *Input) { i.Quality.FreeCashflow = nil }, func(i *Input) { i.Quality.ProfitMargin = ptr(math.NaN()) },
		func(i *Input) { i.Quality.CollectedAt = testNow.AddDate(0, 0, -8) }, func(i *Input) { i.Quality.CollectedAt = testNow.Add(time.Hour) },
		func(i *Input) { i.PEG = 0; i.PSG = 0 }, func(i *Input) { i.Daily.Candles = i.Daily.Candles[:30] },
		func(i *Input) { i.Daily.Candles[len(i.Daily.Candles)-1].Time = testNow.Add(time.Hour).Unix() },
		func(i *Input) { i.Weekly = trend(testNow.AddDate(0, 0, -15), 7*24*time.Hour, true) },
		func(i *Input) { i.Daily = trend(testNow.AddDate(0, 0, -8), 24*time.Hour, true) },
		func(i *Input) { i.Daily.Candles[10].Close = 0 }, func(i *Input) { i.QuoteType = "INDEX" },
	} {
		in := fixture()
		mutate(&in)
		e := Evaluate(in, testNow)
		if e.Eligible || e.Signal != "" || len(e.Warnings) == 0 {
			t.Fatalf("invalid inputs accepted: %+v", e)
		}
	}
	in := fixture()
	in.Quality.FreeCashflow = ptr(0)
	if e := Evaluate(in, testNow); !e.Eligible || *e.Fundamental >= 4 {
		t.Fatalf("zero treated as missing or positive: %+v", e)
	}
}

func TestBadgeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		s    float64
		want string
	}{{7, "STRONG BUY"}, {6.99, "BUY"}, {4, "BUY"}, {3.99, "ACCUMULATE"}, {1, "ACCUMULATE"}, {.99, "HOLD"}, {-.99, "HOLD"}, {-1, "LIGHT TRIM"}, {-3, "TRIM"}, {-5, "SELL"}, {-7, "STRONG SELL"}} {
		if got := badge(tc.s); got != tc.want {
			t.Errorf("%v: %s != %s", tc.s, got, tc.want)
		}
	}
}

func TestConflictingTrendCapsBuy(t *testing.T) {
	in := fixture()
	in.Daily = trend(testNow.AddDate(0, 0, -1), 24*time.Hour, false)
	e := Evaluate(in, testNow)
	if e.Signal != "ACCUMULATE" || e.Daily != -2 || e.Weekly != 4 {
		t.Fatalf("daily contradiction: %+v", e)
	}
	in.Weekly = trend(testNow.AddDate(0, 0, -7), 7*24*time.Hour, false)
	e = Evaluate(in, testNow)
	if Direction(e.Signal) > 0 {
		t.Fatalf("severe breakdown must not be a buy despite good fundamentals: %+v", e)
	}
}

func TestCounterTrendAccumulationNeedsQualityAndLimitedDamage(t *testing.T) {
	in := fixture()
	in.Quality.RevenueGrowth = ptr(-.1)
	in.Quality.FreeCashflow = ptr(0)
	last := &in.Weekly.Candles[len(in.Weekly.Candles)-1]
	last.Open = 80
	last.Close = 80
	last.High = 81
	last.Low = 79
	for i := range in.Daily.Candles {
		v := 105.0
		if i%2 == 1 {
			v = 95
		}
		c := &in.Daily.Candles[i]
		c.Open = v
		c.Close = v
		c.High = v + 1
		c.Low = v - 1
	}
	e := Evaluate(in, testNow)
	if e.Score >= 1 || e.Signal != "ACCUMULATE" || !strings.Contains(strings.Join(e.Warnings, " "), "Accumulation contre-tendance") {
		t.Fatalf("countertrend: %+v", e)
	}
	in.Quality.ProfitMargin = ptr(0)
	if e = Evaluate(in, testNow); e.Signal == "ACCUMULATE" {
		t.Fatalf("weak quality still accumulates: %+v", e)
	}
}

func TestProvisionalSessionTimezone(t *testing.T) {
	in := fixture()
	in.Daily.Candles[len(in.Daily.Candles)-1].Time = testNow.Add(-time.Hour).Unix()
	in.Daily.SessionEnd = testNow.Add(time.Hour)
	if !Evaluate(in, testNow).Provisional {
		t.Fatal("ongoing daily period accepted as closed")
	}
	in.Daily.SessionEnd = testNow.Add(-time.Minute)
	if Evaluate(in, testNow).Provisional {
		t.Fatal("closed daily period is provisional")
	}
	in.Daily.Timezone = ""
	if !Evaluate(in, testNow).Provisional {
		t.Fatal("missing timezone must be provisional")
	}
}
