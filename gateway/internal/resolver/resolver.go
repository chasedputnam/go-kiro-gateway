// Package resolver implements the dynamic model resolution pipeline for
// Kiro Gateway.
//
// The resolver normalizes client model names to Kiro format and resolves
// them through a 5-layer pipeline:
//
//  0. Resolve aliases — custom name mappings (e.g., "auto-kiro" → "auto")
//  1. Normalize name — dashes→dots, strip dates, handle inverted format
//  2. Check dynamic cache — models from /ListAvailableModels API
//  3. Check hidden models — manual config for undocumented models
//  4. Pass-through — unknown models sent to Kiro (let Kiro decide)
//
// Key principle: we are a gateway, not a gatekeeper. Kiro API is the
// final arbiter of what models exist.
package resolver

import (
	"regexp"
	"sort"
	"strings"

	"github.com/jwadow/kiro-gateway/gateway/internal/cache"
)

// Compiled regexp patterns for model name normalization. These are
// initialized once at package level for performance.
var (
	// standardPattern matches standard format with minor version:
	//   claude-{family}-{major}-{minor}(-{suffix})?
	// Examples: claude-haiku-4-5, claude-haiku-4-5-20251001, claude-haiku-4-5-latest
	// Minor version is 1-2 digits only so 8-digit dates do not match.
	standardPattern = regexp.MustCompile(
		`^(claude-(?:haiku|sonnet|opus)-\d+)-(\d{1,2})(?:-(?:\d{8}|latest|\d+))?$`,
	)

	// noMinorPattern matches standard format without minor version:
	//   claude-{family}-{major}(-{date})?
	// Examples: claude-sonnet-4, claude-sonnet-4-20250514
	noMinorPattern = regexp.MustCompile(
		`^(claude-(?:haiku|sonnet|opus)-\d+)(?:-\d{8})?$`,
	)

	// legacyPattern matches legacy inverted format:
	//   claude-{major}-{minor}-{family}(-{suffix})?
	// Examples: claude-3-7-sonnet, claude-3-7-sonnet-20250219
	legacyPattern = regexp.MustCompile(
		`^(claude)-(\d+)-(\d+)-(haiku|sonnet|opus)(?:-(?:\d{8}|latest|\d+))?$`,
	)

	// dotWithDatePattern matches already-normalized names with a date suffix:
	//   claude-{family}-{major}.{minor}-{date} or claude-{major}.{minor}-{family}-{date}
	// Examples: claude-haiku-4.5-20251001, claude-3.7-sonnet-20250219
	dotWithDatePattern = regexp.MustCompile(
		`^(claude-(?:\d+\.\d+-)?(?:haiku|sonnet|opus)(?:-\d+\.\d+)?)-\d{8}$`,
	)

	// invertedWithSuffixPattern matches inverted format with a required suffix:
	//   claude-{major}.{minor}-{family}-{suffix}
	// Examples: claude-4.5-opus-high, claude-4.5-sonnet-low
	// NOTE: suffix is REQUIRED to avoid matching already-normalized formats
	// like claude-3.7-sonnet.
	invertedWithSuffixPattern = regexp.MustCompile(
		`^claude-(\d+)\.(\d+)-(haiku|sonnet|opus)-(.+)$`,
	)

	// familyPattern extracts the model family from a model name.
	familyPattern = regexp.MustCompile(`(?i)(haiku|sonnet|opus)`)
)

// ModelResolution is the result of resolving an external model name.
type ModelResolution struct {
	// InternalID is the model ID to send to Kiro API.
	InternalID string
	// Source indicates where the model was resolved: "cache", "hidden",
	// or "passthrough".
	Source string
	// OriginalRequest is the model name the client originally sent.
	OriginalRequest string
	// Normalized is the model name after normalization.
	Normalized string
	// IsVerified is true when the model was found in cache or hidden
	// models, false for pass-through.
	IsVerified bool
}

