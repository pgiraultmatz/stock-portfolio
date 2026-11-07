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
	signals      multiTickerGroup
	stock        models.Stock
	evaluation   models.Evaluation
	pullback     bool
	isNew        bool
	previousRank int
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
	attach := func(picks []evaluationPick) []evaluationPick {
		for i := range picks {
			picks[i].signals = options.DigestSignals[picks[i].stock.Ticker]
		}
		return picks
	}
	limit := rankingOptions([]RankingOptions{options}).Limit
	sb.WriteString(`<h3>Priorités à examiner</h3><p class="muted">Horizon : semaines à mois · Conviction personnelle indépendante.</p>`)
	fmt.Fprintf(sb, `<table class="top-picks" style="background:transparent;box-shadow:none;border-spacing:0"><tr><th style="background:transparent;color:#26754d">↗ Top %d achats / renforcements</th><th style="background:transparent;color:#b54445">↘ Top %d allègements / ventes</th></tr><tr><td style="border:0;padding:4px 7px 0 0">`, limit, limit)
	writeEvaluationList(sb, attach(markNewPicks(buys, options.Previous, sectionBuys)), true)
	sb.WriteString(`</td><td style="border:0;padding:4px 0 0 7px">`)
	writeEvaluationList(sb, attach(markNewPicks(sells, options.Previous, sectionSells)), false)
	sb.WriteString("</td></tr></table>")
	sb.WriteString(`<h3>Opportunités en repli</h3><p class="muted">Repli ≥ 10 % sur 20 séances · Marge opérationnelle, croissance et cash-flow libre positifs · Valorisation au moins neutre selon nos ratios. Classées par ampleur du repli ; signal swing inchangé.</p><div class="reversal-picks">`)
	writeEvaluationList(sb, attach(markNewPicks(rankPullbacks(options), options.Previous, sectionPullbacks)), false)
	sb.WriteString("</div>")
	if reversals := rankReversals(options); len(reversals) > 0 {
		sb.WriteString(`<h3>Retournements à surveiller</h3><p class="muted">Daily positif, weekly encore défavorable.</p><div class="reversal-picks">`)
		writeEvaluationList(sb, attach(markNewPicks(reversals, options.Previous, sectionReversals)), false)
		sb.WriteString("</div>")
	}
}

