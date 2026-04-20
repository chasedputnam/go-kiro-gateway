package resolver

import (
	"sort"
	"testing"

	"github.com/jwadow/kiro-gateway/gateway/internal/cache"
	"github.com/jwadow/kiro-gateway/gateway/internal/models"
)

// --- helpers ----------------------------------------------------------------

// newPopulatedCache returns a ModelCache pre-loaded with common test models.
func newPopulatedCache() cache.ModelCache {
	c := cache.New(0)
	c.Update([]models.ModelInfo{
		{ModelID: "auto", MaxInputTokens: 200000, DisplayName: "Auto"},
		{ModelID: "claude-sonnet-4.5", MaxInputTokens: 200000, DisplayName: "Claude Sonnet 4.5"},
		{ModelID: "claude-sonnet-4", MaxInputTokens: 200000, DisplayName: "Claude Sonnet 4"},
		{ModelID: "claude-haiku-4.5", MaxInputTokens: 200000, DisplayName: "Claude Haiku 4.5"},
		{ModelID: "claude-opus-4.5", MaxInputTokens: 200000, DisplayName: "Claude Opus 4.5"},
	})
	return c
}

func newTestResolver() Resolver {
	return New(newPopulatedCache(), Config{
		HiddenModels: map[string]string{
			"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0",
		},
	})
}

// --- NormalizeModelName -----------------------------------------------------

