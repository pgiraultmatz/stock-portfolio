package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"stock-portfolio/internal/models"
)

// Analysis represents the AI-generated market analysis.
type Analysis struct {
	TopStocks       []TopStock       `json:"top_stocks"`
	NewsContext     []NewsItem       `json:"news_context"`
	Recommendations []Recommendation `json:"recommendations"`
	MarketSummary   string           `json:"market_summary"`
	GeneratedAt     time.Time        `json:"generated_at"`
}

// TopStock represents a stock highlighted by AI analysis.
type TopStock struct {
	Ticker    string `json:"ticker"`
	Name      string `json:"name"`
	Reasoning string `json:"reasoning"`
	Signal    string `json:"signal"` // "bullish", "bearish", "neutral"
}

// NewsItem represents a relevant market news item.
type NewsItem struct {
	Headline    string   `json:"headline"`
	Impact      string   `json:"impact"` // "positive", "negative", "neutral"
	AffectedBy  []string `json:"affected_by,omitempty"`
	Description string   `json:"description"`
}

// Recommendation represents an actionable recommendation.
type Recommendation struct {
	Ticker string `json:"ticker"`
	Name   string `json:"name"`
	Action string `json:"action"` // "buy", "sell", "hold", "watch"
	Reason string `json:"reason"`
	Risk   string `json:"risk"` // "low", "medium", "high"
}

// Analyzer performs AI-powered stock analysis.
type Analyzer struct {
	client         Client
	promptTemplate string
}

// NewAnalyzer creates a new AI analyzer with an already-loaded prompt template.
func NewAnalyzer(client Client, promptTemplate string) *Analyzer {
	return &Analyzer{client: client, promptTemplate: promptTemplate}
}

// XGroupSection holds the fetched content for one named Twitter/X group.
type XGroupSection struct {
	Name     string
	Accounts []string // Twitter/X handles that contributed content
	Content  string
}

// PromptContext holds optional context sections to include in the prompt.
type PromptContext struct {
	VIXLine string
	XGroups []XGroupSection
}

// FormatXGroups concatenates all group contents into a single string for API mode.
func FormatXGroups(groups []XGroupSection) string {
	var sb strings.Builder
	for _, g := range groups {
		if g.Content == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("## %s\n\n", g.Name))
		sb.WriteString(g.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// BuildPromptFromContent builds the prompt from an already-loaded template string.
func BuildPromptFromContent(results []*models.StockResult, templateContent string, ctx PromptContext) (string, error) {
	var a Analyzer
	stockData := a.prepareStockData(results)

	var sb strings.Builder
	sb.WriteString(templateContent)
	if ctx.VIXLine != "" {
		sb.WriteString("\n**Indicateur de volatilité:**\n")
		sb.WriteString(ctx.VIXLine)
		sb.WriteString("\n")
	}
	sb.WriteString(stockData)

	for i, group := range ctx.XGroups {
		if group.Content == "" {
			continue
		}
		userSuffix := ""
		if len(group.Accounts) > 0 {
			handles := make([]string, len(group.Accounts))
			for j, a := range group.Accounts {
				handles[j] = "@" + a
			}
			userSuffix = " - user " + strings.Join(handles, ", ")
		}
		sb.WriteString(fmt.Sprintf("\n\n════════════════════════════════════════\n"))
		sb.WriteString(fmt.Sprintf("SECTION %d — %s%s\n", i+2, strings.ToUpper(group.Name), userSuffix))
		sb.WriteString("════════════════════════════════════════\n\n")
		sb.WriteString(group.Content)
	}

	return sb.String(), nil
}

// Analyze performs AI analysis on stock results.
// twitterContext is optional: if non-empty, it is included in the prompt as additional context.
func (a *Analyzer) Analyze(ctx context.Context, results []*models.StockResult, twitterContext string) (*Analysis, error) {
	// Prepare stock data for the prompt
	stockData := a.prepareStockData(results)

	twitterSection := ""
	if twitterContext != "" {
		twitterSection = "\n\nContexte additionnel — analyses récentes d'un trader quantitatif crypto:\n" + twitterContext
	}

	systemPrompt := []byte(a.promptTemplate)

	userPrompt := fmt.Sprintf(`%s

Réponds UNIQUEMENT en JSON valide avec cette structure exacte:
{
  "top_stocks": [
    {"ticker": "XXX", "name": "Nom", "reasoning": "Explication courte", "signal": "bullish|bearish|neutral"}
  ],
  "news_context": [
    {"headline": "Titre court", "impact": "positive|negative|neutral", "affected_by": ["TICKER1", "TICKER2"], "description": "Description de l'impact"}
  ],
  "recommendations": [
    {"ticker": "XXX", "name": "Nom", "action": "buy|sell|hold|watch", "reason": "Raison courte", "risk": "low|medium|high"}
  ],
  "market_summary": "Résumé en 1-2 phrases de la situation globale du portefeuille. Mentionne également les dates importantes des prochaines semaines pour ce portefeuille (publications de résultats, dividendes, décisions de banques centrales, indicateurs macro) en précisant les tickers concernés."
}`, stockData+twitterSection)

	response, err := a.client.Complete(ctx, string(systemPrompt), userPrompt, 2000)
	if err != nil {
		return nil, fmt.Errorf("AI completion failed: %w", err)
	}

	// Parse JSON response
	analysis, err := a.parseResponse(response)
	if err != nil {
		return nil, fmt.Errorf("parsing AI response: %w", err)
	}

	analysis.GeneratedAt = time.Now()
	return analysis, nil
}

// prepareStockData formats stock results for the AI prompt.
func (a *Analyzer) prepareStockData(results []*models.StockResult) string {
	var sb strings.Builder

	// Group by category
	categories := make(map[string][]*models.StockResult)
	for _, r := range results {
		categories[r.Stock.Category] = append(categories[r.Stock.Category], r)
	}

	// Sort categories
	catNames := make([]string, 0, len(categories))
	for name := range categories {
		catNames = append(catNames, name)
	}
	sort.Strings(catNames)

	for _, catName := range catNames {
		sb.WriteString(fmt.Sprintf("\n## %s\n", catName))
		for _, r := range categories[catName] {
			status := ""
			if r.IsOversold() {
				status = " [OVERSOLD]"
			} else if r.IsOverbought() {
				status = " [OVERBOUGHT]"
			}

			changeSign := ""
			if r.ChangePercent > 0 {
				changeSign = "+"
			}

			earningsSuffix := ""
			if r.NextEarningsDate != nil {
				earningsSuffix = fmt.Sprintf(" | Résultats: %s", r.NextEarningsDate.Format("02 Jan 2006"))
			}
			sb.WriteString(fmt.Sprintf("- %s (%s): %.2f | %s%.2f%% | RSI: %.1f%s%s\n",
				r.Stock.Name, r.Stock.Ticker, r.CurrentPrice,
				changeSign, r.ChangePercent, r.RSI, status, earningsSuffix))
		}
	}

	return sb.String()
}

// parseResponse extracts the Analysis from the model response.
func (a *Analyzer) parseResponse(response string) (*Analysis, error) {
	// Try to extract JSON from the response
	response = strings.TrimSpace(response)

	// Remove markdown code blocks if present
	if strings.HasPrefix(response, "```json") {
		response = strings.TrimPrefix(response, "```json")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	} else if strings.HasPrefix(response, "```") {
		response = strings.TrimPrefix(response, "```")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)
	}

	var analysis Analysis
	if err := json.Unmarshal([]byte(response), &analysis); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w\nResponse was: %s", err, response[:min(500, len(response))])
	}

	return &analysis, nil
}