func writeEvaluationList(sb *strings.Builder, picks []evaluationPick, buy bool) {
	if len(picks) == 0 {
		sb.WriteString(`<span class="muted">Aucun candidat admissible.</span>`)
		return
	}
	sb.WriteString(`<ol class="signal-list" style="list-style:none;margin:0;padding:0">`)
	for i, p := range picks {
		e := p.evaluation
		class := "bearish"
		if buy {
			class = "bullish"
		}
		if e.Signal == "HOLD" || p.pullback {
			class = "watch"
		}
		color, accent := "#b54445", "#df7773"
		if class == "bullish" {
			color, accent = "#26754d", "#57a879"
		} else if class == "watch" {
			color, accent = "#986b1e", "#d9a442"
		}
		sb.WriteString(`<li style="margin:0 0 9px">`)
		if p.isNew {
			background, border := "#fdf6e7", "#e8ce91"
			if class == "bullish" {
				background, border = "#edf7ef", "#b7d9bf"
			} else if class == "bearish" {
				background, border = "#fdf0ef", "#ebbfbb"
			}
			// Inline styles survive email clients which discard stylesheet classes.
			fmt.Fprintf(sb, `<div class="new-entry" style="display:inline-block;max-width:100%%;box-sizing:border-box;vertical-align:top;background-color:%s;border:1px solid %s;border-radius:6px;padding:3px">`, background, border)
		}
		fmt.Fprintf(sb, `<div class="digest-card" style="background:#ffffff;border:1px solid #e4e9ed;border-left:3px solid %s;border-radius:7px;padding:12px;margin:0"><table role="presentation" style="width:100%%;border-collapse:collapse;background:transparent;box-shadow:none"><tr><td style="padding:0;border:0;vertical-align:top"><strong class="%s" style="color:%s;font-size:15px">%s — %s`, accent, class, color, html.EscapeString(p.stock.Ticker), html.EscapeString(e.Signal))
		if rank := i + 1; p.previousRank > 0 && p.previousRank != rank {
			arrow, places := "▲", p.previousRank-rank
			if places < 0 {
				arrow, places = "▼", -places
			}
			// Inherit the ticker/signal color in every section, including email clients.
			fmt.Fprintf(sb, ` <span class="rank-change" title="Rang %d → %d" aria-label="Rang %d → %d" style="display:inline-block;white-space:nowrap;font-size:12px;color:inherit">%s %d</span>`, p.previousRank, rank, p.previousRank, rank, arrow, places)
		}
		fmt.Fprintf(sb, `</strong></td><td style="padding:0;border:0;text-align:right;vertical-align:top;white-space:nowrap;color:%s;font-weight:bold;font-size:14px">%+.1f <span style="font-size:10px;color:#8a96a3;font-weight:normal">SCORE</span></td></tr></table><div class="muted" style="font-size:12px;color:#758190;margin:6px 0">%s`, color, e.Score, html.EscapeString(p.stock.Name))
		if category := strings.TrimSpace(p.stock.Category); category != "" {
			fmt.Fprintf(sb, ` <span class="muted">· %s</span>`, html.EscapeString(category))
		}
		if e.QuoteType == "ETF" {
			sb.WriteString(` <span class="muted">ETF</span>`)
		}
		sb.WriteString(`</div>`)
		for _, warning := range e.Warnings {
			if warning == "WAIT EARNINGS" {
				sb.WriteString(`<div class="pick-action"><span class="earnings-warning" style="display:inline-block;background:#fff0bd;color:#783c00;border:1px solid #d79a20;border-radius:4px;padding:2px 6px;font-size:12px;font-weight:bold">WAIT EARNINGS`)
				if e.EarningsAt != nil {
					fmt.Fprintf(sb, ` · %s`, e.EarningsAt.UTC().Format("02/01"))
				}
				sb.WriteString("</span></div>")
				break
			}
		}
		for _, period := range []struct {
			label   string
			signals timeframeSignals
		}{{"D", p.signals.Daily}, {"W", p.signals.Weekly}} {
			for _, alert := range period.signals.Divergences {
				color := "#777777"
				if alert.Divergence.Kind == "bearish" {
					color = "#e74c3c"
				} else if alert.Divergence.Kind == "bullish" {
					color = "#27ae60"
				}
				bg := "#f2f5f4"
				if alert.Divergence.Kind == "bearish" {
					bg = "#fff4f3"
				} else if alert.Divergence.Kind == "bullish" {
					bg = "#e8f5ed"
				}
				fmt.Fprintf(sb, `<div style="color:%s;background:%s;padding:7px 9px;border-radius:5px;font-size:12px;margin-top:6px">%s · %s RSI divergence · %s → %s</div>`, color, bg, period.label, html.EscapeString(strings.ToUpper(alert.Divergence.Kind)), formatDate(alert.Divergence.FromTime), formatDate(alert.Divergence.ToTime))
			}
		}
		if p.pullback && e.Pullback != nil {
			fmt.Fprintf(sb, `<div class="muted">Repli −%.1f%% du sommet de clôture sur 20 séances</div>`, e.Pullback.Drawdown20)
			if e.Pullback.NetLoss {
				sb.WriteString(`<div class="watch">Résultat net négatif — à examiner</div>`)
			}
		}
		var warnings []string
		for _, warning := range e.Warnings {
			if warning == "WAIT EARNINGS" || warning == "Provisoire" || warning == "Date de résultats inconnue" {
				continue
			}
			warnings = append(warnings, warning)
		}
		if len(warnings) > 0 {
			fmt.Fprintf(sb, `<div class="muted">%s</div>`, html.EscapeString(strings.Join(warnings, " · ")))
		}
		sb.WriteString("</div>")
		if p.isNew {
			sb.WriteString("</div>")
		}
		sb.WriteString("</li>")
	}
	sb.WriteString("</ol>")
}

func markNewPicks(picks []evaluationPick, previous *SectionHistory, section string) []evaluationPick {
	for i := range picks {
		picks[i].isNew = previous.isNew(section, picks[i].stock.Ticker)
		picks[i].previousRank = previous.previousRank(section, picks[i].stock.Ticker)
	}
	return picks
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
