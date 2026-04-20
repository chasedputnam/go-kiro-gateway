// Package cache provides thread-safe model metadata storage for Kiro Gateway.
//
// The ModelCache stores model information fetched from the Kiro
// ListAvailableModels API. It supports configurable TTL for staleness
// detection, hidden model injection, and per-model maxInputTokens
// lookups used for context usage percentage calculations.
//
// All read operations use sync.RWMutex.RLock for concurrent access,
// and write operations use sync.RWMutex.Lock for exclusive access.
package cache

import (
	"sync"
	"time"

	"github.com/jwadow/kiro-gateway/gateway/internal/models"
)

// DefaultTTL is the default cache time-to-live (1 hour).
const DefaultTTL = time.Hour

// defaultMaxInputTokens is the fallback value when a model does not
// specify maxInputTokens. Matches the config default of 200000.
const defaultMaxInputTokens = 200000

// ModelCache defines the interface for model metadata storage.
// Implementations must be safe for concurrent use by multiple goroutines.
type ModelCache interface {
	// Update replaces the cache with new model data.
	Update(models []models.ModelInfo)

	// Get returns model info by ID, or nil if not found.
	Get(modelID string) *models.ModelInfo

	// IsValidModel checks if a model exists in the cache.
	IsValidModel(modelID string) bool

	// GetMaxInputTokens returns max input tokens for a model.
	// Returns the default (200000) when the model is not found or
	// has no maxInputTokens set.
	GetMaxInputTokens(modelID string) int

	// GetAllModelIDs returns all cached model IDs.
	GetAllModelIDs() []string

	// AddHiddenModel adds a hidden model to the cache. Hidden models
	// are not returned by the Kiro ListAvailableModels API but are
	// still functional. displayName is the model ID exposed to clients,
	// internalID is the Kiro-internal identifier.
	AddHiddenModel(displayName, internalID string)
}

// modelInfoCache is the concrete implementation of ModelCache.
type modelInfoCache struct {
	mu         sync.RWMutex
	models     map[string]models.ModelInfo
	lastUpdate time.Time
	ttl        time.Duration
}

// New creates a new ModelCache with the given TTL. If ttl is zero,
// DefaultTTL (1 hour) is used.
func New(ttl time.Duration) ModelCache {
	if ttl == 0 {
		ttl = DefaultTTL
	}
	return &modelInfoCache{
		models: make(map[string]models.ModelInfo),
		ttl:    ttl,
	}
}

// Update replaces all cached models with the provided slice. It records
// the current time as the last update timestamp.
func (c *modelInfoCache) Update(modelList []models.ModelInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	newModels := make(map[string]models.ModelInfo, len(modelList))
	for _, m := range modelList {
		newModels[m.ModelID] = m
	}
	c.models = newModels
	c.lastUpdate = time.Now()
}

// Get returns a pointer to the ModelInfo for the given ID, or nil if
// the model is not in the cache. The returned pointer is to a copy,
// so callers cannot mutate cache state.
func (c *modelInfoCache) Get(modelID string) *models.ModelInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	m, ok := c.models[modelID]
	if !ok {
		return nil
	}
	// Return a copy so callers cannot mutate cache contents.
	copy := m
	return &copy
}

// IsValidModel reports whether the given model ID exists in the cache.
func (c *modelInfoCache) IsValidModel(modelID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, ok := c.models[modelID]
	return ok
}

// GetMaxInputTokens returns the maxInputTokens value for the given model.
// If the model is not found or its MaxInputTokens is zero, the default
// value (200000) is returned.
func (c *modelInfoCache) GetMaxInputTokens(modelID string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	m, ok := c.models[modelID]
	if !ok || m.MaxInputTokens <= 0 {
		return defaultMaxInputTokens
	}
	return m.MaxInputTokens
}

// GetAllModelIDs returns a slice of all model IDs currently in the cache.
// The order is non-deterministic.
func (c *modelInfoCache) GetAllModelIDs() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	ids := make([]string, 0, len(c.models))
	for id := range c.models {
		ids = append(ids, id)
	}
	return ids
}

// AddHiddenModel inserts a hidden model into the cache. If a model with
// the same displayName already exists, it is not overwritten.
func (c *modelInfoCache) AddHiddenModel(displayName, internalID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.models[displayName]; exists {
		return
	}
	c.models[displayName] = models.ModelInfo{
		ModelID:        displayName,
		MaxInputTokens: defaultMaxInputTokens,
		DisplayName:    displayName,
	}
}

// IsStale reports whether the cache data is older than the configured TTL,
// or has never been updated.
func (c *modelInfoCache) IsStale() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.lastUpdate.IsZero() {
		return true
	}
	return time.Since(c.lastUpdate) > c.ttl
}

// LastUpdate returns the time of the most recent Update call. Returns
// the zero time if the cache has never been updated.
func (c *modelInfoCache) LastUpdate() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastUpdate
}

// Size returns the number of models in the cache.
func (c *modelInfoCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.models)
}
