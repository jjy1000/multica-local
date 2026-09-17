package metrics

import (
	"regexp"
	"strings"
)

type ModelPrice struct {
	Provider       string
	Model          string
	InputPerM      float64
	CacheReadPerM  float64
	CacheWritePerM float64
	OutputPerM     float64
}

var modelPrices = map[string]ModelPrice{
	"openai:gpt-5.5":              {Provider: "openai", Model: "gpt-5.5", InputPerM: 5.00, CacheReadPerM: 0.50, CacheWritePerM: 0.50, OutputPerM: 30.00},
	"openai:gpt-5.4":              {Provider: "openai", Model: "gpt-5.4", InputPerM: 2.50, CacheReadPerM: 0.25, CacheWritePerM: 0.25, OutputPerM: 15.00},
	"openai:gpt-5.4-mini":         {Provider: "openai", Model: "gpt-5.4-mini", InputPerM: 0.75, CacheReadPerM: 0.075, CacheWritePerM: 0.075, OutputPerM: 4.50},
	"openai:gpt-5.3-codex":        {Provider: "openai", Model: "gpt-5.3-codex", InputPerM: 1.75, CacheReadPerM: 0.175, CacheWritePerM: 0.175, OutputPerM: 14.00},
	"openai:gpt-5.2-codex":        {Provider: "openai", Model: "gpt-5.2-codex", InputPerM: 1.75, CacheReadPerM: 0.175, CacheWritePerM: 0.175, OutputPerM: 14.00},
	"anthropic:claude-fable-5-1":  {Provider: "anthropic", Model: "claude-fable-5-1", InputPerM: 10.00, CacheReadPerM: 0.25, CacheWritePerM: 12.50, OutputPerM: 50.00},
	"anthropic:claude-fable-5":    {Provider: "anthropic", Model: "claude-fable-5", InputPerM: 10.00, CacheReadPerM: 1.00, CacheWritePerM: 12.50, OutputPerM: 50.00},
	"anthropic:claude-opus-4.8":   {Provider: "anthropic", Model: "claude-opus-4.8", InputPerM: 5.00, CacheReadPerM: 0.50, CacheWritePerM: 6.25, OutputPerM: 25.00},
	"anthropic:claude-opus-4.7":   {Provider: "anthropic", Model: "claude-opus-4.7", InputPerM: 5.00, CacheReadPerM: 0.50, CacheWritePerM: 6.25, OutputPerM: 25.00},
	"anthropic:claude-opus-4.6":   {Provider: "anthropic", Model: "claude-opus-4.6", InputPerM: 5.00, CacheReadPerM: 0.50, CacheWritePerM: 6.25, OutputPerM: 25.00},
	"anthropic:claude-opus-4.5":   {Provider: "anthropic", Model: "claude-opus-4.5", InputPerM: 5.00, CacheReadPerM: 0.50, CacheWritePerM: 6.25, OutputPerM: 25.00},
	"anthropic:claude-sonnet-4.6": {Provider: "anthropic", Model: "claude-sonnet-4.6", InputPerM: 3.00, CacheReadPerM: 0.30, CacheWritePerM: 3.75, OutputPerM: 15.00},
	"anthropic:claude-sonnet-4.5": {Provider: "anthropic", Model: "claude-sonnet-4.5", InputPerM: 3.00, CacheReadPerM: 0.30, CacheWritePerM: 3.75, OutputPerM: 15.00},
	"anthropic:claude-haiku-4.5":  {Provider: "anthropic", Model: "claude-haiku-4.5", InputPerM: 1.00, CacheReadPerM: 0.10, CacheWritePerM: 1.25, OutputPerM: 5.00},
	"deepseek:v4-pro":             {Provider: "deepseek", Model: "v4-pro", InputPerM: 1.74, CacheReadPerM: 0.0145, CacheWritePerM: 1.74, OutputPerM: 3.48},
	"deepseek:v4-flash":           {Provider: "deepseek", Model: "v4-flash", InputPerM: 0.56, CacheReadPerM: 0.0112, CacheWritePerM: 0.56, OutputPerM: 1.12},
	"minimax:m2.7":                {Provider: "minimax", Model: "m2.7", InputPerM: 0.30, CacheReadPerM: 0.06, CacheWritePerM: 0.375, OutputPerM: 1.20},
	"minimax:m2.7-highspeed":      {Provider: "minimax", Model: "m2.7-highspeed", InputPerM: 0.60, CacheReadPerM: 0.06, CacheWritePerM: 0.375, OutputPerM: 2.40},
	"google:gemini-3-flash":       {Provider: "google", Model: "gemini-3-flash", InputPerM: 0.50, CacheReadPerM: 0.05, CacheWritePerM: 0.50, OutputPerM: 3.00},
	"google:gemini-3.1-pro":       {Provider: "google", Model: "gemini-3.1-pro", InputPerM: 2.00, CacheReadPerM: 0.20, CacheWritePerM: 2.00, OutputPerM: 12.00},
	"google:gemini-2.5-pro":       {Provider: "google", Model: "gemini-2.5-pro", InputPerM: 1.25, CacheReadPerM: 0.31, CacheWritePerM: 1.25, OutputPerM: 10.00},
	"google:gemini-2.5-flash":     {Provider: "google", Model: "gemini-2.5-flash", InputPerM: 0.30, CacheReadPerM: 0.03, CacheWritePerM: 0.30, OutputPerM: 2.50},
}

