package cache

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jwadow/kiro-gateway/gateway/internal/models"
)

// sampleModels returns a slice of ModelInfo used across tests.
func sampleModels() []models.ModelInfo {
	return []models.ModelInfo{
		{ModelID: "claude-sonnet-4", MaxInputTokens: 200000, DisplayName: "Claude Sonnet 4"},
		{ModelID: "claude-haiku-4.5", MaxInputTokens: 200000, DisplayName: "Claude Haiku 4.5"},
		{ModelID: "claude-opus-4.5", MaxInputTokens: 200000, DisplayName: "Claude Opus 4.5"},
	}
}

// --- New / initialization ---------------------------------------------------

func TestNew_DefaultTTL(t *testing.T) {
	c := New(0)
	mc := c.(*modelInfoCache)
	if mc.ttl != DefaultTTL {
		t.Errorf("expected default TTL %v, got %v", DefaultTTL, mc.ttl)
	}
}

func TestNew_CustomTTL(t *testing.T) {
	ttl := 2 * time.Hour
	c := New(ttl)
	mc := c.(*modelInfoCache)
	if mc.ttl != ttl {
		t.Errorf("expected TTL %v, got %v", ttl, mc.ttl)
	}
}

func TestNew_EmptyCache(t *testing.T) {
	c := New(0).(*modelInfoCache)
	if len(c.models) != 0 {
		t.Errorf("expected empty cache, got %d entries", len(c.models))
	}
	if !c.lastUpdate.IsZero() {
		t.Errorf("expected zero lastUpdate, got %v", c.lastUpdate)
	}
}

// --- Update -----------------------------------------------------------------

func TestUpdate_PopulatesCache(t *testing.T) {
	c := New(0)
	data := sampleModels()
	c.Update(data)

	mc := c.(*modelInfoCache)
	if mc.Size() != len(data) {
		t.Errorf("expected %d models, got %d", len(data), mc.Size())
	}
}

func TestUpdate_SetsLastUpdate(t *testing.T) {
	c := New(0)
	before := time.Now()
	c.Update(sampleModels())
	after := time.Now()

	mc := c.(*modelInfoCache)
	lu := mc.LastUpdate()
	if lu.Before(before) || lu.After(after) {
		t.Errorf("lastUpdate %v not between %v and %v", lu, before, after)
	}
}

func TestUpdate_ReplacesExistingData(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	newData := []models.ModelInfo{
		{ModelID: "new-model", MaxInputTokens: 50000, DisplayName: "New Model"},
	}
	c.Update(newData)

	mc := c.(*modelInfoCache)
	if mc.Size() != 1 {
		t.Errorf("expected 1 model after replacement, got %d", mc.Size())
	}
	if c.Get("claude-sonnet-4") != nil {
		t.Error("old model should not exist after replacement")
	}
	if c.Get("new-model") == nil {
		t.Error("new model should exist after replacement")
	}
}

func TestUpdate_EmptySliceClearsCache(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())
	c.Update([]models.ModelInfo{})

	mc := c.(*modelInfoCache)
	if mc.Size() != 0 {
		t.Errorf("expected empty cache after empty update, got %d", mc.Size())
	}
}

// --- Get --------------------------------------------------------------------

func TestGet_ReturnsModelInfo(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	m := c.Get("claude-sonnet-4")
	if m == nil {
		t.Fatal("expected model info, got nil")
	}
	if m.ModelID != "claude-sonnet-4" {
		t.Errorf("expected modelID claude-sonnet-4, got %s", m.ModelID)
	}
	if m.MaxInputTokens != 200000 {
		t.Errorf("expected maxInputTokens 200000, got %d", m.MaxInputTokens)
	}
}

func TestGet_ReturnsNilForUnknown(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	if c.Get("non-existent") != nil {
		t.Error("expected nil for unknown model")
	}
}

func TestGet_ReturnsNilFromEmptyCache(t *testing.T) {
	c := New(0)
	if c.Get("any-model") != nil {
		t.Error("expected nil from empty cache")
	}
}

