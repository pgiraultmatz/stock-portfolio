package marketdigest

import (
	"fmt"
	"html"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"stock-portfolio/internal/config"
	"stock-portfolio/internal/models"
)

type FinancialContext struct {
	Stock         models.Stock
	Data          config.StockFundamentals
	ValuationAt   time.Time
	RefreshFailed bool
}

type financialCoverage struct{ equities, complete, eligible, unknown int }

type financialCandidate struct {
	pick                          topPick
	peg, margin, growth, leverage float64
}

func financialDateRecent(at, now time.Time) bool {
	return !at.IsZero() && !at.After(now) && now.Sub(at) <= 24*time.Hour
}

// Relative ranks, not probabilities or fair-value estimates. PEG contributes
// 40%, net margin 20%, revenue growth 20%, net debt/operating cash flow 20%.
// Positive earnings, growth and cash generation are prerequisites, not points.
func rankFinancials(options RankingOptions) ([]topPick, financialCoverage) {
	options = rankingOptions([]RankingOptions{options})
	now := options.Now
	if !options.FinancialsAt.IsZero() {
		now = options.FinancialsAt
	}
	var coverage financialCoverage
	var candidates []financialCandidate
	for ticker, f := range options.Financials {
		q := f.Data.FinancialQuality
		if q == nil || q.QuoteType == "" {
			coverage.unknown++
			continue
		}
		if q.QuoteType != "EQUITY" {
			continue
		}
		coverage.equities++
		if !financialDateRecent(f.ValuationAt, now) || !financialDateRecent(q.CollectedAt, now) ||
			!validNumber(&f.Data.PEGRatio) || f.Data.PEGRatio <= 0 || !validNumber(q.ProfitMargin) || !validNumber(q.RevenueGrowth) ||
			!validNumber(q.FreeCashflow) || !validNumber(q.OperatingCashflow) ||
			!validNumber(q.TotalDebt) || !validNumber(q.TotalCash) || *q.TotalDebt < 0 || *q.TotalCash < 0 || q.Currency == "" {
			continue
		}
		coverage.complete++
		if *q.ProfitMargin <= 0 || *q.RevenueGrowth <= 0 || *q.FreeCashflow <= 0 || *q.OperatingCashflow <= 0 {
			continue
		}
		leverage := math.Max(0, *q.TotalDebt-*q.TotalCash) / *q.OperatingCashflow
		if !validNumber(&leverage) {
			continue
		}
		candidates = append(candidates, financialCandidate{pick: topPick{ticker: ticker, name: f.Stock.Name}, peg: f.Data.PEGRatio, margin: *q.ProfitMargin, growth: *q.RevenueGrowth, leverage: leverage})
	}
	coverage.eligible = len(candidates)
	for i := range candidates {
		c := &candidates[i]
		score := 0.0
		for j := range candidates {
			if i == j {
				continue
			}
			o := candidates[j]
			score += 40*relativePoint(o.peg, c.peg) + 20*relativePoint(c.margin, o.margin) + 20*relativePoint(c.growth, o.growth) + 20*relativePoint(o.leverage, c.leverage)
		}
		c.pick.score = int(math.Round(1000 * score / float64(max(1, len(candidates)-1))))
		c.pick.signals = []string{fmt.Sprintf("Dette nette / cash-flow opérationnel : %.2fx", c.leverage)}
		if c.leverage == 0 {
			c.pick.signals = []string{"Trésorerie au moins égale à la dette"}
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].pick.score != candidates[j].pick.score {
			return candidates[i].pick.score > candidates[j].pick.score
		}
		if candidates[i].peg != candidates[j].peg {
			return candidates[i].peg < candidates[j].peg
		}
		return candidates[i].pick.ticker < candidates[j].pick.ticker
	})
	var picks []topPick
	for _, c := range candidates[:min(options.Limit, len(candidates))] {
		picks = append(picks, c.pick)
	}
	return picks, coverage
}

func relativePoint(a, b float64) float64 {
	if a > b {
		return 1
	}
	if a == b {
		return 0.5
	}
	return 0
}

func writeFinancialSection(sb *strings.Builder, options RankingOptions) {
	picks, coverage := rankFinancials(options)
	fmt.Fprintf(sb, "<h3>Top %d valorisation / qualité financière</h3>\n", options.Limit)
	fmt.Fprintf(sb, `<p class="muted">Actions évaluables : %d / %d · Configurations admissibles : %d · Données insuffisantes ou anciennes : %d</p>`, coverage.complete, coverage.equities, coverage.eligible, coverage.unknown+coverage.equities-coverage.complete)
	if len(picks) == 0 {
		sb.WriteString(`<p class="muted">Aucune action ne remplit les critères financiers avec des données suffisamment récentes.</p>`)
		return
	}
	writeFinancialRows(sb, picks, options)
}

