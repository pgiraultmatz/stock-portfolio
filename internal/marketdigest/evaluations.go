package marketdigest

import (
	"fmt"
	"html"
	"sort"
	"strings"

	"stock-portfolio/internal/decisions"
	"stock-portfolio/internal/models"
)

type evaluationPick struct {
	stock      models.Stock
	evaluation models.Evaluation
	pullback   bool
}

func rankEvaluations(options RankingOptions) (buys, sells, excluded []evaluationPick) {
	seen := map[string]bool{}
	for _, s := range options.Stocks {
		if seen[s.Ticker] {
			continue
		}
		seen[s.Ticker] = true
		e, ok := options.Evaluations[s.Ticker]
		if !ok {
			e.Warnings = []string{"Évaluation indisponible"}
		}
		p := evaluationPick{stock: s, evaluation: e}
		if !e.Current() || !e.Eligible {
			excluded = append(excluded, p)
			continue
		}
		switch decisions.Direction(e.Signal) {
		case 1:
			buys = append(buys, p)
		case -1:
			sells = append(sells, p)
		}
	}
	for _, list := range [][]evaluationPick{buys, sells} {
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i].evaluation, list[j].evaluation
			if decisions.Strength(a.Signal) != decisions.Strength(b.Signal) {
				return decisions.Strength(a.Signal) > decisions.Strength(b.Signal)
			}
			if a.Score != b.Score {
				if decisions.Direction(a.Signal) > 0 {
					return a.Score > b.Score
				}
				return a.Score < b.Score
			}
			return list[i].stock.Ticker < list[j].stock.Ticker
		})
	}
	sort.Slice(excluded, func(i, j int) bool { return excluded[i].stock.Ticker < excluded[j].stock.Ticker })
	limit := rankingOptions([]RankingOptions{options}).Limit
	return buys[:min(limit, len(buys))], sells[:min(limit, len(sells))], excluded
}

// Rebounds are watch candidates, not upgrades to the shared investment signal.
func rankReversals(options RankingOptions) []evaluationPick {
	var picks []evaluationPick
	seen := map[string]bool{}
	for _, stock := range options.Stocks {
		if seen[stock.Ticker] {
			continue
		}
		seen[stock.Ticker] = true
		e, ok := options.Evaluations[stock.Ticker]
		if ok && e.Current() && e.Eligible && e.Signal == "HOLD" && e.Weekly < 0 && e.Daily == 2 {
			picks = append(picks, evaluationPick{stock: stock, evaluation: e})
		}
	}
	sort.Slice(picks, func(i, j int) bool {
		if picks[i].evaluation.Score != picks[j].evaluation.Score {
			return picks[i].evaluation.Score > picks[j].evaluation.Score
		}
		return picks[i].stock.Ticker < picks[j].stock.Ticker
	})
	limit := min(5, rankingOptions([]RankingOptions{options}).Limit)
	return picks[:min(limit, len(picks))]
}

func rankPullbacks(options RankingOptions) []evaluationPick {
	var picks []evaluationPick
	seen := map[string]bool{}
	for _, stock := range options.Stocks {
		if seen[stock.Ticker] {
			continue
		}
		seen[stock.Ticker] = true
		e, ok := options.Evaluations[stock.Ticker]
		if ok && e.Current() && e.Eligible && e.QuoteType == "EQUITY" && e.Fundamental != nil && e.Pullback != nil && e.Pullback.Eligible {
			picks = append(picks, evaluationPick{stock: stock, evaluation: e, pullback: true})
		}
	}
	sort.Slice(picks, func(i, j int) bool {
		a, b := picks[i].evaluation, picks[j].evaluation
		if a.Pullback.Drawdown20 != b.Pullback.Drawdown20 {
			return a.Pullback.Drawdown20 > b.Pullback.Drawdown20
		}
		if a.Pullback.Valuation != b.Pullback.Valuation {
			return a.Pullback.Valuation > b.Pullback.Valuation
		}
		return picks[i].stock.Ticker < picks[j].stock.Ticker
	})
	const limit = 15
	return picks[:min(limit, len(picks))]
}

