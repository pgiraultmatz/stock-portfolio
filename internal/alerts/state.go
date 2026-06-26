// Package alerts handles price alert detection and deduplication.
package alerts

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// State tracks which alerts have already been sent today.
// It resets automatically when the date changes.
type State struct {
	Date string          `json:"date"` // "2026-03-21"
	Sent map[string]bool `json:"sent"` // "NVDA:+3.0" -> true
}

// LoadState reads the state file from disk.
// If the file doesn't exist or the date has changed, returns a fresh state.
func LoadState(path string) (*State, error) {
	today := time.Now().Format("2006-01-02")

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{Date: today, Sent: make(map[string]bool)}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading alert state: %w", err)
	}

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupted file — start fresh
		return &State{Date: today, Sent: make(map[string]bool)}, nil
	}

	// New day: reset state
	if s.Date != today {
		return &State{Date: today, Sent: make(map[string]bool)}, nil
	}

	return &s, nil
}

// Save writes the state to disk.
func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// HasBeenSent returns true if this alert was already sent today.
func (s *State) HasBeenSent(ticker string, threshold float64) bool {
	return s.Sent[alertKey(ticker, threshold)]
}

// MarkSent records that this alert has been sent.
func (s *State) MarkSent(ticker string, threshold float64) {
	if s.Sent == nil {
		s.Sent = make(map[string]bool)
	}
	s.Sent[alertKey(ticker, threshold)] = true
}

// alertKey builds the deduplication key: "NVDA:+3.0" or "NVDA:-5.0".
func alertKey(ticker string, threshold float64) string {
	sign := "+"
	if threshold < 0 {
		sign = ""
	}
	return fmt.Sprintf("%s:%s%.1f", ticker, sign, threshold)
}