// claudeVersionEnd terminates a Claude family rule: at most one suffix that
// the frontend resolver normalizes away, and then the END of the id. Appending
// it keeps a rule from swallowing a later SKU in the same family — without it
// `claude-fable-5` also matches `claude-fable-5-1`, whose cache reads are a
// quarter of Fable 5's, so those reads bill at 4x.
//
// The admitted suffixes are exactly what `stripContextTag` and `stripDate`
// remove in packages/views/runtimes/utils.ts before its exact-key lookup, so
// both sides accept the same suffix forms. (Only the suffixes: the Claude
// rules are still substring matches, so a malformed PREFIX is out of scope
// here.) The trailing `$` is what makes that true and is not optional: these rules are substring matches, so an
// alternative that merely starts a suffix still matches when arbitrary text
// follows it (`claude-fable-5-1-latest-preview`, `claude-fable-5-1[1m]junk`),
// which is the silent tier-borrowing this constant exists to prevent. The
// bracket form requires a complete tag for the same reason.
//
// A date snapshot carrying a context tag (`claude-fable-5-20260401[1m]`) is
// covered by the tag-stripping retry in PriceForModelAlias, so it does not
// need a combined alternative here.
//
// Anything else — another version digit, a `-preview`-style qualifier — is a
// distinct SKU at an unknown rate and stays unmapped until it gets a row of
// its own, the same "every catalog SKU needs its own row" rule the frontend
// table states.
const claudeVersionEnd = `(?:-20\d{6}|-20\d{2}-\d{2}-\d{2}|-latest|\[[^\]]+\])?$`

