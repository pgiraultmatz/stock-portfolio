package barometer

import (
	"math"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/yahoo"
)

func testTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func testQuotes(now time.Time, es, nq, vix, oil, bps float64) map[string]yahoo.MarketQuote {
	quotes := make(map[string]yahoo.MarketQuote)
	for ticker, change := range map[string]float64{"ES=F": es, "NQ=F": nq, "^VIX": vix, "BZ=F": oil} {
		base := 100.0
		if ticker == "^VIX" {
			base = 18
		}
		quotes[ticker] = yahoo.MarketQuote{Price: base * (1 + change/100), PreviousClose: base, AsOf: now.Add(-20 * time.Minute)}
	}
	quotes["^TNX"] = yahoo.MarketQuote{Price: 4 + bps/100, PreviousClose: 4, AsOf: now.Add(-20 * time.Minute)}
	return quotes
}

func TestEvaluateLabels(t *testing.T) {
	now := testTime("2026-09-10T10:00:00-04:00")
	for _, tc := range []struct {
		name                  string
		es, nq, vix, oil, bps float64
		label                 string
	}{
		{"favorable", .4, .5, -4, 0, 0, "Favorable"},
		{"moderate", .15, .15, 0, 0, 0, "Assez favorable"},
		{"exact threshold", .1, .1, 0, 0, 0, "Assez favorable"},
		{"mixed", 0, 0, 0, 0, 0, "Mitigé"},
		{"moderate negative", -.15, -.15, 0, 0, 0, "Assez défavorable"},
		{"negative", -.4, -.5, 4, 0, 0, "Défavorable"},
		{"lower oil and rates are not always bullish", 0, 0, 0, -5, -10, "Mitigé"},
		{"lower oil and rates confirm rising futures", .15, .15, 0, -2, -5, "Favorable"},
		{"higher oil and rates offset futures", .15, .15, 0, 2, 5, "Mitigé"},
		{"VIX alone cannot imply favorable", 0, 0, -12, 0, 0, "Mitigé"},
		{"conflicting futures", .4, -.15, -5, 0, 0, "Mitigé"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Evaluate(testQuotes(now, tc.es, tc.nq, tc.vix, tc.oil, tc.bps), now)
			if s.Label != tc.label {
				t.Fatalf("got %s, want %s", s.Label, tc.label)
			}
		})
	}
}

func TestMissingInputsAndStress(t *testing.T) {
	now := testTime("2026-09-10T10:00:00-04:00")
	for _, ticker := range []string{"ES=F", "NQ=F", "^VIX"} {
		q := testQuotes(now, .4, .5, -5, 0, 0)
		delete(q, ticker)
		if s := Evaluate(q, now); s.Label != "Indisponible" {
			t.Errorf("missing %s: %s", ticker, s.Label)
		}
	}
	q := testQuotes(now, .4, .5, -5, 0, 0)
	delete(q, "BZ=F")
	s := Evaluate(q, now)
	if s.Label != "Assez favorable" || !strings.Contains(s.Coverage, "partielle") {
		t.Fatalf("partial: %+v", s)
	}
	q = testQuotes(now, .4, .5, -5, 0, 0)
	q["^VIX"] = yahoo.MarketQuote{Price: 32, PreviousClose: 40, AsOf: now}
	if s := Evaluate(q, now); s.Label == "Favorable" {
		t.Fatal("high VIX must limit favorable label")
	}
	if s := Evaluate(nil, now); s.Label != "Indisponible" || len(s.Factors) != 5 {
		t.Fatal("missing input handling")
	}
}

func TestQuoteFreshness(t *testing.T) {
	for _, tc := range []struct {
		name, now, asof, ticker string
		want                    bool
	}{
		{"recent futures", "2026-09-10T08:00:00-04:00", "2026-09-10T07:40:00-04:00", "ES=F", true},
		{"stale futures", "2026-09-10T08:00:00-04:00", "2026-09-09T16:00:00-04:00", "ES=F", false},
		{"overnight futures", "2026-09-10T00:10:00-04:00", "2026-09-09T23:50:00-04:00", "ES=F", true},
		{"prior VIX before open", "2026-09-10T08:00:00-04:00", "2026-09-09T16:15:00-04:00", "^VIX", true},
		{"prior TNX last tick before 15", "2026-09-10T08:00:00-04:00", "2026-09-09T14:59:55-04:00", "^TNX", true},
		{"TNX close after hours", "2026-09-10T21:00:00-04:00", "2026-09-10T14:59:55-04:00", "^TNX", true},
		{"prior TNX too early", "2026-09-10T08:00:00-04:00", "2026-09-09T12:00:00-04:00", "^TNX", false},
		{"prior VIX during session", "2026-09-10T10:00:00-04:00", "2026-09-09T16:15:00-04:00", "^VIX", false},
		{"stalled VIX", "2026-09-10T14:00:00-04:00", "2026-09-10T10:00:00-04:00", "^VIX", false},
		{"closing VIX", "2026-09-10T21:00:00-04:00", "2026-09-10T16:15:00-04:00", "^VIX", true},
		{"Monday prior Friday", "2026-09-14T08:00:00-04:00", "2026-09-11T16:15:00-04:00", "^VIX", true},
		{"weekend", "2026-09-12T08:00:00-04:00", "2026-09-11T16:15:00-04:00", "^VIX", false},
		{"future timestamp", "2026-09-10T08:00:00-04:00", "2026-09-10T09:00:00-04:00", "ES=F", false},
		{"DST Monday", "2026-11-02T08:00:00-05:00", "2026-10-30T16:15:00-04:00", "^TNX", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loc, _ := time.LoadLocation("America/New_York")
			q := yahoo.MarketQuote{Price: 10, PreviousClose: 10, AsOf: testTime(tc.asof)}
			if got := usable(q, tc.ticker, testTime(tc.now).In(loc)); got != tc.want {
				t.Fatalf("got %v", got)
			}
		})
	}
	now := testTime("2026-09-10T10:00:00-04:00")
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if usable(yahoo.MarketQuote{Price: bad, PreviousClose: 10, AsOf: now}, "^VIX", now) {
			t.Fatal("invalid price accepted")
		}
		if usable(yahoo.MarketQuote{Price: 10, PreviousClose: bad, AsOf: now}, "^VIX", now) {
			t.Fatal("invalid reference accepted")
		}
	}
}

func TestTreasuryBasisPointsAndContext(t *testing.T) {
	now := testTime("2026-09-10T21:00:00-04:00")
	s := Evaluate(testQuotes(now, .15, .15, 0, 0, 5), now)
	if s.Factors[4].Value != "4.05% (+5.0 pb)" || s.Factors[4].Class != "negative" {
		t.Fatalf("wrong rate units: %+v", s.Factors[4])
	}
	if !strings.Contains(s.Context, "Hors séance") {
		t.Fatal("missing closed-session context")
	}
}
