package testutil

import (
	"context"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/repo"
)

type MakeMemoryOptions struct {
	ProjectID       *uuid.UUID
	ProjectTaskID   *uuid.UUID
	AgentID         *uuid.UUID
	MemoryType      string
	Scope           string
	Content         string
	ContentHash     string
	Embedding       []float32
	Confidence      float64
	UtilityScore    float64
	ExtractionScore *int
	Status          string
	Sensitivity     string
	TrustTier       float64
}

func MakeMemory(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID, opts MakeMemoryOptions) *repo.Memory {
	t.Helper()

	memoryType := strings.TrimSpace(opts.MemoryType)
	if memoryType == "" {
		memoryType = "semantic"
	}
	scope := strings.TrimSpace(opts.Scope)
	if scope == "" {
		scope = "org"
	}
	content := strings.TrimSpace(opts.Content)
	if content == "" {
		content = "Memory " + uuid.NewString()
	}
	contentHash := strings.TrimSpace(opts.ContentHash)
	if contentHash == "" {
		contentHash = uuid.NewString()
	}
	status := strings.TrimSpace(opts.Status)
	if status == "" {
		status = "active"
	}

	created, err := repo.NewMemoryRepo(db).Create(context.Background(), repo.Memory{
		OrganizationID:  orgID,
		ProjectID:       opts.ProjectID,
		ProjectTaskID:   opts.ProjectTaskID,
		AgentID:         opts.AgentID,
		MemoryType:      memoryType,
		Scope:           scope,
		Content:         content,
		ContentHash:     contentHash,
		Embedding:       append([]float32(nil), opts.Embedding...),
		Confidence:      opts.Confidence,
		UtilityScore:    opts.UtilityScore,
		ExtractionScore: opts.ExtractionScore,
		Status:          status,
		Sensitivity:     opts.Sensitivity,
		TrustTier:       opts.TrustTier,
	})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	return &created
}

func MakeMemoryEntity(t testing.TB, db *pgxpool.Pool, orgID uuid.UUID) uuid.UUID {
	t.Helper()

	entity, err := repo.NewMemoryEntityRepo(db).Create(context.Background(), repo.MemoryEntity{
		OrganizationID: orgID,
		CanonicalName:  "Entity " + uuid.NewString()[:8],
		EntityType:     "general",
	})
	if err != nil {
		t.Fatalf("create memory entity: %v", err)
	}
	return entity.ID
}

type MockLLMExtractorModel struct {
	Fixtures     []memory.ExtractedMemory
	RequestCount int
	LastRequest  memory.ExtractionRequest
}

func MockLLMExtractor(fixtures []memory.ExtractedMemory) *MockLLMExtractorModel {
	return &MockLLMExtractorModel{
		Fixtures: append([]memory.ExtractedMemory(nil), fixtures...),
	}
}

func (m *MockLLMExtractorModel) Extract(_ context.Context, req memory.ExtractionRequest) ([]memory.ExtractedMemory, error) {
	m.RequestCount++
	m.LastRequest = req
	return append([]memory.ExtractedMemory(nil), m.Fixtures...), nil
}

func RandomEmbedding(dims int) []float32 {
	if dims <= 0 {
		dims = 1536
	}
	rng := rand.New(rand.NewSource(42 + int64(dims)))
	vector := make([]float32, dims)
	var sumSquares float64
	for i := range vector {
		value := rng.Float64()*2 - 1
		vector[i] = float32(value)
		sumSquares += value * value
	}
	if sumSquares == 0 {
		vector[0] = 1
		return vector
	}
	norm := math.Sqrt(sumSquares)
	for i := range vector {
		vector[i] = float32(float64(vector[i]) / norm)
	}
	return vector
}
