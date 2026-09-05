package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"stock-portfolio/internal/config"
)

func TestMockReportWithPausedPrompts(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.html")
	promptPath := filepath.Join(dir, "prompt.html")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runMockReport(reportPath, promptPath, config.ReportConfig{}, logger); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(promptPath); !os.IsNotExist(err) {
		t.Fatalf("paused prompt should not create a file: %v", err)
	}
	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), `<table class="stock-table">`) {
		t.Fatal("position recap should be hidden by default")
	}
}
