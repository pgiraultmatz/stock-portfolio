package marketdigest

import (
	"fmt"
	"html"
	"sort"
	"strings"
	"time"

	"stock-portfolio/internal/technicalalerts"
)

type RankingOptions struct {
	Limit        int
	Now          time.Time
	Financials   map[string]FinancialContext
	FinancialsAt time.Time
}

func rankingOptions(options []RankingOptions) RankingOptions {
	var result RankingOptions
	if len(options) > 0 {
		result = options[0]
	}
	if result.Limit != 5 {
		result.Limit = 3
	}
	if result.Now.IsZero() {
		result.Now = time.Now()
	}
	return result
}

type topPick struct {
	ticker  string
	name    string
	action  string
	score   int
	signals []string
	latest  int64
}

type evidence struct {
	weight int
	label  string
	at     int64
}

// Each family contributes at most once per timeframe. Several EMA crossings or
// simultaneous RSI/MACD turns cannot inflate their family's contribution.
type periodEvidence struct {
	structure  evidence
	momentum   evidence
	exhaustion evidence
}

func stronger(a, b evidence) evidence {
	if b.weight > a.weight || (b.weight == a.weight && (b.at > a.at || (b.at == a.at && b.label < a.label))) {
		return b
	}
	return a
}

func fresh(at int64, now time.Time, days int) bool {
	return at > 0 && at <= now.Unix() && !time.Unix(at, 0).Before(now.AddDate(0, 0, -days))
}

func summarizePeriod(signals timeframeSignals, bias, prefix string, now time.Time) periodEvidence {
	var result periodEvidence
	days, divergenceDays := 7, 14
	if prefix == "W" {
		days, divergenceDays = 21, 42
	}
	for _, alert := range signals.Technical {
		if alert.Bias != bias || !fresh(alert.CandleTime, now, days) {
			continue
		}
		e := evidence{label: prefix + ": " + alert.Label, at: alert.CandleTime}
		switch alert.Kind {
		case technicalalerts.Breakout, technicalalerts.Breakdown:
			e.weight = 3
			result.structure = stronger(result.structure, e)
		case technicalalerts.EMAReclaim, technicalalerts.EMALoss:
			e.weight = 2
			result.structure = stronger(result.structure, e)
		case technicalalerts.RSIRegimeUp, technicalalerts.RSIRegimeDn, technicalalerts.MACDBullish, technicalalerts.MACDBearish:
			e.weight = 2
			result.momentum = stronger(result.momentum, e)
		}
	}
	for _, alert := range signals.Divergences {
		d := alert.Divergence
		if d.Kind != bias || !fresh(d.ToTime, now, divergenceDays) {
			continue
		}
		e := evidence{weight: 2, label: prefix + ": " + strings.ToUpper(bias) + " RSI divergence", at: d.ToTime}
		result.momentum = stronger(result.momentum, e)
		if bias == "bearish" && d.FromRSI >= 70 {
			e.label += " (RSI initial >= 70)"
			result.exhaustion = stronger(result.exhaustion, e)
		}
	}
	return result
}

func (p periodEvidence) score() int { return p.structure.weight + p.momentum.weight }

func directionalPick(group multiTickerGroup, daily, weekly, oppositeDaily, oppositeWeekly periodEvidence, buy bool) (topPick, bool) {
	structure := stronger(daily.structure, weekly.structure)
	momentum := stronger(daily.momentum, weekly.momentum)
	if structure.weight == 0 || momentum.weight == 0 {
		return topPick{}, false
	}
	// A contrary weekly structure is not an aligned entry/exit setup.
	if oppositeWeekly.structure.weight > 0 {
		return topPick{}, false
	}
	score := daily.score() + weekly.score() - oppositeDaily.score() - oppositeWeekly.score()
	aligned := daily.score() > 0 && weekly.score() > 0 && oppositeDaily.score() == 0 && oppositeWeekly.score() == 0
	if aligned {
		score += 2
	}
	if score < 4 {
		return topPick{}, false
	}
	action := "Protection / sortie à étudier"
	if buy {
		action = "Achat à étudier"
		if group.InPortfolio == nil || *group.InPortfolio {
			action = "Renforcement à étudier"
		}
	}
	pick := topPick{ticker: group.Ticker, name: group.Name, action: action, score: score, signals: []string{structure.label, momentum.label}, latest: max(structure.at, momentum.at)}
	if aligned {
		pick.signals = append(pick.signals, "Convergence daily / weekly")
	}
	if oppositeDaily.score() > 0 {
		pick.signals = append(pick.signals, "Signaux daily contraires présents")
	}
	return pick, true
}