func writeEvaluationTopSection(sb *strings.Builder, options RankingOptions) {
	buys, sells, _ := rankEvaluations(options)
	limit := rankingOptions([]RankingOptions{options}).Limit
	sb.WriteString(`<h3>Priorités à examiner</h3><p class="muted">Horizon : semaines à mois · Conviction personnelle indépendante.</p>`)
	fmt.Fprintf(sb, `<table class="top-picks"><tr><th>Top %d achats / renforcements</th><th>Top %d allègements / ventes</th></tr><tr><td>`, limit, limit)
	writeEvaluationList(sb, buys, true)
	sb.WriteString("</td><td>")
	writeEvaluationList(sb, sells, false)
	sb.WriteString("</td></tr></table>")
	sb.WriteString(`<h3>Opportunités en repli</h3><p class="muted">Repli ≥ 10 % sur 20 séances · Marge opérationnelle, croissance et cash-flow libre positifs · Valorisation au moins neutre selon nos ratios. Classées par ampleur du repli ; signal swing inchangé.</p><div class="reversal-picks">`)
	writeEvaluationList(sb, rankPullbacks(options), false)
	sb.WriteString("</div>")
	if reversals := rankReversals(options); len(reversals) > 0 {
		sb.WriteString(`<h3>Retournements à surveiller</h3><p class="muted">Daily positif, weekly encore défavorable · Rebond à confirmer.</p><div class="reversal-picks">`)
		writeEvaluationList(sb, reversals, false)
		sb.WriteString("</div>")
	}
}

func writeEvaluationList(sb *strings.Builder, picks []evaluationPick, buy bool) {
	if len(picks) == 0 {
		sb.WriteString(`<span class="muted">Aucun candidat admissible.</span>`)
		return
	}
	sb.WriteString(`<ol class="signal-list">`)
	for _, p := range picks {
		e := p.evaluation
		action := "À éviter"
		class := "bearish"
		if buy {
			class = "bullish"
			action = "Achat à étudier"
			if p.stock.IsInPortfolio() {
				action = "Renforcement à étudier"
			}
		} else if p.stock.IsInPortfolio() {
			action = "Allègement / vente à étudier"
		}
		if e.Signal == "HOLD" {
			action, class = "Rebond à confirmer", "watch"
		}
		if p.pullback {
			action, class = "Entrée anticipée · risque élevé", "watch"
		}
		fmt.Fprintf(sb, `<li><strong class="%s">%s — %s</strong> <span class="muted">%s</span>`, class, html.EscapeString(p.stock.Ticker), html.EscapeString(e.Signal), html.EscapeString(p.stock.Name))
		if category := strings.TrimSpace(p.stock.Category); category != "" {
			fmt.Fprintf(sb, ` <span class="muted">· %s</span>`, html.EscapeString(category))
		}
		if e.QuoteType == "ETF" {
			sb.WriteString(` <span class="muted">ETF</span>`)
		}
		fmt.Fprintf(sb, `<div class="pick-action">%s`, action)
		for _, warning := range e.Warnings {
			if warning == "WAIT EARNINGS" {
				sb.WriteString(` <span class="earnings-warning" style="display:inline-block;background:#fff0bd;color:#783c00;border:1px solid #d79a20;border-radius:4px;padding:2px 6px;font-size:12px;font-weight:bold">WAIT EARNINGS`)
				if e.EarningsAt != nil {
					fmt.Fprintf(sb, ` · %s`, e.EarningsAt.UTC().Format("02/01"))
				}
				sb.WriteString("</span>")
				break
			}
		}
		sb.WriteString("</div>")
		fmt.Fprintf(sb, `<div>%.2f`, e.Price)
		if e.DailyChange != nil {
			fmt.Fprintf(sb, ` · D %+.2f%%`, *e.DailyChange)
		}
		if e.WeeklyChange != nil {
			fmt.Fprintf(sb, ` · W %+.2f%%`, *e.WeeklyChange)
		}
		if !e.PriceAt.IsZero() {
			fmt.Fprintf(sb, ` <span class="muted">(%s)</span>`, e.PriceAt.UTC().Format("02/01"))
		}
		fmt.Fprintf(sb, `</div><div class="muted">Weekly %s · Daily %s · Score %+.1f</div>`, evaluationOutlook(e.Weekly), evaluationOutlook(e.Daily), e.Score)
		if p.pullback && e.Pullback != nil {
			fmt.Fprintf(sb, `<div class="muted">Repli −%.1f%% du sommet de clôture sur 20 séances</div>`, e.Pullback.Drawdown20)
			if e.Pullback.NetLoss {
				sb.WriteString(`<div class="watch">Résultat net négatif — à examiner</div>`)
			}
		}
		var warnings []string
		for _, warning := range e.Warnings {
			if warning == "WAIT EARNINGS" {
				continue
			}
			if warning == "Date de résultats inconnue" {
				warning = "Résultats inconnus"
			}
			warnings = append(warnings, warning)
		}
		if len(warnings) > 0 {
			fmt.Fprintf(sb, `<div class="muted">%s</div>`, html.EscapeString(strings.Join(warnings, " · ")))
		}
		sb.WriteString("</li>")
	}
	sb.WriteString("</ol>")
}

func evaluationOutlook(score float64) string {
	if score > 0 {
		return "favorable"
	}
	if score < 0 {
		return "défavorable"
	}
	return "neutre"
}
