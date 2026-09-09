package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"stock-portfolio/internal/config"
	"stock-portfolio/internal/models"
	"stock-portfolio/internal/yahoo"
)

func TestStandaloneFinancialReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "financial-review.html")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runFinancialDigest(context.Background(), &config.Config{}, path, 5, logger); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Revue financière") || !strings.Contains(string(content), "Top 5 valorisation") || strings.Contains(string(content), "Market Signal") {
		t.Fatal("wrong report generated")
	}
	if err := runFinancialDigest(context.Background(), &config.Config{}, path, 4, logger); err == nil {
		t.Fatal("invalid limit accepted")
	}
}

func TestDigestFinancialCacheAndFallback(t *testing.T) {
	now := time.Date(2026, 9, 8, 20, 0, 0, 0, time.UTC)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, scenario := range []string{"fresh", "legacy", "stale", "failure"} {
		t.Run(scenario, func(t *testing.T) {
			cache := &config.StockDataFile{UpdatedAt: now, Stocks: map[string]config.StockFundamentals{"TEST": {PEGRatio: 1.7, FinancialQuality: &models.FinancialQuality{CollectedAt: now, QuoteType: "EQUITY"}}}}
			if scenario == "legacy" {
				f := cache.Stocks["TEST"]
				f.FinancialQuality = nil
				cache.Stocks["TEST"] = f
			}
			if scenario == "stale" || scenario == "failure" {
				cache.UpdatedAt = now.Add(-48 * time.Hour)
			}
			calls := 0
			fetch := func(context.Context, string) (yahoo.ValuationData, error) {
				calls++
				if scenario == "failure" {
					return yahoo.ValuationData{}, errors.New("unavailable")
				}
				return yahoo.ValuationData{PEGRatio: 2.3, FinancialQuality: &models.FinancialQuality{CollectedAt: now, QuoteType: "EQUITY"}}, nil
			}
			got := enrichDigestFinancials(context.Background(), []string{"TEST", "TEST"}, cache, now, fetch, logger)["TEST"]
			if scenario == "fresh" && calls != 0 {
				t.Fatal("fresh cache refetched")
			}
			if scenario != "fresh" && calls != 1 {
				t.Fatalf("refresh count %d", calls)
			}
			want := 1.7
			if scenario == "stale" {
				want = 2.3
			}
			if got.Data.PEGRatio != want || got.Data.FinancialQuality == nil {
				t.Fatalf("cache lost: %+v", got)
			}
			if scenario == "failure" && (!got.RefreshFailed || !got.ValuationAt.Equal(cache.UpdatedAt)) {
				t.Fatal("failed refresh made old data fresh")
			}
		})
	}
}