func validNumber(v *float64) bool { return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) }

func ratio(v float64) string {
	if !validNumber(&v) || v <= 0 {
		return "N/D"
	}
	return fmt.Sprintf("%.2f", v)
}

func percent(v *float64) string {
	if !validNumber(v) {
		return "N/D"
	}
	return fmt.Sprintf("%.1f %%", *v*100)
}

func amount(v *float64, currency string) string {
	if !validNumber(v) || currency == "" {
		return "N/D"
	}
	if math.Abs(*v) >= 1e9 {
		return fmt.Sprintf("%.2f Md %s", *v/1e9, html.EscapeString(currency))
	}
	return fmt.Sprintf("%.2f M %s", *v/1e6, html.EscapeString(currency))
}

func collectionDate(at, now time.Time) string {
	if at.IsZero() || at.After(now) {
		return "Date de collecte inconnue"
	}
	label := "Collecte : " + at.UTC().Format("02/01/2006 15:04 UTC")
	if now.Sub(at) > 24*time.Hour {
		label += " (ancienne)"
	}
	return label
}

func writeFinancialRows(sb *strings.Builder, picks []topPick, options RankingOptions) {
	if !options.FinancialsAt.IsZero() {
		options.Now = options.FinancialsAt
	}
	if len(picks) == 0 {
		return
	}
	sb.WriteString(`<table class="financial-table"><tr><th>Titre</th><th>Valorisation</th><th>Qualité financière</th></tr>`)
	for _, pick := range picks {
		f := options.Financials[pick.ticker]
		q := f.Data.FinancialQuality
		if q == nil || q.QuoteType != "EQUITY" {
			continue
		}
		fmt.Fprintf(sb, `<tr><td><strong>%s</strong><br><span class="muted">%s</span><br><a href="https://finance.yahoo.com/quote/%s/key-statistics/">Yahoo Finance</a></td>`, html.EscapeString(pick.ticker), html.EscapeString(pick.name), html.EscapeString(url.PathEscape(pick.ticker)))
		fmt.Fprintf(sb, `<td>PEG : %s<br>PSG : %s<br>EV / bénéfice brut : %s`, ratio(f.Data.PEGRatio), ratio(f.Data.PSGRatio), ratio(f.Data.EVGrossProfit))
		fmt.Fprintf(sb, `<br><span class="muted">%s</span>`, collectionDate(f.ValuationAt, options.Now))
		if q != nil {
			trailing, forward := "N/D", "N/D"
			if q.TrailingPE != nil {
				trailing = ratio(*q.TrailingPE)
			}
			if q.ForwardPE != nil {
				forward = ratio(*q.ForwardPE)
			}
			fmt.Fprintf(sb, "<br>PER passé / prévisionnel : %s / %s", trailing, forward)
			fmt.Fprintf(sb, `<br><span class="muted">%s</span>`, collectionDate(q.CollectedAt, options.Now))
		}
		sb.WriteString("</td><td>")
		if q == nil {
			sb.WriteString(`<span class="muted">Données financières indisponibles.</span>`)
		} else {
			fmt.Fprintf(sb, "Marge nette : %s<br>Marge opérationnelle : %s<br>Croissance du CA : %s<br>Cash-flow opérationnel : %s<br>Cash-flow libre : %s<br>Dette totale : %s<br>Trésorerie : %s", percent(q.ProfitMargin), percent(q.OperatingMargin), percent(q.RevenueGrowth), amount(q.OperatingCashflow, q.Currency), amount(q.FreeCashflow, q.Currency), amount(q.TotalDebt, q.Currency), amount(q.TotalCash, q.Currency))
			for _, reason := range pick.signals {
				fmt.Fprintf(sb, "<br>%s", html.EscapeString(reason))
			}
			// Only factual warnings, never an overall quality verdict or trade score.
			if validNumber(q.FreeCashflow) && *q.FreeCashflow < 0 {
				sb.WriteString(`<br><span class="watch">Cash-flow libre négatif</span>`)
			}
			if validNumber(q.ProfitMargin) && *q.ProfitMargin < 0 {
				sb.WriteString(`<br><span class="watch">Marge nette négative</span>`)
			}
			fmt.Fprintf(sb, `<br><span class="muted">%s</span>`, collectionDate(q.CollectedAt, options.Now))
		}
		if f.RefreshFailed {
			sb.WriteString(`<br><span class="watch">Actualisation indisponible</span>`)
		}
		sb.WriteString("</td></tr>\n")
	}
	sb.WriteString("</table>\n")
}