func TestNormalizeModelName_StandardWithMinor(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-haiku-4-5", "claude-haiku-4.5"},
		{"claude-sonnet-4-5", "claude-sonnet-4.5"},
		{"claude-opus-4-5", "claude-opus-4.5"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeModelName_StripDateSuffix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-haiku-4-5-20251001", "claude-haiku-4.5"},
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4.5"},
		{"claude-opus-4-5-20251101", "claude-opus-4.5"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeModelName_StripLatestSuffix(t *testing.T) {
	got := NormalizeModelName("claude-haiku-4-5-latest")
	if got != "claude-haiku-4.5" {
		t.Errorf("NormalizeModelName(claude-haiku-4-5-latest) = %q, want %q", got, "claude-haiku-4.5")
	}
}

func TestNormalizeModelName_WithoutMinorVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-sonnet-4", "claude-sonnet-4"},
		{"claude-haiku-4", "claude-haiku-4"},
		{"claude-opus-4", "claude-opus-4"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeModelName_StripDateWithoutMinor(t *testing.T) {
	got := NormalizeModelName("claude-sonnet-4-20250514")
	if got != "claude-sonnet-4" {
		t.Errorf("NormalizeModelName(claude-sonnet-4-20250514) = %q, want %q", got, "claude-sonnet-4")
	}
}

func TestNormalizeModelName_LegacyFormat(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-3-7-sonnet", "claude-3.7-sonnet"},
		{"claude-3-7-sonnet-20250219", "claude-3.7-sonnet"},
		{"claude-3-5-haiku", "claude-3.5-haiku"},
		{"claude-3-0-opus", "claude-3.0-opus"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeModelName_InvertedFormatWithSuffix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-4.5-opus-high", "claude-opus-4.5"},
		{"claude-4.5-sonnet-low", "claude-sonnet-4.5"},
		{"claude-4.5-haiku-high", "claude-haiku-4.5"},
		{"claude-4.5-opus-high-thinking", "claude-opus-4.5"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeModelName_InvertedRequiresSuffix(t *testing.T) {
	// These should NOT match the inverted pattern because they lack a suffix.
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-3.7-sonnet", "claude-3.7-sonnet"},
		{"claude-4.5-sonnet", "claude-4.5-sonnet"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q (should not match inverted pattern)", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeModelName_CaseInsensitive(t *testing.T) {
	got := NormalizeModelName("CLAUDE-4.5-OPUS-HIGH")
	if got != "claude-opus-4.5" {
		t.Errorf("NormalizeModelName(CLAUDE-4.5-OPUS-HIGH) = %q, want %q", got, "claude-opus-4.5")
	}
}

func TestNormalizeModelName_AlreadyNormalized(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-haiku-4.5", "claude-haiku-4.5"},
		{"claude-sonnet-4.5", "claude-sonnet-4.5"},
		{"claude-opus-4.5", "claude-opus-4.5"},
		{"claude-3.7-sonnet", "claude-3.7-sonnet"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeModelName_Passthrough(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"auto", "auto"},
		{"gpt-4", "gpt-4"},
		{"gpt-4-turbo", "gpt-4-turbo"},
		{"some-random-model", "some-random-model"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeModelName_EmptyString(t *testing.T) {
	got := NormalizeModelName("")
	if got != "" {
		t.Errorf("NormalizeModelName(\"\") = %q, want \"\"", got)
	}
}

func TestNormalizeModelName_DotWithDateSuffix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-haiku-4.5-20251001", "claude-haiku-4.5"},
		{"claude-3.7-sonnet-20250219", "claude-3.7-sonnet"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// --- ExtractModelFamily -----------------------------------------------------

func TestExtractModelFamily(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"claude-haiku-4.5", "haiku"},
		{"claude-sonnet-4.5", "sonnet"},
		{"claude-opus-4.5", "opus"},
		{"claude-3.7-sonnet", "sonnet"},
		{"claude-haiku-4-5-20251001", "haiku"},
		{"CLAUDE-HAIKU-4.5", "haiku"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ExtractModelFamily(tt.input)
			if got != tt.expected {
				t.Errorf("ExtractModelFamily(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractModelFamily_NonClaude(t *testing.T) {
	tests := []string{"gpt-4", "auto", "unknown-model"}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			got := ExtractModelFamily(input)
			if got != "" {
				t.Errorf("ExtractModelFamily(%q) = %q, want \"\"", input, got)
			}
		})
	}
}

// --- Resolve ----------------------------------------------------------------

func TestResolve_FindsModelInCache(t *testing.T) {
	r := newTestResolver()
	res := r.Resolve("claude-haiku-4-5")

	if res.InternalID != "claude-haiku-4.5" {
		t.Errorf("InternalID = %q, want %q", res.InternalID, "claude-haiku-4.5")
	}
	if res.Source != "cache" {
		t.Errorf("Source = %q, want %q", res.Source, "cache")
	}
	if !res.IsVerified {
		t.Error("IsVerified = false, want true")
	}
	if res.Normalized != "claude-haiku-4.5" {
		t.Errorf("Normalized = %q, want %q", res.Normalized, "claude-haiku-4.5")
	}
	if res.OriginalRequest != "claude-haiku-4-5" {
		t.Errorf("OriginalRequest = %q, want %q", res.OriginalRequest, "claude-haiku-4-5")
	}
}

func TestResolve_FindsModelInHidden(t *testing.T) {
	r := newTestResolver()
	res := r.Resolve("claude-3-7-sonnet")

	if res.InternalID != "CLAUDE_3_7_SONNET_20250219_V1_0" {
		t.Errorf("InternalID = %q, want %q", res.InternalID, "CLAUDE_3_7_SONNET_20250219_V1_0")
	}
	if res.Source != "hidden" {
		t.Errorf("Source = %q, want %q", res.Source, "hidden")
	}
	if !res.IsVerified {
		t.Error("IsVerified = false, want true")
	}
}

func TestResolve_PassthroughForUnknown(t *testing.T) {
	r := newTestResolver()
	res := r.Resolve("claude-haiku-4-6")

	if res.InternalID != "claude-haiku-4.6" {
		t.Errorf("InternalID = %q, want %q", res.InternalID, "claude-haiku-4.6")
	}
	if res.Source != "passthrough" {
		t.Errorf("Source = %q, want %q", res.Source, "passthrough")
	}
	if res.IsVerified {
		t.Error("IsVerified = true, want false")
	}
}

func TestResolve_NormalizesBeforeLookup(t *testing.T) {
	r := newTestResolver()
	res := r.Resolve("claude-haiku-4-5-20251001")

	if res.Normalized != "claude-haiku-4.5" {
		t.Errorf("Normalized = %q, want %q", res.Normalized, "claude-haiku-4.5")
	}
	if res.Source != "cache" {
		t.Errorf("Source = %q, want %q", res.Source, "cache")
	}
}

func TestResolve_CacheTakesPriorityOverHidden(t *testing.T) {
	// If a model exists in both cache and hidden, cache wins.
	c := cache.New(0)
	c.Update([]models.ModelInfo{
		{ModelID: "claude-3.7-sonnet", MaxInputTokens: 200000, DisplayName: "Claude 3.7 Sonnet"},
	})
	r := New(c, Config{
		HiddenModels: map[string]string{
			"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_INTERNAL",
		},
	})

	res := r.Resolve("claude-3-7-sonnet")
	if res.Source != "cache" {
		t.Errorf("Source = %q, want %q (cache should take priority over hidden)", res.Source, "cache")
	}
	if res.InternalID != "claude-3.7-sonnet" {
		t.Errorf("InternalID = %q, want %q", res.InternalID, "claude-3.7-sonnet")
	}
}

func TestResolve_NeverPanics(t *testing.T) {
	r := newTestResolver()
	// These should all return without panicking.
	inputs := []string{
		"", "a", "claude", "claude-", "claude-haiku-",
		"claude-haiku-4-5-20251001-extra", "gpt-4",
		"some-random-model", "UPPERCASE-MODEL",
	}
	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			res := r.Resolve(input)
			if res.Source == "" {
				t.Error("Source should never be empty")
			}
		})
	}
}

// --- Alias resolution -------------------------------------------------------

func TestResolve_AliasResolution(t *testing.T) {
	r := New(newPopulatedCache(), Config{
		Aliases: map[string]string{
			"auto-kiro": "auto",
			"my-opus":   "claude-opus-4-5",
		},
	})

	// "auto-kiro" → alias resolves to "auto" → found in cache
	res := r.Resolve("auto-kiro")
	if res.InternalID != "auto" {
		t.Errorf("InternalID = %q, want %q", res.InternalID, "auto")
	}
	if res.Source != "cache" {
		t.Errorf("Source = %q, want %q", res.Source, "cache")
	}
	if res.OriginalRequest != "auto-kiro" {
		t.Errorf("OriginalRequest = %q, want %q", res.OriginalRequest, "auto-kiro")
	}
}

func TestResolve_AliasWithNormalization(t *testing.T) {
	r := New(newPopulatedCache(), Config{
		Aliases: map[string]string{
			"my-opus": "claude-opus-4-5",
		},
	})

	// "my-opus" → alias resolves to "claude-opus-4-5" → normalized to "claude-opus-4.5" → found in cache
	res := r.Resolve("my-opus")
	if res.InternalID != "claude-opus-4.5" {
		t.Errorf("InternalID = %q, want %q", res.InternalID, "claude-opus-4.5")
	}
	if res.Source != "cache" {
		t.Errorf("Source = %q, want %q", res.Source, "cache")
	}
}

func TestResolve_NonAliasPassesThrough(t *testing.T) {
	r := New(newPopulatedCache(), Config{
		Aliases: map[string]string{
			"auto-kiro": "auto",
		},
	})

	// "auto" is not an alias, but it is in cache.
	res := r.Resolve("auto")
	if res.InternalID != "auto" {
		t.Errorf("InternalID = %q, want %q", res.InternalID, "auto")
	}
	if res.Source != "cache" {
		t.Errorf("Source = %q, want %q", res.Source, "cache")
	}
}

// --- GetAvailableModels -----------------------------------------------------

func TestGetAvailableModels_CombinesCacheAndHidden(t *testing.T) {
	r := newTestResolver()
	models := r.GetAvailableModels()

	// Should include cache models + hidden model display name.
	expected := []string{
		"auto",
		"claude-3.7-sonnet",
		"claude-haiku-4.5",
		"claude-opus-4.5",
		"claude-sonnet-4",
		"claude-sonnet-4.5",
	}

	if len(models) != len(expected) {
		t.Fatalf("got %d models, want %d: %v", len(models), len(expected), models)
	}
	for i, m := range models {
		if m != expected[i] {
			t.Errorf("models[%d] = %q, want %q", i, m, expected[i])
		}
	}
}

func TestGetAvailableModels_HiddenFromList(t *testing.T) {
	r := New(newPopulatedCache(), Config{
		Aliases: map[string]string{
			"auto-kiro": "auto",
		},
		HiddenFromList: []string{"auto"},
	})

	models := r.GetAvailableModels()

	// "auto" should be hidden, but "auto-kiro" alias should be present.
	for _, m := range models {
		if m == "auto" {
			t.Error("auto should be hidden from list")
		}
	}

	found := false
	for _, m := range models {
		if m == "auto-kiro" {
			found = true
			break
		}
	}
	if !found {
		t.Error("auto-kiro alias should be in the list")
	}
}

func TestGetAvailableModels_IncludesAliases(t *testing.T) {
	r := New(newPopulatedCache(), Config{
		Aliases: map[string]string{
			"my-model": "claude-sonnet-4",
		},
	})

	models := r.GetAvailableModels()
	found := false
	for _, m := range models {
		if m == "my-model" {
			found = true
			break
		}
	}
	if !found {
		t.Error("alias 'my-model' should appear in available models")
	}
}

func TestGetAvailableModels_Sorted(t *testing.T) {
	r := newTestResolver()
	models := r.GetAvailableModels()

	sorted := make([]string, len(models))
	copy(sorted, models)
	sort.Strings(sorted)

	for i := range models {
		if models[i] != sorted[i] {
			t.Errorf("models not sorted: got %v", models)
			break
		}
	}
}

func TestGetAvailableModels_EmptyCache(t *testing.T) {
	c := cache.New(0)
	r := New(c, Config{})
	models := r.GetAvailableModels()
	if len(models) != 0 {
		t.Errorf("expected empty list, got %v", models)
	}
}

// --- GetModelIDForKiro helper -----------------------------------------------

func TestGetModelIDForKiro_NormalizesWithoutHidden(t *testing.T) {
	got := GetModelIDForKiro("claude-haiku-4-5-20251001", nil)
	if got != "claude-haiku-4.5" {
		t.Errorf("GetModelIDForKiro = %q, want %q", got, "claude-haiku-4.5")
	}
}

func TestGetModelIDForKiro_ReturnsInternalIDForHidden(t *testing.T) {
	hidden := map[string]string{
		"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0",
	}
	got := GetModelIDForKiro("claude-3.7-sonnet", hidden)
	if got != "CLAUDE_3_7_SONNET_20250219_V1_0" {
		t.Errorf("GetModelIDForKiro = %q, want %q", got, "CLAUDE_3_7_SONNET_20250219_V1_0")
	}
}

func TestGetModelIDForKiro_NormalizesThenChecksHidden(t *testing.T) {
	hidden := map[string]string{
		"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0",
	}
	got := GetModelIDForKiro("claude-3-7-sonnet", hidden)
	if got != "CLAUDE_3_7_SONNET_20250219_V1_0" {
		t.Errorf("GetModelIDForKiro = %q, want %q", got, "CLAUDE_3_7_SONNET_20250219_V1_0")
	}
}

func TestGetModelIDForKiro_PassthroughUnknown(t *testing.T) {
	got := GetModelIDForKiro("claude-unknown-model", nil)
	if got != "claude-unknown-model" {
		t.Errorf("GetModelIDForKiro = %q, want %q", got, "claude-unknown-model")
	}
}

// --- Comprehensive parametrized normalization test --------------------------

func TestNormalizeModelName_Comprehensive(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// Standard format with minor version
		{"claude-haiku-4-5", "claude-haiku-4.5"},
		{"claude-haiku-4-5-20251001", "claude-haiku-4.5"},
		{"claude-haiku-4-5-latest", "claude-haiku-4.5"},
		{"claude-sonnet-4-5", "claude-sonnet-4.5"},
		{"claude-sonnet-4-5-20250929", "claude-sonnet-4.5"},
		{"claude-opus-4-5", "claude-opus-4.5"},
		{"claude-opus-4-5-20251101", "claude-opus-4.5"},
		// Without minor version
		{"claude-sonnet-4", "claude-sonnet-4"},
		{"claude-sonnet-4-20250514", "claude-sonnet-4"},
		{"claude-haiku-4", "claude-haiku-4"},
		{"claude-opus-4", "claude-opus-4"},
		// Legacy format
		{"claude-3-7-sonnet", "claude-3.7-sonnet"},
		{"claude-3-7-sonnet-20250219", "claude-3.7-sonnet"},
		{"claude-3-5-haiku", "claude-3.5-haiku"},
		{"claude-3-0-opus", "claude-3.0-opus"},
		// Already normalized
		{"claude-haiku-4.5", "claude-haiku-4.5"},
		{"claude-sonnet-4.5", "claude-sonnet-4.5"},
		{"claude-opus-4.5", "claude-opus-4.5"},
		{"claude-3.7-sonnet", "claude-3.7-sonnet"},
		{"auto", "auto"},
		// Passthrough for unknown
		{"gpt-4", "gpt-4"},
		{"gpt-4-turbo", "gpt-4-turbo"},
		{"unknown-model", "unknown-model"},
		// Inverted format with suffix
		{"claude-4.5-opus-high", "claude-opus-4.5"},
		{"claude-4.5-sonnet-low", "claude-sonnet-4.5"},
		{"claude-4.5-haiku-high", "claude-haiku-4.5"},
		{"claude-4.5-opus-high-thinking", "claude-opus-4.5"},
		// Dot with date suffix
		{"claude-haiku-4.5-20251001", "claude-haiku-4.5"},
		{"claude-3.7-sonnet-20250219", "claude-3.7-sonnet"},
		// Empty
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeModelName(tt.input)
			if got != tt.expected {
				t.Errorf("NormalizeModelName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