func TestGet_ReturnsCopy(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	m := c.Get("claude-sonnet-4")
	m.MaxInputTokens = 999

	// Original cache entry should be unchanged.
	m2 := c.Get("claude-sonnet-4")
	if m2.MaxInputTokens != 200000 {
		t.Errorf("Get should return a copy; cache was mutated to %d", m2.MaxInputTokens)
	}
}

// --- IsValidModel -----------------------------------------------------------

func TestIsValidModel_True(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	if !c.IsValidModel("claude-sonnet-4") {
		t.Error("expected true for existing model")
	}
}

func TestIsValidModel_False(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	if c.IsValidModel("unknown-model") {
		t.Error("expected false for unknown model")
	}
}

func TestIsValidModel_EmptyCache(t *testing.T) {
	c := New(0)
	if c.IsValidModel("any") {
		t.Error("expected false on empty cache")
	}
}

// --- GetMaxInputTokens ------------------------------------------------------

func TestGetMaxInputTokens_ReturnsValue(t *testing.T) {
	c := New(0)
	c.Update([]models.ModelInfo{
		{ModelID: "big-model", MaxInputTokens: 500000, DisplayName: "Big"},
	})

	got := c.GetMaxInputTokens("big-model")
	if got != 500000 {
		t.Errorf("expected 500000, got %d", got)
	}
}

func TestGetMaxInputTokens_DefaultForUnknown(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	got := c.GetMaxInputTokens("unknown-model")
	if got != defaultMaxInputTokens {
		t.Errorf("expected default %d, got %d", defaultMaxInputTokens, got)
	}
}

func TestGetMaxInputTokens_DefaultForZeroValue(t *testing.T) {
	c := New(0)
	c.Update([]models.ModelInfo{
		{ModelID: "zero-model", MaxInputTokens: 0, DisplayName: "Zero"},
	})

	got := c.GetMaxInputTokens("zero-model")
	if got != defaultMaxInputTokens {
		t.Errorf("expected default %d for zero maxInputTokens, got %d", defaultMaxInputTokens, got)
	}
}

func TestGetMaxInputTokens_DefaultForNegativeValue(t *testing.T) {
	c := New(0)
	c.Update([]models.ModelInfo{
		{ModelID: "neg-model", MaxInputTokens: -1, DisplayName: "Neg"},
	})

	got := c.GetMaxInputTokens("neg-model")
	if got != defaultMaxInputTokens {
		t.Errorf("expected default %d for negative maxInputTokens, got %d", defaultMaxInputTokens, got)
	}
}

func TestGetMaxInputTokens_EmptyCache(t *testing.T) {
	c := New(0)
	got := c.GetMaxInputTokens("any")
	if got != defaultMaxInputTokens {
		t.Errorf("expected default %d from empty cache, got %d", defaultMaxInputTokens, got)
	}
}

// --- GetAllModelIDs ---------------------------------------------------------

func TestGetAllModelIDs_EmptyCache(t *testing.T) {
	c := New(0)
	ids := c.GetAllModelIDs()
	if len(ids) != 0 {
		t.Errorf("expected empty slice, got %v", ids)
	}
}

func TestGetAllModelIDs_ReturnsAllIDs(t *testing.T) {
	c := New(0)
	data := sampleModels()
	c.Update(data)

	ids := c.GetAllModelIDs()
	sort.Strings(ids)

	expected := []string{"claude-haiku-4.5", "claude-opus-4.5", "claude-sonnet-4"}
	sort.Strings(expected)

	if len(ids) != len(expected) {
		t.Fatalf("expected %d IDs, got %d", len(expected), len(ids))
	}
	for i := range expected {
		if ids[i] != expected[i] {
			t.Errorf("expected ID %s at index %d, got %s", expected[i], i, ids[i])
		}
	}
}

// --- AddHiddenModel ---------------------------------------------------------