var modelAliasRules = []struct {
	re       *regexp.Regexp
	priceKey string
}{
	{regexp.MustCompile(`(^|/|:)gpt-5[.-]5$|^gpt-5-5$`), "openai:gpt-5.5"},
	{regexp.MustCompile(`(^|/|:)gpt-5[.-]4($|-2026-03-05|-xhigh)`), "openai:gpt-5.4"},
	{regexp.MustCompile(`(^|/|:)gpt-5[.-]4-mini($|[^a-z0-9])`), "openai:gpt-5.4-mini"},
	{regexp.MustCompile(`(^|/|:)gpt-5[.-]3-codex$`), "openai:gpt-5.3-codex"},
	{regexp.MustCompile(`(^|/|:)gpt-5[.-]2-codex$`), "openai:gpt-5.2-codex"},
	// Fable 5.1 shares Fable 5's $10 / $50 and $12.50 cache write but prices
	// cache reads at 0.025x input ($0.25) instead of the standard 0.1x, so it
	// needs its own row, and both rules end at their own version
	// (claudeVersionEnd) so neither can swallow the other's ids.
	{regexp.MustCompile(`claude-fable-5[-.]1` + claudeVersionEnd), "anthropic:claude-fable-5-1"},
	{regexp.MustCompile(`claude-fable-5` + claudeVersionEnd), "anthropic:claude-fable-5"},
	{regexp.MustCompile(`claude-opus-4[-.]8`), "anthropic:claude-opus-4.8"},
	{regexp.MustCompile(`claude-opus-4[-.]7`), "anthropic:claude-opus-4.7"},
	{regexp.MustCompile(`claude-opus-4[-.]6`), "anthropic:claude-opus-4.6"},
	{regexp.MustCompile(`claude-opus-4[-.]5`), "anthropic:claude-opus-4.5"},
	{regexp.MustCompile(`claude-sonnet-4[-.]6|claude-4[-.]6-sonnet`), "anthropic:claude-sonnet-4.6"},
	{regexp.MustCompile(`claude-sonnet-4[-.]5|claude-4[-.]5-sonnet`), "anthropic:claude-sonnet-4.5"},
	{regexp.MustCompile(`claude-haiku-4[-.]5`), "anthropic:claude-haiku-4.5"},
	{regexp.MustCompile(`deepseek-v4-pro`), "deepseek:v4-pro"},
	{regexp.MustCompile(`deepseek-v4-flash|^deepseek-chat$|^deepseek-reasoner$`), "deepseek:v4-flash"},
	{regexp.MustCompile(`minimax-m2[.]7.*highspeed|highspeed.*minimax-m2[.]7`), "minimax:m2.7-highspeed"},
	{regexp.MustCompile(`minimax-m2[.]7`), "minimax:m2.7"},
	{regexp.MustCompile(`gemini-3-flash`), "google:gemini-3-flash"},
	{regexp.MustCompile(`gemini-3[.]1-pro`), "google:gemini-3.1-pro"},
	{regexp.MustCompile(`gemini-2[.]5-pro`), "google:gemini-2.5-pro"},
	{regexp.MustCompile(`gemini-2[.]5-flash`), "google:gemini-2.5-flash"},
}

// contextTagRe matches a trailing context-window variant tag such as the
// `[1m]` Claude Code appends to the model id. A complete bracket tag with at
// least one character inside, anchored at the end — the same shape the
// frontend's `stripContextTag` strips (`\[[^\]]+\]$` in
// packages/views/runtimes/utils.ts), so empty tags (`model[]`) and non-tag
// trailing brackets (`model[`) stay unmapped on both sides.
var contextTagRe = regexp.MustCompile(`\[[^\]]+\]$`)

func matchModelAlias(model string) (ModelPrice, bool) {
	for _, rule := range modelAliasRules {
		if rule.re.MatchString(model) {
			price, ok := modelPrices[rule.priceKey]
			return price, ok
		}
	}
	return ModelPrice{}, false
}

func PriceForModelAlias(model string) (ModelPrice, bool) {
	model = strings.ToLower(strings.TrimSpace(model))
	if price, ok := matchModelAlias(model); ok {
		return price, true
	}
	// The raw id did not resolve: a harness-appended context-window tag
	// (`kimi-k3[1m]`, `grok-4.5[1m]`) is the same SKU at the same tier, so
	// retry against the bare id. The anchored Codex rules end at `$`, so
	// without this a bracketed variant would take the unpriced branch in
	// RecordLLMUsage. Only ever turns a miss into a hit — the raw form is
	// tried first, so an explicit bracketed rule still wins.
	//
	// Exactly ONE tag, matching the frontend: `canonicalCandidates` in
	// packages/views/runtimes/utils.ts strips a single trailing tag and does
	// not re-strip the result. Retrying a doubly-tagged id would peel `[2m]`
	// off `claude-fable-5[1m][2m]` and let the leftover `[1m]` satisfy a rule
	// that already, correctly, rejected the raw form — the dashboard leaves
	// that id unmapped, so pricing it here would put two different costs on
	// one usage row. A second tag means the id is not a shape we recognise.
	if stripped := contextTagRe.ReplaceAllString(model, ""); stripped != model {
		if contextTagRe.MatchString(stripped) {
			return ModelPrice{}, false
		}
		return matchModelAlias(stripped)
	}
	return ModelPrice{}, false
}

func tokenCostUSD(tokens int64, pricePerM float64) float64 {
	if tokens <= 0 || pricePerM <= 0 {
		return 0
	}
	return float64(tokens) * pricePerM / 1_000_000
}
