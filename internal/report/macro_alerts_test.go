package report

import (
	"strings"
	"testing"

	"stock-portfolio/internal/macro"
)

func TestMacroAlertsRenderIndependentlyOfVIXAndCalendars(t *testing.T) {
	g, err := NewGenerator(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.MacroAlerts = []macro.CalendarAlert{{Title: "Demain : CPI US <test>", Message: "Publication à surveiller.", State: "upcoming"}}
	for _, vix := range []*VIXData{nil, {Price: "15.72", Change: "+2.75%"}} {
		content, err := g.GenerateWithAI(nil, nil, "", vix, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(content, `aria-label="Alertes macro proches"`) || !strings.Contains(content, "CPI US &lt;test&gt;") || strings.Contains(content, "CPI US <test>") {
			t.Fatal("alert missing or unescaped")
		}
	}
	g.MacroAlerts = nil
	content, err := g.GenerateWithAI(nil, nil, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, `aria-label="Alertes macro proches"`) {
		t.Fatal("empty alert banner rendered")
	}
}
