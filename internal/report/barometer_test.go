package report

import (
	"html"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/barometer"
	"stock-portfolio/internal/macro"
)

func TestVIXAppearsOnlyInBarometer(t *testing.T) {
	g, err := NewGenerator(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.Barometer = &barometer.Summary{Label: "Mitigé", Factors: []barometer.Factor{{Name: "VIX", Value: "17.03 (+8.33%)"}}}
	content, err := g.GenerateWithAI(nil, nil, "", NewVIXData(17.03, 8.33), nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "vix-banner") || strings.Contains(content, "Normal volatility") {
		t.Fatal("standalone VIX banner should not render")
	}
	if strings.Count(content, ">VIX<") != 1 || !strings.Contains(html.UnescapeString(content), "17.03 (+8.33%)") {
		t.Fatal("VIX must remain visible once in the barometer")
	}
}

func TestSupplementalContextRendering(t *testing.T) {
	g, err := NewGenerator(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	now, _ := time.Parse(time.RFC3339, "2026-10-12T10:00:00-04:00")
	g.Barometer = &barometer.Summary{Label: "Assez favorable", Class: "positive"}
	g.MarketContext = barometer.EvaluateContext(g.Barometer, nil, nil, now)
	g.MacroAlerts = []macro.CalendarAlert{{Title: "PPI US", State: "today"}}
	g.MarketContext.Weekday.Instruments[0] = barometer.Statistics{
		Name: "Nasdaq <100>", Count: 8, Positive: 5, Negative: 3, PositivePercent: 62.5,
		Mean: .42, Median: .25, OpenMean: .18, OpenCount: 7,
		Bias: barometer.Bias{Label: "Favorable", Class: "positive"}, Coverage: "8/8 observations",
	}
	g.MarketContext.Monthly.Instruments[0] = barometer.Statistics{
		Name: "Nasdaq 100", Count: 15, Positive: 12, Negative: 3, PositivePercent: 80,
		Mean: 2.4, Median: 2.1, Best: 10, Worst: -12, BestYear: 2020, WorstYear: 2018,
		Bias: barometer.Bias{Label: "Favorable", Class: "positive"},
	}
	content, err := g.GenerateWithAI(nil, nil, "", nil, []EconomicEventData{{Name: "CPI US", Date: "14/10 08:30 EDT"}})
	if err != nil {
		t.Fatal(err)
	}
	content = html.UnescapeString(content)
	for _, want := range []string{"Assez favorable", "AI/Semis :", "Backtest du lundi", "5 verts / 3 rouges", "Saisonnalité d’octobre", "Baromètre actions US :", "Facteurs de marché", "Conclusion :", "Indisponible"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(content, "Setup actuel") || strings.Contains(content, "Biais AI / Semis") || strings.Count(content, "Assez favorable") != 1 {
		t.Fatal("main barometer must retain its exact label once, without a second setup indicator")
	}
	_, summary, foundSummary := strings.Cut(content, `aria-label="Synthèse des biais"`)
	summary, _, _ = strings.Cut(summary, "</table>")
	_, weekdayRow, foundWeekday := strings.Cut(summary, "Backtest du lundi :")
	weekdayRow, _, _ = strings.Cut(weekdayRow, "</tr>")
	if !foundSummary || !foundWeekday || !strings.Contains(weekdayRow, "Nasdaq <100> : 5 verts / 3 rouges") || strings.Count(content, "5 verts / 3 rouges") != 1 || strings.Count(content, "Backtest du lundi") != 1 {
		t.Fatal("session counts must appear only on the existing weekday row in the synthesis")
	}
	for _, unwanted := range []string{"Séances vertes", "Séances positifs", "Moyenne Close", "Médiane", "Moyenne Open", "ouvertures exploitables", "Mois positifs", "Rendement mensuel moyen", "Meilleur mois", "Pire mois", "8/8 observations", "62,5 %", "+0,42 %", "+0,25 %", "+0,18 %", "+10,00 % (2020)", "-12,00 % (2018)", "confirmation concordante", "Biais global directionnel"} {
		if strings.Contains(content, unwanted) {
			t.Errorf("detailed statistic still rendered: %q", unwanted)
		}
	}
	if strings.Count(content, "Saisonnalité d’octobre") != 1 || !strings.Contains(summary, "Saisonnalité d’octobre") {
		t.Fatal("seasonality must appear only in the synthesis")
	}
	raw, _ := g.GenerateWithAI(nil, nil, "", nil, nil)
	if !strings.Contains(raw, "Nasdaq &lt;100&gt;") {
		t.Fatal("unescaped instrument name")
	}
	previous := -1
	for _, marker := range []string{`class="macro-alerts"`, `aria-label="Synthèse du contexte de marché"`, `class="calendar-section"`, `aria-label="Baromètre actions US"`} {
		position := strings.Index(content, marker)
		if position <= previous {
			t.Fatalf("missing or misplaced section: %s", marker)
		}
		previous = position
	}
	g.MarketContext = nil
	content, err = g.GenerateWithAI(nil, nil, "", nil, nil)
	if err != nil || strings.Contains(content, "AI/Semis :") || !strings.Contains(content, "Assez favorable") {
		t.Fatal("optional context changed existing rendering")
	}
}

func TestBarometerIndependentOfMacroAlerts(t *testing.T) {
	g, err := NewGenerator(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.Barometer = &barometer.Summary{Label: "Assez favorable", Class: "positive", Factors: []barometer.Factor{{Name: "S&P <test>", Value: "+0.20%"}}}
	for _, alerts := range [][]macro.CalendarAlert{nil, {{Title: "Aujourd'hui : PPI US", State: "today"}}} {
		g.MacroAlerts = alerts
		content, err := g.GenerateWithAI(nil, nil, "", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(content, "Assez favorable") || !strings.Contains(content, "S&amp;P &lt;test&gt;") {
			t.Fatal("barometer missing or unescaped")
		}
		if len(alerts) > 0 && (!strings.Contains(content, "PPI US") || strings.Index(content, `class="macro-alerts"`) > strings.Index(content, `class="market-barometer"`)) {
			t.Fatal("macro alert should precede barometer")
		}
	}
	g.Barometer = &barometer.Summary{Label: "Indisponible", Class: "neutral"}
	content, err := g.GenerateWithAI(nil, nil, "", nil, nil)
	if err != nil || !strings.Contains(content, "Indisponible") {
		t.Fatal("unavailable state missing")
	}
	g.Barometer = nil
	content, err = g.GenerateWithAI(nil, nil, "", nil, nil)
	if err != nil || strings.Contains(content, `class="market-barometer"`) {
		t.Fatal("empty barometer rendered")
	}
}