// Resolver maps external model names to internal Kiro API model IDs.
type Resolver interface {
	// Resolve maps an external model name to a Kiro internal ID.
	// It never returns an error — unknown models are passed through.
	Resolve(externalModel string) ModelResolution

	// GetAvailableModels returns all model IDs suitable for the
	// /v1/models endpoint, filtering out hidden-from-list models.
	GetAvailableModels() []string
}

// Config holds the resolver's configuration data that is injected at
// construction time.
type Config struct {
	// HiddenModels maps display names (e.g., "claude-3.7-sonnet") to
	// internal Kiro IDs (e.g., "CLAUDE_3_7_SONNET_20250219_V1_0").
	HiddenModels map[string]string

	// Aliases maps custom alias names to real model IDs.
	// Example: {"auto-kiro": "auto", "my-opus": "claude-opus-4.5"}
	Aliases map[string]string

	// HiddenFromList contains model IDs that should be hidden from the
	// /v1/models endpoint but still work when requested directly.
	HiddenFromList []string
}

// modelResolver is the concrete implementation of Resolver.
type modelResolver struct {
	cache          cache.ModelCache
	hiddenModels   map[string]string
	aliases        map[string]string
	hiddenFromList map[string]struct{}
}

// New creates a new Resolver with the given cache and configuration.
// Nil maps in cfg are treated as empty.
func New(c cache.ModelCache, cfg Config) Resolver {
	hidden := cfg.HiddenModels
	if hidden == nil {
		hidden = make(map[string]string)
	}

	aliases := cfg.Aliases
	if aliases == nil {
		aliases = make(map[string]string)
	}

	hiddenSet := make(map[string]struct{}, len(cfg.HiddenFromList))
	for _, id := range cfg.HiddenFromList {
		hiddenSet[id] = struct{}{}
	}

	return &modelResolver{
		cache:          c,
		hiddenModels:   hidden,
		aliases:        aliases,
		hiddenFromList: hiddenSet,
	}
}

// NormalizeModelName converts a client model name to Kiro format.
//
// Transformations applied in order:
//  1. claude-haiku-4-5 → claude-haiku-4.5 (dash to dot for minor version)
//  2. claude-haiku-4-5-20251001 → claude-haiku-4.5 (strip date suffix)
//  3. claude-haiku-4-5-latest → claude-haiku-4.5 (strip latest suffix)
//  4. claude-sonnet-4-20250514 → claude-sonnet-4 (strip date, no minor)
//  5. claude-3-7-sonnet → claude-3.7-sonnet (legacy format normalization)
//  6. claude-3-7-sonnet-20250219 → claude-3.7-sonnet (legacy + strip date)
//  7. claude-4.5-opus-high → claude-opus-4.5 (inverted format with suffix)
func NormalizeModelName(name string) string {
	if name == "" {
		return name
	}

	lower := strings.ToLower(name)

	// Pattern 1: Standard format — claude-{family}-{major}-{minor}(-{suffix})?
	if m := standardPattern.FindStringSubmatch(lower); m != nil {
		// m[1] = "claude-haiku-4", m[2] = "5"
		return m[1] + "." + m[2]
	}

	// Pattern 2: Standard format without minor — claude-{family}-{major}(-{date})?
	if m := noMinorPattern.FindStringSubmatch(lower); m != nil {
		return m[1]
	}

	// Pattern 3: Legacy format — claude-{major}-{minor}-{family}(-{suffix})?
	if m := legacyPattern.FindStringSubmatch(lower); m != nil {
		// m[1] = "claude", m[2] = "3", m[3] = "7", m[4] = "sonnet"
		return m[1] + "-" + m[2] + "." + m[3] + "-" + m[4]
	}

	// Pattern 4: Already normalized with dot but has date suffix
	if m := dotWithDatePattern.FindStringSubmatch(lower); m != nil {
		return m[1]
	}

	// Pattern 5: Inverted format with suffix — claude-{major}.{minor}-{family}-{suffix}
	// NOTE: suffix is REQUIRED to avoid matching already-normalized formats.
	if m := invertedWithSuffixPattern.FindStringSubmatch(lower); m != nil {
		// m[1] = "4", m[2] = "5", m[3] = "opus"
		return "claude-" + m[3] + "-" + m[1] + "." + m[2]
	}

	// No transformation needed — return lowercased for consistency.
	return lower
}

