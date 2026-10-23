package decisions

import "stock-portfolio/internal/models"

// Called only after histories and fundamentals have passed freshness/validity checks.
func evaluatePullback(in Input, fundamental, daily, weekly float64) *models.PullbackEvaluation {
	p := &models.PullbackEvaluation{}
	// The swing fundamental score uses net margin. Recover its valuation part
	// before assessing operational quality for this independent screen.
	var netQuality float64
	for _, value := range []*float64{in.Quality.ProfitMargin, in.Quality.RevenueGrowth, in.Quality.FreeCashflow} {
		if *value > 0 {
			netQuality += 2
		} else if *value < 0 {
			netQuality -= 2
		}
	}
	p.Valuation = fundamental - netQuality/3
	p.NetLoss = *in.Quality.ProfitMargin < 0
	allPositive := true
	for _, value := range []*float64{in.Quality.OperatingMargin, in.Quality.RevenueGrowth, in.Quality.FreeCashflow} {
		if value == nil || !finite(*value) {
			allPositive = false
			continue
		}
		if *value > 0 {
			p.Quality += 2
		} else {
			allPositive = false
			if *value < 0 {
				p.Quality -= 2
			}
		}
	}
	p.Quality /= 3
	candles := in.Daily.Candles
	peak := 0.0
	for _, candle := range candles[len(candles)-20:] {
		peak = max(peak, candle.Close)
	}
	p.Drawdown20 = 100 * (1 - candles[len(candles)-1].Close/peak)
	p.Eligible = allPositive && p.Valuation >= 0 && p.Drawdown20 >= 10-1e-9 && (daily < 0 || weekly < 0)
	return p
}
