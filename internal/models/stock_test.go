package models

import "testing"

func TestStockResult_IsOversold(t *testing.T) {
	tests := []struct {
		name string
		rsi  float64
		want bool
	}{
		{"oversold", 25, true},
		{"boundary", 30, false},
		{"normal", 50, false},
		{"overbought", 75, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &StockResult{RSI: tt.rsi}
			if got := r.IsOversold(); got != tt.want {
				t.Errorf("IsOversold() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStockResult_IsOverbought(t *testing.T) {
	tests := []struct {
		name string
		rsi  float64
		want bool
	}{
		{"oversold", 25, false},
		{"normal", 50, false},
		{"boundary", 70, false},
		{"overbought", 75, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &StockResult{RSI: tt.rsi}
			if got := r.IsOverbought(); got != tt.want {
				t.Errorf("IsOverbought() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStockResult_IsPositive(t *testing.T) {
	tests := []struct {
		name   string
		change float64
		want   bool
	}{
		{"positive", 5.5, true},
		{"small positive", 0.02, true},
		{"tiny positive", 0.001, false},
		{"zero", 0, false},
		{"negative", -5.5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &StockResult{ChangePercent: tt.change}
			if got := r.IsPositive(); got != tt.want {
				t.Errorf("IsPositive() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStockResult_IsNegative(t *testing.T) {
	tests := []struct {
		name   string
		change float64
		want   bool
	}{
		{"positive", 5.5, false},
		{"zero", 0, false},
		{"tiny negative", -0.001, false},
		{"small negative", -0.02, true},
		{"negative", -5.5, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &StockResult{ChangePercent: tt.change}
			if got := r.IsNegative(); got != tt.want {
				t.Errorf("IsNegative() = %v, want %v", got, tt.want)
			}
		})
	}
}