func TestAddHiddenModel_AddsNewModel(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	c.AddHiddenModel("claude-3.7-sonnet", "CLAUDE_3_7_SONNET_V1")

	m := c.Get("claude-3.7-sonnet")
	if m == nil {
		t.Fatal("expected hidden model to be added")
	}
	if m.ModelID != "claude-3.7-sonnet" {
		t.Errorf("expected modelID claude-3.7-sonnet, got %s", m.ModelID)
	}
	if m.MaxInputTokens != defaultMaxInputTokens {
		t.Errorf("expected default maxInputTokens %d, got %d", defaultMaxInputTokens, m.MaxInputTokens)
	}
}

func TestAddHiddenModel_DoesNotOverwriteExisting(t *testing.T) {
	c := New(0)
	c.Update([]models.ModelInfo{
		{ModelID: "claude-sonnet-4", MaxInputTokens: 200000, DisplayName: "Claude Sonnet 4"},
	})

	c.AddHiddenModel("claude-sonnet-4", "SOME_INTERNAL_ID")

	m := c.Get("claude-sonnet-4")
	if m == nil {
		t.Fatal("model should still exist")
	}
	// Original display name should be preserved.
	if m.DisplayName != "Claude Sonnet 4" {
		t.Errorf("expected original DisplayName, got %s", m.DisplayName)
	}
}

func TestAddHiddenModel_IsValidModel(t *testing.T) {
	c := New(0)
	c.AddHiddenModel("hidden-model", "INTERNAL_ID")

	if !c.IsValidModel("hidden-model") {
		t.Error("hidden model should be valid")
	}
}

func TestAddHiddenModel_AppearsInGetAllModelIDs(t *testing.T) {
	c := New(0)
	c.AddHiddenModel("hidden-model", "INTERNAL_ID")

	ids := c.GetAllModelIDs()
	if len(ids) != 1 || ids[0] != "hidden-model" {
		t.Errorf("expected [hidden-model], got %v", ids)
	}
}

// --- IsStale ----------------------------------------------------------------

func TestIsStale_TrueForNewCache(t *testing.T) {
	c := New(0).(*modelInfoCache)
	if !c.IsStale() {
		t.Error("new cache should be stale")
	}
}

func TestIsStale_FalseAfterUpdate(t *testing.T) {
	c := New(time.Hour).(*modelInfoCache)
	c.Update(sampleModels())

	if c.IsStale() {
		t.Error("recently updated cache should not be stale")
	}
}

func TestIsStale_TrueAfterTTLExpires(t *testing.T) {
	c := New(10 * time.Millisecond).(*modelInfoCache)
	c.Update(sampleModels())

	time.Sleep(20 * time.Millisecond)

	if !c.IsStale() {
		t.Error("cache should be stale after TTL expires")
	}
}

// --- Thread safety ----------------------------------------------------------

func TestConcurrentReads(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := c.Get("claude-sonnet-4")
			if m == nil || m.ModelID != "claude-sonnet-4" {
				t.Errorf("concurrent read returned unexpected result")
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentUpdates(t *testing.T) {
	c := New(0)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			data := []models.ModelInfo{
				{ModelID: "model", MaxInputTokens: i * 1000, DisplayName: "Model"},
			}
			c.Update(data)
		}()
	}
	wg.Wait()

	// After all concurrent updates, cache should have exactly 1 model.
	mc := c.(*modelInfoCache)
	if mc.Size() != 1 {
		t.Errorf("expected 1 model after concurrent updates, got %d", mc.Size())
	}
}

func TestConcurrentReadsDuringUpdate(t *testing.T) {
	c := New(0)
	c.Update(sampleModels())

	var wg sync.WaitGroup

	// Readers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Get("claude-sonnet-4")
			_ = c.IsValidModel("claude-sonnet-4")
			_ = c.GetMaxInputTokens("claude-sonnet-4")
			_ = c.GetAllModelIDs()
		}()
	}

	// Writers.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Update(sampleModels())
		}()
	}

	wg.Wait()
	// No race detector failures = pass.
}

func TestConcurrentAddHiddenModel(t *testing.T) {
	c := New(0)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.AddHiddenModel("hidden", "INTERNAL")
		}()
	}
	wg.Wait()

	if !c.IsValidModel("hidden") {
		t.Error("hidden model should exist after concurrent adds")
	}
}
