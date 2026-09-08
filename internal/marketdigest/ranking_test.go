package marketdigest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/chartcalc"
	"stock-portfolio/internal/divergencealerts"
	"stock-portfolio/internal/models"
	"stock-portfolio/internal/technicalalerts"
)

var rankingNow = time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC)

func rankedGroup(ticker, bias string) multiTickerGroup {
	structure, momentum := technicalalerts.EMAReclaim, technicalalerts.MACDBullish
	if bias == "bearish" {
		structure, momentum = technicalalerts.EMALoss, technicalalerts.MACDBearish
	}
	return multiTickerGroup{Ticker: ticker, Name: ticker, Daily: timeframeSignals{Technical: []technicalalerts.Alert{
		{Kind: structure, Bias: bias, Label: string(structure), CandleTime: rankingNow.Add(-time.Hour).Unix()},
		{Kind: momentum, Bias: bias, Label: string(momentum), CandleTime: rankingNow.Add(-time.Hour).Unix()},
	}}}
}

func TestRankingRequiresIndependentFamilies(t *testing.T) {
	g := rankedGroup("EMA", "bullish")
	g.Daily.Technical = []technicalalerts.Alert{g.Daily.Technical[0], g.Daily.Technical[0], g.Daily.Technical[0]}
	buys, sells := rankDecisions([]multiTickerGroup{g}, RankingOptions{Now: rankingNow})
	if len(buys) != 0 || len(sells) != 0 {
		t.Fatal("EMA signals alone must not qualify")
	}
	g = rankedGroup("GOOD", "bullish")
	buys, _ = rankDecisions([]multiTickerGroup{g}, RankingOptions{Now: rankingNow})
	score := buys[0].score
	g.Daily.Technical = append(g.Daily.Technical, g.Daily.Technical[0], g.Daily.Technical[1])
	again, _ := rankDecisions([]multiTickerGroup{g}, RankingOptions{Now: rankingNow})
	if again[0].score != score || len(again[0].signals) != 2 {
		t.Fatal("correlated duplicates inflated ranking")
	}
}

func TestRankingPrefersAlignmentAndFreshness(t *testing.T) {
	old := rankedGroup("OLD", "bullish")
	for i := range old.Daily.Technical {
		old.Daily.Technical[i].CandleTime = rankingNow.Add(-72 * time.Hour).Unix()
	}
	recent := rankedGroup("RECENT", "bullish")
	aligned := old
	aligned.Ticker = "ALIGNED"
	aligned.Weekly = old.Daily
	buys, _ := rankDecisions([]multiTickerGroup{old, recent, aligned}, RankingOptions{Now: rankingNow})
	if len(buys) != 3 || buys[0].ticker != "ALIGNED" || buys[1].ticker != "RECENT" {
		t.Fatalf("unexpected ranking: %#v", buys)
	}
}

func TestRankingRejectsStaleUnknownFutureAndConflictingSignals(t *testing.T) {
	for _, age := range []time.Duration{-8 * 24 * time.Hour, time.Hour} {
		g := rankedGroup("STALE", "bullish")
		for i := range g.Daily.Technical {
			g.Daily.Technical[i].CandleTime = rankingNow.Add(age).Unix()
		}
		buys, _ := rankDecisions([]multiTickerGroup{g}, RankingOptions{Now: rankingNow})
		if len(buys) != 0 {
			t.Fatal("stale/future signals qualified")
		}
	}
	g := rankedGroup("UNKNOWN", "bullish")
	for i := range g.Daily.Technical {
		g.Daily.Technical[i].CandleTime = 0
	}
	buys, _ := rankDecisions([]multiTickerGroup{g}, RankingOptions{Now: rankingNow})
	if len(buys) != 0 {
		t.Fatal("undated signals qualified")
	}
	g = rankedGroup("MIXED", "bullish")
	g.Weekly = rankedGroup("MIXED", "bearish").Daily
	buys, sells := rankDecisions([]multiTickerGroup{g}, RankingOptions{Now: rankingNow})
	if len(buys) != 0 || len(sells) != 0 {
		t.Fatal("contradictory daily/weekly setup qualified")
	}
}

func TestRankingFiltersSalesBeforeLimitingAndLabelsEntries(t *testing.T) {
	watch := false
	var groups []multiTickerGroup
	for i := 0; i < 7; i++ {
		g := rankedGroup(fmt.Sprintf("SELL%d", i), "bearish")
		if i < 2 {
			g.InPortfolio = &watch
		}
		groups = append(groups, g)
	}
	for _, limit := range []int{3, 5} {
		_, sells := rankDecisions(groups, RankingOptions{Now: rankingNow, Limit: limit})
		if len(sells) != limit || sells[0].ticker != "SELL2" {
			t.Fatalf("wrong eligible sale list: %#v", sells)
		}
	}
	entry := rankedGroup("ENTRY", "bullish")
	entry.InPortfolio = &watch
	held := rankedGroup("HELD", "bullish")
	buys, _ := rankDecisions([]multiTickerGroup{entry, held}, RankingOptions{Now: rankingNow, Limit: 5})
	if len(buys) != 2 || buys[0].action != "Achat à étudier" || buys[1].action != "Renforcement à étudier" {
		t.Fatalf("wrong entry actions: %#v", buys)
	}
}

func TestRankingDistinguishesExhaustionFromProtection(t *testing.T) {
	g := rankedGroup("TRIM", "bullish")
	g.Daily.Technical = g.Daily.Technical[:1]
	g.Daily.Divergences = []divergencealerts.Alert{{Divergence: chartcalc.Divergence{Kind: "bearish", FromRSI: 78, ToTime: rankingNow.Add(-time.Hour).Unix()}}}
	buys, sells := rankDecisions([]multiTickerGroup{g}, RankingOptions{Now: rankingNow})
	if len(buys) != 0 || len(sells) != 1 || sells[0].action != "Allègement à étudier" {
		t.Fatalf("expected exhaustion warning: %#v %#v", buys, sells)
	}
	g.Daily.Divergences[0].Divergence.FromRSI = 55
	_, sells = rankDecisions([]multiTickerGroup{g}, RankingOptions{Now: rankingNow})
	if len(sells) != 0 {
		t.Fatal("divergence without overbought context became a trim")
	}
}

func TestWeeklyRankingAndEscapedOutput(t *testing.T) {
	g := rankedGroup("<ticker>", "bullish")
	watch := false
	for i := range g.Daily.Technical {
		g.Daily.Technical[i].CandleTime = rankingNow.Add(-10 * 24 * time.Hour).Unix()
		g.Daily.Technical[i].Stock = models.Stock{Ticker: g.Ticker, InPortfolio: &watch}
	}
	report := GenerateReportWithCategoryOrder(nil, g.Daily.Technical, "weekly", 1.5, nil, RankingOptions{Limit: 5, Now: rankingNow})
	top := strings.Split(strings.Split(report, `<table class="top-picks">`)[1], "</table>")[0]
	if !strings.Contains(top, "Top 5 achats") || !strings.Contains(top, "W: ") || !strings.Contains(top, "&lt;ticker&gt;") {
		t.Fatalf("bad weekly top: %s", top)
	}
	if strings.Contains(top, "<ticker>") {
		t.Fatal("unescaped ranking output")
	}
}
