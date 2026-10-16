package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTwitterEnabledByDefault(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options string
		want    bool
	}{
		{"omitted", "", true},
		{"paused prompts", `,"report":{"enable_prompts":false}`, true},
		{"explicit enabled", `,"twitter":{"enabled":true}`, true},
		{"twitter options without switch", `,"twitter":{"max_tweets":4}`, true},
		{"explicit opt out", `,"twitter":{"enabled":false}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			data := `{"stocks":[{"ticker":"TEST","name":"Test"}]` + tc.options + `}`
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Twitter.Enabled != tc.want {
				t.Fatalf("Twitter.Enabled = %v, want %v", cfg.Twitter.Enabled, tc.want)
			}
		})
	}
}
