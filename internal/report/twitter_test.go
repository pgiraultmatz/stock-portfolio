package report

import (
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/twitter"
)

func TestTwitterAccountsDisplayedWithoutPrompts(t *testing.T) {
	g, err := NewGenerator(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.TwitterAccounts = []TwitterAccountData{
		{Group: "Marché", Username: "working", Tweets: []twitter.Tweet{{Text: "<script>alert(1)</script>\nMarket update", CreatedAt: time.Date(2026, 10, 16, 12, 30, 0, 0, time.UTC)}}},
		{Group: "Marché", Username: "blocked", FetchFailed: true},
		{Group: "Marché", Username: "quiet"},
	}
	content, err := g.Generate(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Tweets récents", "Marché", "@working", "16/10/2026 12:30",
		"&lt;script&gt;alert(1)&lt;/script&gt;", "Market update",
		"Impossible de récupérer les tweets de @blocked.",
		"Aucun tweet récent pour @quiet.",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, unwanted := range []string{"<script>", "Aucun tweet récent pour @blocked", "Impossible de récupérer les tweets de @quiet"} {
		if strings.Contains(content, unwanted) {
			t.Errorf("unexpected %q", unwanted)
		}
	}

	g.TwitterAccounts = g.TwitterAccounts[1:2]
	content, err = g.Generate(nil)
	if err != nil || !strings.Contains(content, "Impossible de récupérer les tweets de @blocked.") {
		t.Fatalf("all-failed collection must remain visible: %v", err)
	}

	g.TwitterAccounts = nil
	content, err = g.Generate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "Tweets récents") {
		t.Fatal("disabled collection must not display a tweets section")
	}
}