func rankDecisions(groups []multiTickerGroup, options RankingOptions) (buys, sells []topPick) {
	options = rankingOptions([]RankingOptions{options})
	for _, group := range groups {
		bd := summarizePeriod(group.Daily, "bullish", "D", options.Now)
		bw := summarizePeriod(group.Weekly, "bullish", "W", options.Now)
		sd := summarizePeriod(group.Daily, "bearish", "D", options.Now)
		sw := summarizePeriod(group.Weekly, "bearish", "W", options.Now)
		held := group.InPortfolio == nil || *group.InPortfolio
		if held {
			if pick, ok := directionalPick(group, sd, sw, bd, bw, false); ok {
				sells = append(sells, pick)
				continue
			}
			// An overbought bearish divergence in a still-positive structure is
			// an exhaustion warning, not a confirmed bearish exit.
			exhaustion := stronger(sd.exhaustion, sw.exhaustion)
			structure := stronger(bd.structure, bw.structure)
			bullMomentum := stronger(bd.momentum, bw.momentum)
			if exhaustion.weight > 0 && structure.weight > 0 && sd.structure.weight == 0 && sw.structure.weight == 0 && bullMomentum.weight == 0 {
				sells = append(sells, topPick{ticker: group.Ticker, name: group.Name, action: "Allègement à étudier", score: 4, signals: []string{exhaustion.label, structure.label}, latest: max(exhaustion.at, structure.at)})
				continue
			}
		}
		if pick, ok := directionalPick(group, bd, bw, sd, sw, true); ok {
			buys = append(buys, pick)
		}
	}
	return limitPicks(buys, options.Limit), limitPicks(sells, options.Limit)
}

func limitPicks(picks []topPick, limit int) []topPick {
	sort.Slice(picks, func(i, j int) bool {
		if picks[i].score != picks[j].score {
			return picks[i].score > picks[j].score
		}
		if picks[i].latest != picks[j].latest {
			return picks[i].latest > picks[j].latest
		}
		return picks[i].ticker < picks[j].ticker
	})
	if len(picks) > limit {
		picks = picks[:limit]
	}
	return picks
}

func writeTopSection(sb *strings.Builder, groups []tickerGroup, timeframe string, options RankingOptions) {
	var multi []multiTickerGroup
	for _, group := range groups {
		g := multiTickerGroup{Ticker: group.Ticker, Name: group.Name, InPortfolio: group.InPortfolio}
		period := timeframeSignals{Divergences: group.Divergences, Technical: group.Technical}
		if timeframe == "weekly" {
			g.Weekly = period
		} else {
			g.Daily = period
		}
		multi = append(multi, g)
	}
	writeTopSectionMulti(sb, multi, options)
}

func writeTopSectionMulti(sb *strings.Builder, groups []multiTickerGroup, options RankingOptions) {
	buys, sells := rankDecisions(groups, options)
	sb.WriteString("<h3>Priorités à examiner</h3>\n<table class=\"top-picks\">\n")
	fmt.Fprintf(sb, "<tr><th>Top %d achats / renforcements</th><th>Top %d allègements / ventes</th></tr><tr><td>", options.Limit, options.Limit)
	writeTopList(sb, buys, "bullish")
	sb.WriteString("</td><td>")
	writeTopList(sb, sells, "bearish")
	sb.WriteString("</td></tr></table>\n")
}

func writeTopList(sb *strings.Builder, picks []topPick, className string) {
	if len(picks) == 0 {
		sb.WriteString(`<span class="muted">Aucune configuration suffisamment convergente.</span>`)
		return
	}
	sb.WriteString(`<ol class="signal-list">`)
	for _, pick := range picks {
		fmt.Fprintf(sb, `<li><span class="%s">%s</span> <span class="muted">%s</span><span class="pick-action">%s</span><ul class="pick-reasons">`, className, html.EscapeString(pick.ticker), html.EscapeString(pick.name), html.EscapeString(pick.action))
		for _, reason := range pick.signals {
			fmt.Fprintf(sb, "<li>%s</li>", html.EscapeString(reason))
		}
		fmt.Fprintf(sb, `</ul><span class="muted">Signal au %s UTC</span></li>`, time.Unix(pick.latest, 0).UTC().Format("02/01/2006"))
	}
	sb.WriteString("</ol>")
}
