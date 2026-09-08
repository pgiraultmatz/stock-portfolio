package marketdigest

import (
	"math"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/config"
	"stock-portfolio/internal/models"
)

func TestFinancialSectionMissingNegativeAndNonEquity(t *testing.T) {
	zero, negative := 0.0, -2e6
	options := RankingOptions{Now: rankingNow, Financials: map[string]FinancialContext{
		"EQUITY": {Data: config.StockFundamentals{PEGRatio: 1.7, FinancialQuality: &models.FinancialQuality{QuoteType: "EQUITY", Currency: "USD", CollectedAt: rankingNow, FreeCashflow: &negative, TotalDebt: &zero}}, ValuationAt: rankingNow.Add(-48 * time.Hour), RefreshFailed: true},
		"ETF":    {Data: config.StockFundamentals{PEGRatio: 99, FinancialQuality: &models.FinancialQuality{QuoteType: "ETF"}}},
	}}
	var sb strings.Builder
	writeFinancialRows(&sb, []topPick{{ticker: "EQUITY", name: "<script>"}, {ticker: "MISSING"}, {ticker: "ETF"}}, options)
	out := sb.String()
	for _, want := range []string{"PEG : 1.70", "Marge nette : N/D", "-2.00 M USD", "Dette totale : 0.00 M USD", "Cash-flow libre négatif", "(ancienne)", "Actualisation indisponible", "&lt;script&gt;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "99.00") || strings.Contains(out, "<script>") || strings.Contains(out, "ETF") || strings.Contains(out, "MISSING") {
		t.Fatal("non-equity valuation or unsafe HTML")
	}
	if ratio(math.NaN()) != "N/D" || ratio(-1) != "N/D" || percent(nil) != "N/D" || amount(&zero, "") != "N/D" {
		t.Fatal("invalid values rendered")
	}
}

func financeFixture(ticker string, peg float64) FinancialContext {
	margin, growth, ocf, fcf, debt, cash := 0.2, 0.1, 10e6, 5e6, 20e6, 10e6
	return FinancialContext{Stock: models.Stock{Ticker: ticker, Name: ticker}, ValuationAt: rankingNow, Data: config.StockFundamentals{PEGRatio: peg, FinancialQuality: &models.FinancialQuality{CollectedAt: rankingNow, QuoteType: "EQUITY", Currency: "USD", ProfitMargin: &margin, RevenueGrowth: &growth, OperatingCashflow: &ocf, FreeCashflow: &fcf, TotalDebt: &debt, TotalCash: &cash}}}
}

func TestIndependentFinancialRankingWithoutTechnicalSignals(t *testing.T) {
	options := RankingOptions{Now: rankingNow, Financials: map[string]FinancialContext{"VALUE": financeFixture("VALUE", 1.0)}}
	out := GenerateMultiTimeframeReportWithChanges(nil, nil, nil, nil, 1.5, 3, nil, nil, options)
	_, financial, ok := strings.Cut(out, "Top 3 valorisation")
	if !ok || !strings.Contains(financial, "<strong>VALUE</strong>") || !strings.Contains(out, "Aucune configuration suffisamment convergente") {
		t.Fatal("financial ranking still depends on technical signals")
	}
}

func TestFinancialEligibilityExcludesCryptoMissingStaleAndNegative(t *testing.T) {
	options := RankingOptions{Now: rankingNow, Financials: make(map[string]FinancialContext)}
	for _, ticker := range []string{"GOOD", "CRYPTO", "ETF", "UNKNOWN", "STALE", "FUTURE", "NO_PEG", "NO_DEBT", "NEGATIVE"} {
		f := financeFixture(ticker, 1)
		switch ticker {
		case "CRYPTO":
			f.Data.FinancialQuality.QuoteType = "CRYPTOCURRENCY"
		case "ETF":
			f.Data.FinancialQuality.QuoteType = "ETF"
		case "UNKNOWN":
			f.Data.FinancialQuality.QuoteType = ""
		case "STALE":
			f.ValuationAt = rankingNow.Add(-25 * time.Hour)
		case "FUTURE":
			f.Data.FinancialQuality.CollectedAt = rankingNow.Add(time.Hour)
		case "NO_PEG":
			f.Data.PEGRatio = 0
		case "NO_DEBT":
			f.Data.FinancialQuality.TotalDebt = nil
		case "NEGATIVE":
			*f.Data.FinancialQuality.FreeCashflow = -1
		}
		options.Financials[ticker] = f
	}
	picks, coverage := rankFinancials(options)
	if len(picks) != 1 || picks[0].ticker != "GOOD" || coverage.equities != 6 || coverage.eligible != 1 {
		t.Fatalf("wrong eligible universe: %+v %+v", picks, coverage)
	}
	var sb strings.Builder
	writeFinancialSection(&sb, options)
	for _, excluded := range []string{"CRYPTO", "ETF", "UNKNOWN", "NEGATIVE"} {
		if strings.Contains(sb.String(), excluded) {
			t.Errorf("excluded ticker shown: %s", excluded)
		}
	}
}

func TestFinancialRankingWeightsAndLimits(t *testing.T) {
	options := RankingOptions{Now: rankingNow, Limit: 5, Financials: make(map[string]FinancialContext)}
	for i, ticker := range []string{"A", "B", "C", "D", "E", "F"} {
		options.Financials[ticker] = financeFixture(ticker, float64(i+1))
	}
	picks, _ := rankFinancials(options)
	if len(picks) != 5 || picks[0].ticker != "A" || picks[4].ticker != "E" {
		t.Fatalf("PEG ordering / top5: %+v", picks)
	}
	f := options.Financials["F"]
	*f.Data.FinancialQuality.ProfitMargin = 0.9
	*f.Data.FinancialQuality.RevenueGrowth = 0.9
	*f.Data.FinancialQuality.TotalDebt = 0
	options.Financials["F"] = f
	options.Limit = 3
	picks, _ = rankFinancials(options)
	if len(picks) != 3 || picks[1].ticker != "F" {
		t.Fatalf("quality did not influence independent ranking: %+v", picks)
	}
}

func TestFinancialContextDoesNotChangeRanking(t *testing.T) {
	groups := []multiTickerGroup{rankedGroup("TEST", "bullish")}
	before, _ := rankDecisions(groups, RankingOptions{Now: rankingNow})
	after, _ := rankDecisions(groups, RankingOptions{Now: rankingNow, Financials: map[string]FinancialContext{"TEST": {Data: config.StockFundamentals{PEGRatio: 99}}}})
	if len(before) != 1 || len(after) != 1 || before[0].score != after[0].score {
		t.Fatal("financial context changed technical ranking")
	}
}
