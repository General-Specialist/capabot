package llm

// tokenPricing stores per-million-token costs [input, output] in USD.
var tokenPricing = map[string][2]float64{
	// Anthropic
	"claude-sonnet-4-20250514":  {3, 15},
	"claude-sonnet-4-6":         {3, 15},
	"claude-haiku-4-20250414":   {0.80, 4},
	"claude-haiku-4-5-20251001": {0.80, 4},
	"claude-opus-4-20250514":    {15, 75},
	"claude-opus-4-6":           {15, 75},
	// OpenAI
	"gpt-4o":       {2.50, 10},
	"gpt-4o-mini":  {0.15, 0.60},
	"gpt-4.1":      {2, 8},
	"gpt-4.1-mini": {0.40, 1.60},
	"gpt-4.1-nano": {0.10, 0.40},
	"o3":           {2, 8},
	"o3-mini":      {1.10, 4.40},
	"o4-mini":      {1.10, 4.40},
	// Gemini
	"gemini-2.5-pro":                 {1.25, 10},
	"gemini-2.5-pro-preview-05-06":   {1.25, 10},
	"gemini-2.5-flash":               {0.15, 0.60},
	"gemini-2.5-flash-preview-04-17": {0.15, 0.60},
	"gemini-2.0-flash":               {0.10, 0.40},
	"gemini-2.0-flash-001":           {0.10, 0.40},
	"gemini-3-flash-preview":         {0.10, 0.40},
	// OpenRouter
	"anthropic/claude-sonnet-4-6": {3, 15},
	"anthropic/claude-opus-4-6":   {15, 75},
	"openai/gpt-4o":               {2.50, 10},
	"openai/gpt-4o-mini":          {0.15, 0.60},
	"google/gemini-2.0-flash-001": {0.10, 0.40},
}

// EstimateCost returns the estimated USD cost for the given model and token counts.
func EstimateCost(model string, inputTokens, outputTokens int) float64 {
	p, ok := tokenPricing[model]
	if !ok {
		return 0
	}
	return (float64(inputTokens)*p[0] + float64(outputTokens)*p[1]) / 1_000_000
}