// ExtractModelFamily returns the model family ("haiku", "sonnet", or
// "opus") from a model name, or empty string if not a Claude model.
func ExtractModelFamily(modelName string) string {
	m := familyPattern.FindStringSubmatch(modelName)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// Resolve maps an external model name to an internal Kiro API model ID.
//
// Resolution layers:
//
//  0. Resolve alias (if exists)
//  1. Normalize name (dashes→dots, strip date)
//  2. Check dynamic cache (from /ListAvailableModels)
//  3. Check hidden models (manual config)
//  4. Pass-through (let Kiro decide)
//
// Resolve never returns an error. If the model is not found in cache or
// hidden models, it is passed through to Kiro with is_verified=false.
func (r *modelResolver) Resolve(externalModel string) ModelResolution {
	// Layer 0: Resolve alias.
	resolved := externalModel
	if target, ok := r.aliases[externalModel]; ok {
		resolved = target
	}

	// Layer 1: Normalize name.
	normalized := NormalizeModelName(resolved)

	// Layer 2: Check dynamic cache.
	if r.cache.IsValidModel(normalized) {
		return ModelResolution{
			InternalID:      normalized,
			Source:          "cache",
			OriginalRequest: externalModel,
			Normalized:      normalized,
			IsVerified:      true,
		}
	}

	// Layer 3: Check hidden models.
	if internalID, ok := r.hiddenModels[normalized]; ok {
		return ModelResolution{
			InternalID:      internalID,
			Source:          "hidden",
			OriginalRequest: externalModel,
			Normalized:      normalized,
			IsVerified:      true,
		}
	}

	// Layer 4: Pass-through — let Kiro decide.
	return ModelResolution{
		InternalID:      normalized,
		Source:          "passthrough",
		OriginalRequest: externalModel,
		Normalized:      normalized,
		IsVerified:      false,
	}
}

// GetAvailableModels returns a sorted list of all model IDs for the
// /v1/models endpoint.
//
// The list combines:
//   - Models from the dynamic cache (Kiro API)
//   - Hidden model display names (manual config)
//   - Alias keys (custom mappings)
//
// Models in the hidden-from-list set are excluded.
func (r *modelResolver) GetAvailableModels() []string {
	models := make(map[string]struct{})

	// Add cache models.
	for _, id := range r.cache.GetAllModelIDs() {
		models[id] = struct{}{}
	}

	// Add hidden model display names.
	for displayName := range r.hiddenModels {
		models[displayName] = struct{}{}
	}

	// Remove models that should be hidden from list.
	for id := range r.hiddenFromList {
		delete(models, id)
	}

	// Add alias keys.
	for alias := range r.aliases {
		models[alias] = struct{}{}
	}

	result := make([]string, 0, len(models))
	for id := range models {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// GetModelsByFamily returns available models filtered by family name.
// Useful for error messages suggesting alternatives from the same family.
func (r *modelResolver) GetModelsByFamily(family string) []string {
	all := r.GetAvailableModels()
	lowerFamily := strings.ToLower(family)
	var filtered []string
	for _, m := range all {
		if strings.Contains(strings.ToLower(m), lowerFamily) {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// GetModelIDForKiro is a helper for converters that don't have access
// to the full Resolver. It normalizes the name and checks hidden models.
func GetModelIDForKiro(modelName string, hiddenModels map[string]string) string {
	normalized := NormalizeModelName(modelName)
	if internalID, ok := hiddenModels[normalized]; ok {
		return internalID
	}
	return normalized
}
