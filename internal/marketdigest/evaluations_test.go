package marketdigest

import (
	"fmt"
	"strings"
	"testing"

	"stock-portfolio/internal/models"
)

func TestSharedRankingsIncludeWatchlistAndLimitEligibleCandidates(t *testing.T) {
	watch := false
	o := RankingOptions{Evaluations: map[string]models.Evaluation{}}
	for i := 0; i < 13; i++ {
		ticker := fmt.Sprintf("BUY%02d", i)
		o.Stocks = append(o.Stocks, models.Stock{Ticker: ticker, Name: "<script>", InPortfolio: &watch})
		o.Evaluations[ticker] = models.Evaluation{Version: models.EvaluationVersion, Eligible: true, QuoteType: "ETF", Signal: "BUY", Score: 6, EvaluatedAt: rankingNow}
	}
	o.Stocks = append(o.Stocks, models.Stock{Ticker: "SELL", InPortfolio: &watch}, models.Stock{Ticker: "MISSING"}, models.Stock{Ticker: "HOLD"})
	o.Evaluations["SELL"] = models.Evaluation{Version: models.EvaluationVersion, Eligible: true, Signal: "SELL", Score: -6, EvaluatedAt: rankingNow}
	o.Evaluations["HOLD"] = models.Evaluation{Version: models.EvaluationVersion, Eligible: true, Signal: "HOLD", EvaluatedAt: rankingNow}
	buys, sells, excluded := rankEvaluations(o)
	if len(buys) != 10 || buys[0].stock.Ticker != "BUY00" || buys[9].stock.Ticker != "BUY09" || len(sells) != 1 || len(excluded) != 1 {
		t.Fatalf("buys=%+v sells=%+v excluded=%+v", buys, sells, excluded)
	}
	var sb strings.Builder
	writeEvaluationTopSection(&sb, o)
	content := sb.String()
	for _, want := range []string{"Top 10 achats", "À éviter", "Achat à étudier", "&lt;script&gt;"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(content, "<script>") || strings.Contains(content, "BUY10") {
		t.Fatal("unsafe or over-limit output")
	}
	if strings.Contains(content, "Couverture :") || strings.Contains(content, "MISSING") {
		t.Fatal("excluded-stock diagnostics should not be displayed")
	}
	for _, limit := range []int{3, 5, 10} {
		o.Limit = limit
		b, _, _ := rankEvaluations(o)
		if len(b) != limit {
			t.Fatalf("limit %d: %d", limit, len(b))
		}
	}
}

func TestBadgeSafeguardsControlPriorityBeforeScore(t *testing.T) {
	o := RankingOptions{Stocks: []models.Stock{{Ticker: "HIGH_SCORE"}, {Ticker: "CONFIRMED"}, {Ticker: "COUNTER"}}, Evaluations: map[string]models.Evaluation{
		"HIGH_SCORE": {Version: models.EvaluationVersion, Eligible: true, Signal: "BUY", Score: 10},
		"CONFIRMED":  {Version: models.EvaluationVersion, Eligible: true, Signal: "STRONG BUY", Score: 7},
		"COUNTER":    {Version: models.EvaluationVersion, Eligible: true, Signal: "ACCUMULATE", Score: -.5},
	}}
	b, s, _ := rankEvaluations(o)
	if len(b) != 3 || len(s) != 0 || b[0].stock.Ticker != "CONFIRMED" || b[2].stock.Ticker != "COUNTER" {
		t.Fatalf("badge ordering: %+v %+v", b, s)
	}
	// Shared evaluations are rendered even when no new technical alert fired.
	content := GenerateMultiTimeframeReportWithChanges(nil, nil, nil, nil, 1.5, 3, nil, nil, o)
	if !strings.Contains(content, "CONFIRMED — STRONG BUY") {
		t.Fatal("alert-free candidate disappeared")
	}
}

func TestReversalsKeepHoldSignalAndRequireStrongDailyRecovery(t *testing.T) {
	o := RankingOptions{Evaluations: map[string]models.Evaluation{}}
	add := func(ticker string, e models.Evaluation) {
		o.Stocks = append(o.Stocks, models.Stock{Ticker: ticker})
		o.Evaluations[ticker] = e
	}
	nee := models.Evaluation{Version: models.EvaluationVersion, Eligible: true, Signal: "HOLD", Weekly: -3, Daily: 2, Score: 0}
	add("NEE", nee)
	for _, name := range []string{"BUY", "SELL", "WEAK_DAILY", "POSITIVE_WEEKLY", "INVALID", "OLD"} {
		e := nee
		switch name {
		case "BUY", "SELL":
			e.Signal = name
		case "WEAK_DAILY":
			e.Daily = 1
		case "POSITIVE_WEEKLY":
			e.Weekly = 1
		case "INVALID":
			e.Eligible = false
		case "OLD":
			e.Version = "old"
		}
		add(name, e)
	}
	o.Stocks = append(o.Stocks, models.Stock{Ticker: "NEE"}, models.Stock{Ticker: "MISSING"})
	picks := rankReversals(o)
	if len(picks) != 1 || picks[0].stock.Ticker != "NEE" || picks[0].evaluation.Signal != "HOLD" {
		t.Fatalf("unexpected reversal picks: %+v", picks)
	}
	content := GenerateMultiTimeframeReportWithChanges(nil, nil, nil, nil, 1.5, 3, nil, nil, o)
	if !strings.Contains(content, "Retournements à surveiller") || strings.Count(content, "NEE — HOLD") != 1 || !strings.Contains(content, "Rebond à confirmer") {
		t.Fatal("missing or duplicated rebound candidate")
	}
	for i := 0; i < 7; i++ {
		e := nee
		e.Score = .5
		add(fmt.Sprintf("R%d", i), e)
	}
	for _, limit := range []int{3, 5, 10} {
		o.Limit = limit
		picks = rankReversals(o)
		if len(picks) != min(5, limit) || picks[0].stock.Ticker != "R0" {
			t.Fatalf("unexpected limited order: %+v", picks)
		}
	}
}

func TestCompactCardsHighlightEarningsBesideAction(t *testing.T) {
	target, debt := 630.0, 2.45
	e := models.Evaluation{Version: models.EvaluationVersion, Eligible: true, Signal: "BUY", Score: 5, Weekly: 1, Daily: 2, Price: 552.35, EvaluatedAt: rankingNow, EarningsAt: &rankingNow, AnalystTarget: &target, NetDebtToCashflow: &debt, Reasons: []string{"Verbose structure and momentum details"}, Warnings: []string{"Provisoire", "WAIT EARNINGS", "<unsafe>"}}
	watch := false
	var sb strings.Builder
	writeEvaluationList(&sb, []evaluationPick{{stock: models.Stock{Ticker: "TEST", InPortfolio: &watch}, evaluation: e}}, true)
	content := sb.String()
	action := strings.Split(strings.Split(content, `<div class="pick-action">`)[1], "</div>")[0]
	if !strings.Contains(action, "Achat à étudier") || !strings.Contains(action, `class="earnings-warning"`) || !strings.Contains(action, "WAIT EARNINGS") || !strings.Contains(action, rankingNow.Format("02/01")) {
		t.Fatalf("earnings not beside action: %s", action)
	}
	for _, removed := range []string{"Calculé le", "Objectif analystes", "Dette nette", "Verbose structure", "<unsafe>"} {
		if strings.Contains(content, removed) {
			t.Errorf("unexpected content: %s", removed)
		}
	}
	if strings.Count(content, "WAIT EARNINGS") != 1 || !strings.Contains(content, "Provisoire") || !strings.Contains(content, "&lt;unsafe&gt;") {
		t.Fatal("warnings duplicated, lost or unescaped")
	}
}

func TestPullbackRankingIsIndependentAndDoesNotFillWithInvalidCandidates(t *testing.T) {
	f := 3.0
	o := RankingOptions{Evaluations: map[string]models.Evaluation{}}
	for i := 0; i < 17; i++ {
		ticker := fmt.Sprintf("DIP%02d", i)
		o.Stocks = append(o.Stocks, models.Stock{Ticker: ticker})
		o.Evaluations[ticker] = models.Evaluation{Version: models.EvaluationVersion, Eligible: true, QuoteType: "EQUITY", Fundamental: &f, Signal: "LIGHT TRIM", Score: -2, Pullback: &models.PullbackEvaluation{Eligible: true, Drawdown20: 20}}
	}
	for _, ticker := range []string{"OLD", "MISSING", "INELIGIBLE", "ETF", "NO_FUNDAMENTALS", "FAILED_SCREEN"} {
		e := o.Evaluations["DIP00"]
		switch ticker {
		case "OLD":
			e.Version = "old"
		case "MISSING":
			e.Pullback = nil
		case "INELIGIBLE":
			e.Eligible = false
		case "ETF":
			e.QuoteType = "ETF"
		case "NO_FUNDAMENTALS":
			e.Fundamental = nil
		case "FAILED_SCREEN":
			e.Pullback = &models.PullbackEvaluation{Eligible: false}
		}
		o.Stocks = append(o.Stocks, models.Stock{Ticker: ticker})
		o.Evaluations[ticker] = e
	}
	o.Stocks = append(o.Stocks, models.Stock{Ticker: "DIP00"})
	for _, limit := range []int{3, 5, 10} {
		o.Limit = limit
		picks := rankPullbacks(o)
		if len(picks) != 15 || picks[0].stock.Ticker != "DIP00" || picks[len(picks)-1].stock.Ticker != "DIP14" {
			t.Fatalf("unexpected picks: %+v", picks)
		}
	}
	e := o.Evaluations["DIP11"]
	e.Pullback = &models.PullbackEvaluation{Eligible: true, Drawdown20: 33.6, NetLoss: true}
	o.Evaluations["DIP11"] = e
	if rankPullbacks(o)[0].stock.Ticker != "DIP11" {
		t.Fatal("largest qualifying drawdown must take priority")
	}
	content := GenerateMultiTimeframeReportWithChanges(nil, nil, nil, nil, 1.5, 3, nil, nil, o)
	if !strings.Contains(content, "Opportunités en repli") || !strings.Contains(content, "Entrée anticipée · risque élevé") || !strings.Contains(content, "Repli −20.0%") || !strings.Contains(content, "DIP11 — LIGHT TRIM") || !strings.Contains(content, "Résultat net négatif — à examiner") {
		t.Fatal("missing pullback context or changed signal")
	}
}
