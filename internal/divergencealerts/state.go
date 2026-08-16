package divergencealerts

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type State struct {
	Date string          `json:"date"`
	Sent map[string]bool `json:"sent"`
}

func LoadState(path string) (*State, error) {
	return LoadStateForScope(path, time.Now().Format("2006-01-02"))
}

func LoadStateForTimeframe(path string, timeframe Timeframe) (*State, error) {
	return LoadStateForScope(path, StateScope(timeframe, time.Now()))
}

func StateScope(timeframe Timeframe, t time.Time) string {
	if timeframe == Weekly {
		year, week := t.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	}
	return t.Format("2006-01-02")
}

func LoadStateForScope(path string, scope string) (*State, error) {
	state := &State{Date: scope, Sent: make(map[string]bool)}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	if state.Date != scope {
		state.Date = scope
		state.Sent = make(map[string]bool)
	}
	if state.Sent == nil {
		state.Sent = make(map[string]bool)
	}
	return state, nil
}

func (s *State) Has(key string) bool {
	return s.Sent[key]
}

func (s *State) Mark(key string) {
	if s.Sent == nil {
		s.Sent = make(map[string]bool)
	}
	s.Sent[key] = true
}

func (s *State) Save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
