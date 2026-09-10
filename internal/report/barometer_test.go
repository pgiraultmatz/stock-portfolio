package report

import (
	"html"
	"strings"
	"testing"

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
		if len(alerts) > 0 && (!strings.Contains(content, "PPI US") || strings.Index(content, `class="market-barometer"`) > strings.Index(content, `class="macro-alerts"`)) {
			t.Fatal("macro alert should remain below barometer")
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
