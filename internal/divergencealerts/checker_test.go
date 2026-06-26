package divergencealerts

import (
	"testing"

	"stock-portfolio/internal/chartcalc"
)

func TestDivergenceOnLatestCandle(t *testing.T) {
	div := chartcalc.Divergence{ToTime: 200}
	if !divergenceOnLatestCandle(div, 200) {
		t.Fatal("expected divergence on latest candle to be alertable")
	}
	if divergenceOnLatestCandle(div, 201) {
		t.Fatal("expected older divergence to be ignored")
	}
	if divergenceOnLatestCandle(chartcalc.Divergence{}, 201) {
		t.Fatal("expected empty divergence to be ignored")
	}
}
