package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"mime"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/samhotchkiss/otter-camp/internal/api"
	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
	"github.com/samhotchkiss/otter-camp/internal/memory"
	"github.com/samhotchkiss/otter-camp/internal/memory/compaction"
	memoryimporter "github.com/samhotchkiss/otter-camp/internal/memory/importer"
	"github.com/samhotchkiss/otter-camp/internal/middleware"
	"github.com/samhotchkiss/otter-camp/internal/repo"
	"github.com/samhotchkiss/otter-camp/internal/storage"
)

const (
	memoryDefaultListLimit       = 50
	memoryMaxListLimit           = 200
	memoryDefaultMaxResults      = 20
	memoryQueryMaxResults        = 200
	memoryQueryMaxContentChars   = 2000
	memoryImportMaxCompressedZip = 100 * 1024 * 1024
)

type memoryRetriever interface {
	Query(ctx context.Context, req memory.RetrievalRequest) (memory.RetrievalResult, error)
}

type memoryImporter interface {
	StartImport(ctx context.Context, orgID uuid.UUID, requestedBy uuid.UUID, fileKey string) (uuid.UUID, error)
}

type memoryRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Memory, error)
}

type memoryTaxonomyTagRepository interface {
	ListByMemory(ctx context.Context, memoryID uuid.UUID) ([]repo.MemoryTaxonomyTag, error)
}

type memoryEntityMentionRepository interface {
	ListByMemory(ctx context.Context, memoryID uuid.UUID) ([]repo.MemoryEntityMention, error)
}

type memoryEntityRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.MemoryEntity, error)
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]repo.MemoryEntity, error)
}

type memoryTaxonomyNodeRepository interface {
	ListByOrganization(ctx context.Context, orgID uuid.UUID) ([]repo.MemoryTaxonomyNode, error)
}

type memoryImportRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.MemoryImport, error)
}

type memoryCompactionRunRepository interface {
	Create(ctx context.Context, item repo.MemoryCompactionRun) (repo.MemoryCompactionRun, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string, errorMessage *string, startedAt, completedAt *time.Time) (repo.MemoryCompactionRun, error)
}

type memoryProjectTaskRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.ProjectTask, error)
}

type memoryAgentRepository interface {
	GetByID(ctx context.Context, id uuid.UUID) (repo.Agent, error)
}

type supersessionChainReader interface {
	GetSupersessionChain(ctx context.Context, memoryID uuid.UUID) ([]repo.Memory, error)
}

type memoryJobEnqueuer interface {
	Enqueue(ctx context.Context, tx pgx.Tx, jobType string, priority int, payload any, runAfter *time.Time) (uuid.UUID, error)
}

type MemoryRouteOptions struct {
	Pool           *pgxpool.Pool
	Retriever      memoryRetriever
	Importer       memoryImporter
	Store          storage.Store
	Imports        memoryImportRepository
	Runs           memoryCompactionRunRepository
	Tasks          memoryProjectTaskRepository
	Agents         memoryAgentRepository
	Memories       memoryRepository
	TaxonomyTags   memoryTaxonomyTagRepository
	Mentions       memoryEntityMentionRepository
	Entities       memoryEntityRepository
	TaxonomyNodes  memoryTaxonomyNodeRepository
	Supersessions  supersessionChainReader
	Enqueuer       memoryJobEnqueuer
	MaxUploadBytes int64
	TestMode       bool
}

type MemoryRouteRegistrar struct {
	handlers memoryHandlers
}

func NewMemoryRouteRegistrar(opts MemoryRouteOptions) *MemoryRouteRegistrar {
	h := memoryHandlers{
		pool:          opts.Pool,
		retriever:     opts.Retriever,
		importer:      opts.Importer,
		store:         opts.Store,
		imports:       opts.Imports,
		runs:          opts.Runs,
		tasks:         opts.Tasks,
		agents:        opts.Agents,
		memories:      opts.Memories,
		taxonomyTags:  opts.TaxonomyTags,
		mentions:      opts.Mentions,
		entities:      opts.Entities,
		taxonomyNodes: opts.TaxonomyNodes,
		supersessions: opts.Supersessions,
		enqueuer:      opts.Enqueuer,
		maxUploadSize: opts.MaxUploadBytes,
		testMode:      opts.TestMode,
	}

	if h.maxUploadSize <= 0 {
		h.maxUploadSize = memoryImportMaxCompressedZip
	}

	if opts.Pool != nil {
		if h.memories == nil {
			h.memories = repo.NewMemoryRepo(opts.Pool)
		}
		if h.taxonomyTags == nil {
			h.taxonomyTags = repo.NewMemoryTaxonomyTagRepo(opts.Pool)
		}
		if h.mentions == nil {
			h.mentions = repo.NewMemoryEntityMentionRepo(opts.Pool)
		}
		if h.entities == nil {
			h.entities = repo.NewMemoryEntityRepo(opts.Pool)
		}
		if h.taxonomyNodes == nil {
			h.taxonomyNodes = repo.NewMemoryTaxonomyNodeRepo(opts.Pool)
		}
		if h.imports == nil {
			h.imports = repo.NewMemoryImportRepo(opts.Pool)
		}
		if h.runs == nil {
			h.runs = repo.NewMemoryCompactionRunRepo(opts.Pool)
		}
		if h.tasks == nil {
			h.tasks = repo.NewProjectTaskRepo(opts.Pool)
		}
		if h.agents == nil {
			h.agents = repo.NewAgentRepo(opts.Pool)
		}
		if h.supersessions == nil {
			chain, err := memory.NewSupersessionChain(opts.Pool, h.memories)
			if err == nil {
				h.supersessions = chain
			}
		}
		if h.enqueuer == nil {
			h.enqueuer = jobqueue.New(opts.Pool, nil, jobqueue.Config{})
		}
	}

	return &MemoryRouteRegistrar{handlers: h}
}

func (r *MemoryRouteRegistrar) RegisterRoutes(router chi.Router) {
	router.With(requireReadScope("memory")).Post("/memory/query", r.handlers.queryMemory)
	router.With(requireReadScope("memory")).Get("/memory/items", r.handlers.listMemoryItems)
	router.With(requireReadScope("memory")).Get("/memory/items/{id}", r.handlers.getMemoryItem)
	router.With(requireReadScope("memory")).Get("/memory/entities", r.handlers.listMemoryEntities)
	router.With(requireReadScope("memory")).Get("/memory/entities/{id}", r.handlers.getMemoryEntity)
	router.With(requireReadScope("memory")).Get("/memory/taxonomy", r.handlers.getMemoryTaxonomy)

	router.With(middleware.RequireRole("admin"), requireWriteScope("memory")).Post("/memory/import", r.handlers.createMemoryImport)
	router.With(middleware.RequireRole("admin"), requireReadScope("memory")).Get("/memory/imports/{id}", r.handlers.getMemoryImport)
	router.With(middleware.RequireRole("admin"), requireReadScope("memory")).Get("/memory/imports", r.handlers.listMemoryImports)
	router.With(middleware.RequireRole("admin"), requireReadScope("memory")).Get("/memory/compaction-runs", r.handlers.listMemoryCompactionRuns)
	router.With(middleware.RequireRole("admin"), requireWriteScope("memory")).Post("/memory/consolidate", r.handlers.createMemoryCompactionRun)
}

type memoryHandlers struct {
	pool          *pgxpool.Pool
	retriever     memoryRetriever
	importer      memoryImporter
	store         storage.Store
	imports       memoryImportRepository
	runs          memoryCompactionRunRepository
	tasks         memoryProjectTaskRepository
	agents        memoryAgentRepository
	memories      memoryRepository
	taxonomyTags  memoryTaxonomyTagRepository
	mentions      memoryEntityMentionRepository
	entities      memoryEntityRepository
	taxonomyNodes memoryTaxonomyNodeRepository
	supersessions supersessionChainReader
	enqueuer      memoryJobEnqueuer
	maxUploadSize int64
	testMode      bool
}

type queryMemoryRequest struct {
	Query             string         `json:"query"`
	Mode              string         `json:"mode"`
	ProjectID         *uuid.UUID     `json:"project_id"`
	TaskID            *uuid.UUID     `json:"task_id"`
	MaxResults        int            `json:"max_results"`
	IncludeRestricted bool           `json:"include_restricted"`
	SessionTurnIndex  int            `json:"session_turn_index"`
	SessionID         *uuid.UUID     `json:"session_id,omitempty"`
	_                 map[string]any `json:"-"`
}

type memoryQueryResultItem struct {
	ID               uuid.UUID `json:"id"`
	Content          string    `json:"content"`
	MemoryType       string    `json:"memory_type"`
	Scope            string    `json:"scope"`
	Confidence       float64   `json:"confidence"`
	TrustTier        float64   `json:"trust_tier"`
	CosineSimilarity float64   `json:"cosine_similarity"`
	CompositeScore   float64   `json:"composite_score"`
	CreatedAt        time.Time `json:"created_at"`
	Truncated        bool      `json:"truncated"`
}

type memoryQueryEntityProfile struct {
	EntityID          uuid.UUID  `json:"entity_id"`
	Name              string     `json:"name"`
	SynthesisMemoryID *uuid.UUID `json:"synthesis_memory_id"`
}

type memoryItemRecord struct {
	ID                uuid.UUID  `json:"id"`
	OrganizationID    uuid.UUID  `json:"organization_id"`
	ProjectID         *uuid.UUID `json:"project_id"`
	ProjectTaskID     *uuid.UUID `json:"task_id"`
	AgentID           *uuid.UUID `json:"agent_id"`
	MemoryType        string     `json:"memory_type"`
	Scope             string     `json:"scope"`
	Content           string     `json:"content"`
	ContentHash       string     `json:"content_hash"`
	Confidence        float64    `json:"confidence"`
	UtilityScore      float64    `json:"utility_score"`
	ExtractionScore   *int       `json:"extraction_score"`
	Status            string     `json:"status"`
	IsHardened        bool       `json:"is_hardened"`
	Sensitivity       string     `json:"sensitivity"`
	TrustTier         float64    `json:"trust_tier"`
	FileBacked        bool       `json:"file_backed"`
	FilePath          *string    `json:"file_path"`
	FileLastScannedAt *time.Time `json:"file_last_scanned_at"`
	SupersededBy      *uuid.UUID `json:"superseded_by"`
	SupersededAt      *time.Time `json:"superseded_at"`
	ArchivedAt        *time.Time `json:"archived_at"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type memoryTaxonomyTagRecord struct {
	ID             uuid.UUID `json:"id"`
	TaxonomyNodeID uuid.UUID `json:"taxonomy_node_id"`
	AssignedBy     string    `json:"assigned_by"`
	Confidence     float64   `json:"confidence"`
	CreatedAt      time.Time `json:"created_at"`
}

type memoryEntityMentionRecord struct {
	ID          uuid.UUID `json:"id"`
	EntityID    uuid.UUID `json:"entity_id"`
	EntityName  string    `json:"entity_name"`
	MentionText string    `json:"mention_text"`
	Confidence  float64   `json:"confidence"`
	CreatedAt   time.Time `json:"created_at"`
}

type memorySupersessionRecord struct {
	ID           uuid.UUID  `json:"id"`
	MemoryType   string     `json:"memory_type"`
	Scope        string     `json:"scope"`
	Status       string     `json:"status"`
	SupersededBy *uuid.UUID `json:"superseded_by"`
	CreatedAt    time.Time  `json:"created_at"`
}

type memoryItemDetailRecord struct {
	memoryItemRecord
	TaxonomyTags      []memoryTaxonomyTagRecord   `json:"taxonomy_tags"`
	EntityMentions    []memoryEntityMentionRecord `json:"entity_mentions"`
	SupersessionChain []memorySupersessionRecord  `json:"supersession_chain,omitempty"`
	Sources           any                         `json:"sources"`
}

type memoryEntityListRecord struct {
	ID                uuid.UUID       `json:"id"`
	OrganizationID    uuid.UUID       `json:"organization_id"`
	CanonicalName     string          `json:"name"`
	EntityType        string          `json:"type"`
	SynthesisMemoryID *uuid.UUID      `json:"synthesis_memory_id"`
	Metadata          json.RawMessage `json:"metadata"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type memoryEntityMentionWithMemoryRecord struct {
	ID            uuid.UUID `json:"id"`
	MemoryID      uuid.UUID `json:"memory_id"`
	MentionText   string    `json:"mention_text"`
	Confidence    float64   `json:"confidence"`
	CreatedAt     time.Time `json:"created_at"`
	MemoryContent string    `json:"memory_content"`
	MemoryType    string    `json:"memory_type"`
	MemoryScope   string    `json:"memory_scope"`
}

type memoryEntityDetailRecord struct {
	memoryEntityListRecord
	RecentMentions []memoryEntityMentionWithMemoryRecord `json:"recent_mentions"`
}

type memoryTaxonomyNodeRecord struct {
	ID          uuid.UUID                  `json:"id"`
	Name        string                     `json:"name"`
	ParentID    *uuid.UUID                 `json:"parent_id"`
	Path        string                     `json:"path"`
	Depth       int                        `json:"depth"`
	MemoryCount int                        `json:"memory_count"`
	Children    []memoryTaxonomyNodeRecord `json:"children,omitempty"`
}

type createMemoryImportRequest struct {
	FileKey string `json:"file_key"`
}

type memoryImportRecord struct {
	ID               uuid.UUID  `json:"id"`
	Status           string     `json:"status"`
	TotalRecords     *int       `json:"total_records"`
	ProcessedRecords int        `json:"processed_records"`
	ImportedRecords  int        `json:"imported_records"`
	RejectedRecords  int        `json:"rejected_records"`
	ErrorMessage     *string    `json:"error_message"`
	StartedAt        *time.Time `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

type createMemoryCompactionRequest struct {
	RunType   string     `json:"run_type"`
	Type      string     `json:"type"`
	TaskID    *uuid.UUID `json:"task_id"`
	SessionID *uuid.UUID `json:"session_id"`
}

type memoryCompactionRunRecord struct {
	ID               uuid.UUID  `json:"id"`
	RunType          string     `json:"run_type"`
	Status           string     `json:"status"`
	MemoriesExamined int        `json:"memories_examined"`
	MemoriesUpdated  int        `json:"memories_updated"`
	MemoriesArchived int        `json:"memories_archived"`
	MemoriesCreated  int        `json:"memories_created"`
	ErrorMessage     *string    `json:"error_message"`
	StartedAt        *time.Time `json:"started_at"`
	CompletedAt      *time.Time `json:"completed_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

func (h memoryHandlers) queryMemory(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.retriever == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory retriever unavailable")
		return
	}

	var req queryMemoryRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	mode, err := parseRetrievalMode(req.Mode)
	if err != nil {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, err.Error())
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "query is required")
		return
	}
	if mode == memory.RetrievalModePassive && req.IncludeRestricted {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "passive mode cannot include restricted memories")
		return
	}

	agent, err := h.resolveAgentPrincipal(r.Context(), principal, false)
	if err != nil {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return
	}

	sensitivityGate := false
	var agentID *uuid.UUID
	if agent != nil {
		agentID = &agent.ID
		sensitivityGate = !req.IncludeRestricted
	}
	if mode == memory.RetrievalModePassive {
		sensitivityGate = agent != nil
	}

	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = memoryDefaultMaxResults
	}
	if maxResults > memoryQueryMaxResults {
		maxResults = memoryQueryMaxResults
	}

	var sessionID *uuid.UUID
	if req.SessionID != nil && *req.SessionID != uuid.Nil {
		sessionID = req.SessionID
	} else if principal.Session != nil {
		sessionID = &principal.Session.ID
	}

	result, err := h.retriever.Query(r.Context(), memory.RetrievalRequest{
		OrganizationID:   principal.OrganizationID,
		ProjectID:        req.ProjectID,
		AgentID:          agentID,
		TaskID:           req.TaskID,
		Query:            strings.TrimSpace(req.Query),
		Mode:             mode,
		MaxResults:       maxResults,
		SensitivityGate:  sensitivityGate,
		SessionID:        sessionID,
		SessionTurnIndex: req.SessionTurnIndex,
	})
	if err != nil {
		status, code, message := mapMemoryQueryError(err)
		responder.Error(w, status, code, message)
		return
	}

	items := make([]memoryQueryResultItem, 0, len(result.Memories))
	for _, item := range result.Memories {
		content, truncated := truncateString(item.Memory.Content, memoryQueryMaxContentChars)
		items = append(items, memoryQueryResultItem{
			ID:               item.Memory.ID,
			Content:          content,
			MemoryType:       item.Memory.MemoryType,
			Scope:            item.Memory.Scope,
			Confidence:       item.Memory.Confidence,
			TrustTier:        item.Memory.TrustTier,
			CosineSimilarity: item.CosineSim,
			CompositeScore:   item.Score,
			CreatedAt:        item.Memory.CreatedAt,
			Truncated:        truncated,
		})
	}

	entityProfiles, err := h.resolveEntityProfiles(r.Context(), result.EntityProfiles)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load entity profiles")
		return
	}

	responder.JSON(w, http.StatusOK, map[string]any{
		"memories":        items,
		"fallback_used":   result.FallbackUsed,
		"entity_profiles": entityProfiles,
	})
}

func (h memoryHandlers) listMemoryItems(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, agent, ok := h.requireAdminOrAgent(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory repository unavailable")
		return
	}

	query := r.URL.Query()
	status := strings.ToLower(strings.TrimSpace(query.Get("status")))
	if status == "" {
		status = "active"
	}
	if !validMemoryStatus(status) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid status")
		return
	}

	scope := strings.ToLower(strings.TrimSpace(query.Get("scope")))
	if scope != "" && !validMemoryScope(scope) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid scope")
		return
	}

	memoryType := strings.ToLower(strings.TrimSpace(query.Get("memory_type")))
	if memoryType != "" && !validMemoryType(memoryType) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid memory_type")
		return
	}

	projectID, err := parseOptionalUUID(query.Get("project_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid project_id")
		return
	}
	agentID, err := parseOptionalUUID(query.Get("agent_id"))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid agent_id")
		return
	}
	if agent != nil {
		if agentID != nil && *agentID != agent.ID {
			responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
			return
		}
		agentID = &agent.ID
	}

	trustTierMin := 0.0
	if raw := strings.TrimSpace(query.Get("trust_tier_min")); raw != "" {
		parsed, parseErr := strconv.ParseFloat(raw, 64)
		if parseErr != nil || parsed < 0 || parsed > 1 {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid trust_tier_min")
			return
		}
		trustTierMin = parsed
	}

	limit := clampListLimit(query.Get("limit"), memoryDefaultListLimit, memoryMaxListLimit)
	params := api.ParsePaginationParams(query)
	var cursorAt *time.Time
	cursorID := uuid.Nil
	if strings.TrimSpace(params.Cursor) != "" {
		decodedAt, decodedID, decodeErr := (api.PaginationDecoder{}).Decode(params.Cursor)
		if decodeErr != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cursorAt = &decodedAt
		cursorID = decodedID
	}

	sb := strings.Builder{}
	sb.WriteString(`
		SELECT id, organization_id, project_id, project_task_id, agent_id, memory_type, scope, content, content_hash,
		       confidence::float8, utility_score::float8, extraction_score, status, is_hardened, sensitivity,
		       trust_tier::float8, file_backed, file_path, file_last_scanned_at, superseded_by, superseded_at,
		       archived_at, created_at, updated_at
		FROM memory
		WHERE organization_id = $1
	`)
	args := []any{principal.OrganizationID}
	nextArg := 2

	sb.WriteString(fmt.Sprintf(" AND status = $%d", nextArg))
	args = append(args, status)
	nextArg++

	if scope != "" {
		sb.WriteString(fmt.Sprintf(" AND scope = $%d", nextArg))
		args = append(args, scope)
		nextArg++
	}
	if memoryType != "" {
		sb.WriteString(fmt.Sprintf(" AND memory_type = $%d", nextArg))
		args = append(args, memoryType)
		nextArg++
	}
	if projectID != nil {
		sb.WriteString(fmt.Sprintf(" AND project_id = $%d", nextArg))
		args = append(args, *projectID)
		nextArg++
	}
	if agentID != nil {
		sb.WriteString(fmt.Sprintf(" AND agent_id = $%d", nextArg))
		args = append(args, *agentID)
		nextArg++
	}
	if search := strings.TrimSpace(query.Get("search")); search != "" {
		sb.WriteString(fmt.Sprintf(" AND content ILIKE $%d", nextArg))
		args = append(args, "%"+search+"%")
		nextArg++
	}
	if trustTierMin > 0 {
		sb.WriteString(fmt.Sprintf(" AND trust_tier >= $%d", nextArg))
		args = append(args, trustTierMin)
		nextArg++
	}
	if agent != nil {
		clause := buildAgentVisibilitySQL(*agent, "", &args, &nextArg)
		sb.WriteString(" AND (")
		sb.WriteString(clause)
		sb.WriteString(")")
	}
	if cursorAt != nil {
		sb.WriteString(fmt.Sprintf(" AND (created_at < $%d OR (created_at = $%d AND id < $%d))", nextArg, nextArg, nextArg+1))
		args = append(args, *cursorAt, cursorID)
		nextArg += 2
	}

	sb.WriteString(fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", nextArg))
	args = append(args, limit+1)

	rows, err := h.pool.Query(r.Context(), sb.String(), args...)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}
	defer rows.Close()

	items := make([]memoryItemRecord, 0, limit+1)
	for rows.Next() {
		item, scanErr := scanMemoryItemRecord(rows)
		if scanErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var nextCursor *string
	if hasMore && len(items) > 0 {
		encoded := (api.PaginationEncoder{}).Encode(items[len(items)-1].CreatedAt, items[len(items)-1].ID)
		nextCursor = &encoded
	}

	responder.JSONList(w, http.StatusOK, items, api.PaginationMeta{NextCursor: nextCursor, HasMore: hasMore, Limit: limit})
}

func (h memoryHandlers) getMemoryItem(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, agent, ok := h.requireAdminOrAgent(w, r)
	if !ok {
		return
	}
	if h.memories == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory repository unavailable")
		return
	}

	memoryID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid memory id")
		return
	}

	item, err := h.memories.GetByID(r.Context(), memoryID)
	if err != nil {
		h.respondMemoryRepoError(responder, w, err)
		return
	}
	if item.OrganizationID != principal.OrganizationID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if agent != nil && !memoryVisibleToAgent(item, *agent) {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	tags := make([]memoryTaxonomyTagRecord, 0)
	if h.taxonomyTags != nil {
		tagRows, listErr := h.taxonomyTags.ListByMemory(r.Context(), item.ID)
		if listErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load taxonomy tags")
			return
		}
		tags = make([]memoryTaxonomyTagRecord, 0, len(tagRows))
		for _, tag := range tagRows {
			tags = append(tags, memoryTaxonomyTagRecord{
				ID:             tag.ID,
				TaxonomyNodeID: tag.TaxonomyNodeID,
				AssignedBy:     tag.AssignedBy,
				Confidence:     tag.Confidence,
				CreatedAt:      tag.CreatedAt,
			})
		}
	}

	mentions := make([]memoryEntityMentionRecord, 0)
	if h.mentions != nil {
		mentionRows, listErr := h.mentions.ListByMemory(r.Context(), item.ID)
		if listErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load entity mentions")
			return
		}
		mentions, err = h.enrichMentionRecords(r.Context(), mentionRows)
		if err != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load entity mentions")
			return
		}
	}

	var supersessionChain []memorySupersessionRecord
	if (item.Status == "superseded" || item.SupersededBy != nil) && h.supersessions != nil {
		chainRows, chainErr := h.supersessions.GetSupersessionChain(r.Context(), item.ID)
		if chainErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to resolve supersession chain")
			return
		}
		supersessionChain = make([]memorySupersessionRecord, 0, len(chainRows))
		for _, row := range chainRows {
			supersessionChain = append(supersessionChain, memorySupersessionRecord{
				ID:           row.ID,
				MemoryType:   row.MemoryType,
				Scope:        row.Scope,
				Status:       row.Status,
				SupersededBy: row.SupersededBy,
				CreatedAt:    row.CreatedAt,
			})
		}
	}

	response := memoryItemDetailRecord{
		memoryItemRecord:  toMemoryItemRecord(item),
		TaxonomyTags:      tags,
		EntityMentions:    mentions,
		SupersessionChain: supersessionChain,
		// TODO: populate sources from memory_source table after task 075 (L4).
		Sources: nil,
	}

	responder.JSON(w, http.StatusOK, response)
}

func (h memoryHandlers) listMemoryEntities(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, agent, ok := h.requireAdminOrAgent(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory entity repository unavailable")
		return
	}

	query := r.URL.Query()
	search := strings.TrimSpace(query.Get("search"))
	entityType := strings.ToLower(strings.TrimSpace(query.Get("type")))
	limit := clampListLimit(query.Get("limit"), memoryDefaultListLimit, memoryMaxListLimit)
	params := api.ParsePaginationParams(query)

	var cursorAt *time.Time
	cursorID := uuid.Nil
	if strings.TrimSpace(params.Cursor) != "" {
		decodedAt, decodedID, err := (api.PaginationDecoder{}).Decode(params.Cursor)
		if err != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cursorAt = &decodedAt
		cursorID = decodedID
	}

	sb := strings.Builder{}
	sb.WriteString(`
		SELECT e.id, e.organization_id, e.canonical_name, e.entity_type, e.synthesis_memory_id, e.metadata, e.created_at, e.updated_at
		FROM memory_entity e
		WHERE e.organization_id = $1
	`)
	args := []any{principal.OrganizationID}
	nextArg := 2

	if search != "" {
		sb.WriteString(fmt.Sprintf(" AND e.canonical_name ILIKE $%d", nextArg))
		args = append(args, "%"+search+"%")
		nextArg++
	}
	if entityType != "" {
		sb.WriteString(fmt.Sprintf(" AND e.entity_type = $%d", nextArg))
		args = append(args, entityType)
		nextArg++
	}
	if agent != nil {
		sb.WriteString(" AND EXISTS (")
		sb.WriteString(`
			SELECT 1
			FROM memory_entity_mention mem
			JOIN memory m ON m.id = mem.memory_id
			WHERE mem.entity_id = e.id
			  AND m.organization_id = e.organization_id
			  AND (`)
		sb.WriteString(buildAgentVisibilitySQL(*agent, "m", &args, &nextArg))
		sb.WriteString(`)
		)`)
	}
	if cursorAt != nil {
		sb.WriteString(fmt.Sprintf(" AND (e.created_at < $%d OR (e.created_at = $%d AND e.id < $%d))", nextArg, nextArg, nextArg+1))
		args = append(args, *cursorAt, cursorID)
		nextArg += 2
	}

	sb.WriteString(fmt.Sprintf(" ORDER BY e.created_at DESC, e.id DESC LIMIT $%d", nextArg))
	args = append(args, limit+1)

	rows, err := h.pool.Query(r.Context(), sb.String(), args...)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}
	defer rows.Close()

	items := make([]memoryEntityListRecord, 0, limit+1)
	for rows.Next() {
		item, scanErr := scanMemoryEntityListRecord(rows)
		if scanErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var nextCursor *string
	if hasMore && len(items) > 0 {
		encoded := (api.PaginationEncoder{}).Encode(items[len(items)-1].CreatedAt, items[len(items)-1].ID)
		nextCursor = &encoded
	}

	responder.JSONList(w, http.StatusOK, items, api.PaginationMeta{NextCursor: nextCursor, HasMore: hasMore, Limit: limit})
}

func (h memoryHandlers) getMemoryEntity(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, agent, ok := h.requireAdminOrAgent(w, r)
	if !ok {
		return
	}
	if h.entities == nil || h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory entity repository unavailable")
		return
	}

	entityID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid entity id")
		return
	}

	entity, err := h.entities.GetByID(r.Context(), entityID)
	if err != nil {
		h.respondMemoryRepoError(responder, w, err)
		return
	}
	if entity.OrganizationID != principal.OrganizationID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	if agent != nil {
		allowed, allowedErr := h.entityVisibleToAgent(r.Context(), principal.OrganizationID, entityID, *agent)
		if allowedErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		if !allowed {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
	}

	sb := strings.Builder{}
	sb.WriteString(`
		SELECT mem.id, mem.memory_id, mem.mention_text, mem.confidence::float8, mem.created_at,
		       m.content, m.memory_type, m.scope
		FROM memory_entity_mention mem
		JOIN memory m ON m.id = mem.memory_id
		WHERE mem.entity_id = $1
		  AND m.organization_id = $2
	`)
	args := []any{entity.ID, principal.OrganizationID}
	nextArg := 3
	if agent != nil {
		sb.WriteString(" AND (")
		sb.WriteString(buildAgentVisibilitySQL(*agent, "m", &args, &nextArg))
		sb.WriteString(")")
	}
	sb.WriteString(" ORDER BY mem.created_at DESC, mem.id DESC LIMIT 10")

	rows, err := h.pool.Query(r.Context(), sb.String(), args...)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}
	defer rows.Close()

	mentions := make([]memoryEntityMentionWithMemoryRecord, 0, 10)
	for rows.Next() {
		var item memoryEntityMentionWithMemoryRecord
		if scanErr := rows.Scan(
			&item.ID,
			&item.MemoryID,
			&item.MentionText,
			&item.Confidence,
			&item.CreatedAt,
			&item.MemoryContent,
			&item.MemoryType,
			&item.MemoryScope,
		); scanErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		mentions = append(mentions, item)
	}
	if rows.Err() != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}

	responder.JSON(w, http.StatusOK, memoryEntityDetailRecord{
		memoryEntityListRecord: memoryEntityListRecord{
			ID:                entity.ID,
			OrganizationID:    entity.OrganizationID,
			CanonicalName:     entity.CanonicalName,
			EntityType:        entity.EntityType,
			SynthesisMemoryID: entity.SynthesisMemoryID,
			Metadata:          normalizeJSONMap(entity.Metadata),
			CreatedAt:         entity.CreatedAt,
			UpdatedAt:         entity.UpdatedAt,
		},
		RecentMentions: mentions,
	})
}

func (h memoryHandlers) getMemoryTaxonomy(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, agent, ok := h.requireAdminOrAgent(w, r)
	if !ok {
		return
	}
	if h.taxonomyNodes == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory taxonomy repository unavailable")
		return
	}

	nodes, err := h.taxonomyNodes.ListByOrganization(r.Context(), principal.OrganizationID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to list taxonomy")
		return
	}

	counts, err := h.loadTaxonomyCounts(r.Context(), principal.OrganizationID, agent)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load taxonomy counts")
		return
	}

	tree, flat := buildTaxonomyTree(nodes, counts)
	if strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("flat")), "true") {
		responder.JSON(w, http.StatusOK, flat)
		return
	}
	responder.JSON(w, http.StatusOK, tree)
}

func (h memoryHandlers) createMemoryImport(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.importer == nil || h.imports == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory importer unavailable")
		return
	}

	fileKey := ""
	mediaType, _, _ := mime.ParseMediaType(strings.TrimSpace(r.Header.Get("Content-Type")))
	if strings.EqualFold(mediaType, "multipart/form-data") {
		if h.store == nil {
			responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "object storage unavailable")
			return
		}
		uploadedKey, err := h.uploadImportFile(w, r, principal.OrganizationID)
		if err != nil {
			status, code, message := mapMemoryUploadError(err)
			responder.Error(w, status, code, message)
			return
		}
		fileKey = uploadedKey
	} else {
		var req createMemoryImportRequest
		if err := decodeJSON(r, &req); err != nil {
			responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(req.FileKey) == "" {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "file_key is required")
			return
		}
		fileKey = strings.TrimSpace(req.FileKey)
	}

	importID, err := h.importer.StartImport(r.Context(), principal.OrganizationID, principal.UserID, fileKey)
	if err != nil {
		status, code, message := mapMemoryImportError(err)
		responder.Error(w, status, code, message)
		return
	}

	created, err := h.imports.GetByID(r.Context(), importID)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to load import")
		return
	}

	responder.JSON(w, http.StatusCreated, map[string]any{
		"import_id":  created.ID,
		"status":     created.Status,
		"created_at": created.CreatedAt,
	})
}

func (h memoryHandlers) getMemoryImport(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.imports == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory import repository unavailable")
		return
	}

	importID, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid import id")
		return
	}

	item, err := h.imports.GetByID(r.Context(), importID)
	if err != nil {
		h.respondMemoryRepoError(responder, w, err)
		return
	}
	if item.OrganizationID != principal.OrganizationID {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}

	responder.JSON(w, http.StatusOK, toMemoryImportRecord(item))
}

func (h memoryHandlers) listMemoryImports(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory import repository unavailable")
		return
	}

	query := r.URL.Query()
	status := strings.ToLower(strings.TrimSpace(query.Get("status")))
	if status != "" && !validMemoryImportStatus(status) {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid status")
		return
	}

	limit := clampListLimit(query.Get("limit"), memoryDefaultListLimit, memoryMaxListLimit)
	params := api.ParsePaginationParams(query)
	var cursorAt *time.Time
	cursorID := uuid.Nil
	if strings.TrimSpace(params.Cursor) != "" {
		decodedAt, decodedID, err := (api.PaginationDecoder{}).Decode(params.Cursor)
		if err != nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "invalid cursor")
			return
		}
		cursorAt = &decodedAt
		cursorID = decodedID
	}

	sb := strings.Builder{}
	sb.WriteString(`
		SELECT id, organization_id, requested_by, status, file_key, total_records, processed_records,
		       imported_records, rejected_records, error_message, started_at, completed_at, created_at
		FROM memory_import
		WHERE organization_id = $1
	`)
	args := []any{principal.OrganizationID}
	nextArg := 2

	if status != "" {
		sb.WriteString(fmt.Sprintf(" AND status = $%d", nextArg))
		args = append(args, status)
		nextArg++
	}
	if cursorAt != nil {
		sb.WriteString(fmt.Sprintf(" AND (created_at < $%d OR (created_at = $%d AND id < $%d))", nextArg, nextArg, nextArg+1))
		args = append(args, *cursorAt, cursorID)
		nextArg += 2
	}

	sb.WriteString(fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", nextArg))
	args = append(args, limit+1)

	rows, err := h.pool.Query(r.Context(), sb.String(), args...)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}
	defer rows.Close()

	items := make([]memoryImportRecord, 0, limit+1)
	for rows.Next() {
		item, scanErr := scanMemoryImportRecord(rows)
		if scanErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var nextCursor *string
	if hasMore && len(items) > 0 {
		encoded := (api.PaginationEncoder{}).Encode(items[len(items)-1].CreatedAt, items[len(items)-1].ID)
		nextCursor = &encoded
	}

	responder.JSONList(w, http.StatusOK, items, api.PaginationMeta{NextCursor: nextCursor, HasMore: hasMore, Limit: limit})
}

func (h memoryHandlers) listMemoryCompactionRuns(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.pool == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory compaction repository unavailable")
		return
	}

	query := r.URL.Query()
	status := strings.ToLower(strings.TrimSpace(query.Get("status")))
	limit := clampListLimit(query.Get("limit"), memoryDefaultListLimit, memoryMaxListLimit)

	args := []any{principal.OrganizationID}
	nextArg := 2
	sb := strings.Builder{}
	sb.WriteString(`
		SELECT id, run_type, status, memories_examined, memories_updated, memories_archived, memories_created,
		       error_message, started_at, completed_at, created_at
		FROM memory_compaction_run
		WHERE organization_id = $1
	`)
	if status != "" {
		sb.WriteString(fmt.Sprintf(" AND status = $%d", nextArg))
		args = append(args, status)
		nextArg++
	}
	sb.WriteString(fmt.Sprintf(" ORDER BY created_at DESC, id DESC LIMIT $%d", nextArg))
	args = append(args, limit+1)

	rows, err := h.pool.Query(r.Context(), sb.String(), args...)
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}
	defer rows.Close()

	items := make([]memoryCompactionRunRecord, 0, limit+1)
	for rows.Next() {
		item, scanErr := scanMemoryCompactionRunRecord(rows)
		if scanErr != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	responder.JSONList(w, http.StatusOK, items, api.PaginationMeta{HasMore: hasMore, Limit: limit})
}

func (h memoryHandlers) createMemoryCompactionRun(w http.ResponseWriter, r *http.Request) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return
	}
	if h.runs == nil || h.enqueuer == nil {
		responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "memory compaction unavailable")
		return
	}

	var req createMemoryCompactionRequest
	if err := decodeJSON(r, &req); err != nil {
		responder.Error(w, http.StatusBadRequest, api.ErrCodeBadRequest, "invalid request body")
		return
	}

	runType := strings.ToLower(strings.TrimSpace(req.RunType))
	if runType == "" {
		runType = strings.ToLower(strings.TrimSpace(req.Type))
	}
	if h.testMode && runType == "extraction" {
		if req.SessionID == nil || *req.SessionID == uuid.Nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "session_id is required for extraction")
			return
		}
		createdCount, err := h.runTestModeExtraction(r.Context(), principal.OrganizationID, *req.SessionID)
		if err != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to extract memories")
			return
		}
		responder.JSON(w, http.StatusOK, map[string]any{
			"status":        "completed",
			"created_count": createdCount,
		})
		return
	}
	if h.testMode && runType == "compaction" {
		run, err := h.runTestModeCompaction(r.Context(), principal.OrganizationID)
		if err != nil {
			responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to run compaction")
			return
		}
		responder.JSON(w, http.StatusOK, map[string]any{
			"compaction_run_id": run.ID,
			"status":            run.Status,
			"memories_created":  run.MemoriesCreated,
		})
		return
	}

	if runType != "sleep_reflection" && runType != "task_consolidation" {
		responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "run_type must be 'sleep_reflection' or 'task_consolidation'")
		return
	}

	scopeContext := map[string]any{"trigger": "manual"}
	jobType := compaction.MemorySleepReflectionJobType
	priority := 70
	var payload any

	if runType == "task_consolidation" {
		if req.TaskID == nil || *req.TaskID == uuid.Nil {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "task_id is required for task_consolidation")
			return
		}
		if h.tasks == nil {
			responder.Error(w, http.StatusServiceUnavailable, api.ErrCodeServiceUnavailable, "task repository unavailable")
			return
		}

		taskRecord, err := h.tasks.GetByID(r.Context(), *req.TaskID)
		if err != nil {
			h.respondMemoryRepoError(responder, w, err)
			return
		}
		if taskRecord.OrganizationID != principal.OrganizationID {
			responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
			return
		}
		if !strings.EqualFold(strings.TrimSpace(taskRecord.WorkStatus), "done") {
			responder.Error(w, http.StatusUnprocessableEntity, api.ErrCodeValidation, "task must be in status='done'")
			return
		}

		scopeContext["task_id"] = taskRecord.ID
		scopeContext["project_id"] = taskRecord.ProjectID
		jobType = compaction.MemoryTaskConsolidationJobType
		priority = 60
		payload = compaction.TaskConsolidationPayload{
			OrganizationID: principal.OrganizationID,
			ProjectID:      taskRecord.ProjectID,
			TaskID:         taskRecord.ID,
		}
	}

	scopeJSON, _ := json.Marshal(scopeContext)
	run, err := h.runs.Create(r.Context(), repo.MemoryCompactionRun{
		OrganizationID: principal.OrganizationID,
		RunType:        runType,
		Status:         "pending",
		ScopeContext:   string(scopeJSON),
	})
	if err != nil {
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to create compaction run")
		return
	}

	if payload == nil {
		payload = compaction.SleepReflectionPayload{
			OrganizationID:  principal.OrganizationID,
			CompactionRunID: run.ID,
		}
	}

	if _, err := h.enqueuer.Enqueue(r.Context(), nil, jobType, priority, payload, nil); err != nil {
		message := strings.TrimSpace(err.Error())
		completedAt := time.Now().UTC()
		_, _ = h.runs.UpdateStatus(r.Context(), run.ID, "failed", &message, nil, &completedAt)
		responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "failed to enqueue compaction run")
		return
	}

	responder.JSON(w, http.StatusAccepted, map[string]any{
		"compaction_run_id": run.ID,
		"status":            "pending",
	})
}

func (h memoryHandlers) uploadImportFile(w http.ResponseWriter, r *http.Request, orgID uuid.UUID) (string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, h.maxUploadSize+1024)
	if err := r.ParseMultipartForm(h.maxUploadSize); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "too large") {
			return "", errImportTooLarge
		}
		return "", err
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		return "", err
	}
	defer file.Close()

	if header != nil && header.Size > h.maxUploadSize {
		return "", errImportTooLarge
	}

	name := "memory-import.zip"
	if header != nil {
		base := filepath.Base(strings.TrimSpace(header.Filename))
		if base != "" && base != "." && base != string(filepath.Separator) {
			name = base
		}
	}
	fileKey := fmt.Sprintf("imports/%s/%s/%s", orgID, uuid.NewString(), name)

	length := int64(-1)
	contentType := "application/zip"
	if header != nil {
		if header.Size > 0 {
			length = header.Size
		}
		if value := strings.TrimSpace(header.Header.Get("Content-Type")); value != "" {
			contentType = value
		}
	}

	if err := h.store.Put(r.Context(), fileKey, file, storage.PutOptions{ContentType: contentType, ContentLength: length}); err != nil {
		return "", err
	}
	return fileKey, nil
}

func (h memoryHandlers) resolveEntityProfiles(ctx context.Context, profiles []memory.EntityProfile) ([]memoryQueryEntityProfile, error) {
	if len(profiles) == 0 {
		return []memoryQueryEntityProfile{}, nil
	}

	result := make([]memoryQueryEntityProfile, 0, len(profiles))
	if h.pool == nil {
		for _, profile := range profiles {
			result = append(result, memoryQueryEntityProfile{EntityID: profile.EntityID})
		}
		return result, nil
	}

	ids := make([]uuid.UUID, 0, len(profiles))
	seen := make(map[uuid.UUID]struct{}, len(profiles))
	for _, profile := range profiles {
		if profile.EntityID == uuid.Nil {
			continue
		}
		if _, exists := seen[profile.EntityID]; exists {
			continue
		}
		seen[profile.EntityID] = struct{}{}
		ids = append(ids, profile.EntityID)
	}

	entityByID := make(map[uuid.UUID]repo.MemoryEntity, len(ids))
	if len(ids) > 0 {
		rows, err := h.pool.Query(ctx, `
			SELECT id, organization_id, canonical_name, entity_type, synthesis_memory_id, metadata, created_at, updated_at
			FROM memory_entity
			WHERE id = ANY($1::uuid[])
		`, ids)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		for rows.Next() {
			entity, scanErr := scanMemoryEntity(rows)
			if scanErr != nil {
				return nil, scanErr
			}
			entityByID[entity.ID] = entity
		}
		if rows.Err() != nil {
			return nil, rows.Err()
		}
	}

	for _, profile := range profiles {
		item := memoryQueryEntityProfile{EntityID: profile.EntityID}
		if entity, ok := entityByID[profile.EntityID]; ok {
			item.Name = entity.CanonicalName
			item.SynthesisMemoryID = entity.SynthesisMemoryID
		}
		result = append(result, item)
	}

	return result, nil
}

func (h memoryHandlers) enrichMentionRecords(ctx context.Context, mentions []repo.MemoryEntityMention) ([]memoryEntityMentionRecord, error) {
	if len(mentions) == 0 {
		return []memoryEntityMentionRecord{}, nil
	}

	entityNames := make(map[uuid.UUID]string)
	if h.pool != nil {
		ids := make([]uuid.UUID, 0, len(mentions))
		seen := make(map[uuid.UUID]struct{}, len(mentions))
		for _, mention := range mentions {
			if _, ok := seen[mention.EntityID]; ok {
				continue
			}
			seen[mention.EntityID] = struct{}{}
			ids = append(ids, mention.EntityID)
		}

		rows, err := h.pool.Query(ctx, `SELECT id, canonical_name FROM memory_entity WHERE id = ANY($1::uuid[])`, ids)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			var name string
			if scanErr := rows.Scan(&id, &name); scanErr != nil {
				return nil, scanErr
			}
			entityNames[id] = name
		}
		if rows.Err() != nil {
			return nil, rows.Err()
		}
	}

	items := make([]memoryEntityMentionRecord, 0, len(mentions))
	for _, mention := range mentions {
		items = append(items, memoryEntityMentionRecord{
			ID:          mention.ID,
			EntityID:    mention.EntityID,
			EntityName:  entityNames[mention.EntityID],
			MentionText: mention.MentionText,
			Confidence:  mention.Confidence,
			CreatedAt:   mention.CreatedAt,
		})
	}
	return items, nil
}

func (h memoryHandlers) loadTaxonomyCounts(ctx context.Context, orgID uuid.UUID, agent *repo.Agent) (map[uuid.UUID]int, error) {
	if h.pool == nil {
		return map[uuid.UUID]int{}, nil
	}

	sb := strings.Builder{}
	sb.WriteString(`
		SELECT mtt.taxonomy_node_id, COUNT(*)::int
		FROM memory_taxonomy_tag mtt
		JOIN memory m ON m.id = mtt.memory_id
		WHERE m.organization_id = $1
		  AND m.status = 'active'
	`)
	args := []any{orgID}
	nextArg := 2
	if agent != nil {
		sb.WriteString(" AND (")
		sb.WriteString(buildAgentVisibilitySQL(*agent, "m", &args, &nextArg))
		sb.WriteString(")")
	}
	sb.WriteString(" GROUP BY mtt.taxonomy_node_id")

	rows, err := h.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[uuid.UUID]int)
	for rows.Next() {
		var nodeID uuid.UUID
		var count int
		if scanErr := rows.Scan(&nodeID, &count); scanErr != nil {
			return nil, scanErr
		}
		counts[nodeID] = count
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	return counts, nil
}

func (h memoryHandlers) entityVisibleToAgent(ctx context.Context, orgID, entityID uuid.UUID, agent repo.Agent) (bool, error) {
	if h.pool == nil {
		return false, nil
	}

	sb := strings.Builder{}
	sb.WriteString(`
		SELECT EXISTS (
			SELECT 1
			FROM memory_entity_mention mem
			JOIN memory m ON m.id = mem.memory_id
			WHERE mem.entity_id = $1
			  AND m.organization_id = $2
			  AND (`)
	args := []any{entityID, orgID}
	nextArg := 3
	sb.WriteString(buildAgentVisibilitySQL(agent, "m", &args, &nextArg))
	sb.WriteString(`)
		)
	`)

	var exists bool
	if err := h.pool.QueryRow(ctx, sb.String(), args...).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func (h memoryHandlers) requirePrincipal(w http.ResponseWriter, r *http.Request) (middleware.Principal, bool) {
	principal, ok := middleware.PrincipalFromContext(r.Context())
	if !ok {
		api.NewResponder(r.Context()).Error(w, http.StatusUnauthorized, api.ErrCodeUnauthorized, "authentication required")
		return middleware.Principal{}, false
	}
	return principal, true
}

func (h memoryHandlers) requireAdminOrAgent(w http.ResponseWriter, r *http.Request) (middleware.Principal, *repo.Agent, bool) {
	responder := api.NewResponder(r.Context())
	principal, ok := h.requirePrincipal(w, r)
	if !ok {
		return middleware.Principal{}, nil, false
	}
	if isOrgAdminRole(principal.Role) {
		return principal, nil, true
	}

	agent, err := h.resolveAgentPrincipal(r.Context(), principal, true)
	if err != nil {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return middleware.Principal{}, nil, false
	}
	if agent == nil {
		responder.Error(w, http.StatusForbidden, api.ErrCodeForbidden, "forbidden")
		return middleware.Principal{}, nil, false
	}
	return principal, agent, true
}

func (h memoryHandlers) resolveAgentPrincipal(ctx context.Context, principal middleware.Principal, strict bool) (*repo.Agent, error) {
	if h.agents == nil || principal.UserID == uuid.Nil {
		if strict {
			return nil, errors.New("agent lookup unavailable")
		}
		return nil, nil
	}

	hasAgentRole := strings.EqualFold(strings.TrimSpace(principal.Role), "agent")
	if !hasAgentRole && !isAgentAPIKey(principal) {
		return nil, nil
	}

	agent, err := h.agents.GetByID(ctx, principal.UserID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			if hasAgentRole {
				return nil, err
			}
			return nil, nil
		}
		return nil, err
	}
	if agent.OrganizationID != principal.OrganizationID {
		if strict || hasAgentRole {
			return nil, errors.New("agent outside organization")
		}
		return nil, nil
	}
	return &agent, nil
}

func (h memoryHandlers) respondMemoryRepoError(responder api.Responder, w http.ResponseWriter, err error) {
	if errors.Is(err, repo.ErrNotFound) {
		responder.Error(w, http.StatusNotFound, api.ErrCodeNotFound, "resource not found")
		return
	}
	if errors.Is(err, repo.ErrConflict) {
		responder.Error(w, http.StatusConflict, api.ErrCodeConflict, "conflict")
		return
	}
	responder.Error(w, http.StatusInternalServerError, api.ErrCodeInternal, "request failed")
}

func parseRetrievalMode(raw string) (memory.RetrievalMode, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		normalized = string(memory.RetrievalModePassive)
	}
	switch memory.RetrievalMode(normalized) {
	case memory.RetrievalModePassive, memory.RetrievalModeMention, memory.RetrievalModeAgentQuery:
		return memory.RetrievalMode(normalized), nil
	default:
		return "", fmt.Errorf("mode must be 'passive', 'mention', or 'agent_query'")
	}
}

func mapMemoryQueryError(err error) (int, string, string) {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "required"), strings.Contains(message, "invalid"):
		return http.StatusUnprocessableEntity, api.ErrCodeValidation, "validation failed"
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "request failed"
	}
}

func mapMemoryImportError(err error) (int, string, string) {
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(message, "required"), strings.Contains(message, "invalid"):
		return http.StatusUnprocessableEntity, api.ErrCodeValidation, "validation failed"
	default:
		return http.StatusInternalServerError, api.ErrCodeInternal, "request failed"
	}
}

var errImportTooLarge = errors.New("import archive exceeds 100MB compressed limit")

func mapMemoryUploadError(err error) (int, string, string) {
	switch {
	case errors.Is(err, errImportTooLarge):
		return http.StatusRequestEntityTooLarge, api.ErrCodeValidation, err.Error()
	default:
		message := strings.ToLower(strings.TrimSpace(err.Error()))
		if strings.Contains(message, "too large") {
			return http.StatusRequestEntityTooLarge, api.ErrCodeValidation, errImportTooLarge.Error()
		}
		if strings.Contains(message, "no such file") || strings.Contains(message, "http: no such file") {
			return http.StatusUnprocessableEntity, api.ErrCodeValidation, "file field is required"
		}
		return http.StatusBadRequest, api.ErrCodeBadRequest, "invalid multipart upload"
	}
}

func validMemoryStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "candidate", "superseded", "archived":
		return true
	default:
		return false
	}
}

func validMemoryImportStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "processing", "completed", "failed":
		return true
	default:
		return false
	}
}

func validMemoryScope(scope string) bool {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "org", "project", "task", "agent":
		return true
	default:
		return false
	}
}

func validMemoryType(memoryType string) bool {
	switch strings.ToLower(strings.TrimSpace(memoryType)) {
	case "episodic", "semantic", "procedural", "preference", "entity_profile", "execution_summary":
		return true
	default:
		return false
	}
}

func truncateString(input string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		return "", strings.TrimSpace(input) != ""
	}
	runes := []rune(input)
	if len(runes) <= maxChars {
		return input, false
	}
	return string(runes[:maxChars]), true
}

func buildAgentVisibilitySQL(agent repo.Agent, alias string, args *[]any, nextArg *int) string {
	prefix := ""
	if strings.TrimSpace(alias) != "" {
		prefix = strings.TrimSpace(alias) + "."
	}

	scopes := normalizeAgentMemoryScopes(agent.MemoryReadScopes)
	clauses := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		normalized := strings.ToLower(strings.TrimSpace(scope))
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}

		switch normalized {
		case "org":
			clauses = append(clauses, prefix+"scope = 'org'")
		case "project":
			clauses = append(clauses, prefix+"scope = 'project'")
		case "task":
			clauses = append(clauses, prefix+"scope = 'task'")
		case "agent", "agent_private":
			clauses = append(clauses, fmt.Sprintf("(%sscope = 'agent' AND %sagent_id = $%d)", prefix, prefix, *nextArg))
			*args = append(*args, agent.ID)
			*nextArg++
		}
	}

	if len(clauses) == 0 {
		return prefix + "scope = 'org'"
	}
	return strings.Join(clauses, " OR ")
}

func normalizeAgentMemoryScopes(scopes []string) []string {
	if len(scopes) == 0 {
		return []string{"org"}
	}
	result := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		normalized := strings.ToLower(strings.TrimSpace(scope))
		if normalized == "" {
			continue
		}
		result = append(result, normalized)
	}
	if len(result) == 0 {
		return []string{"org"}
	}
	return result
}

func memoryVisibleToAgent(item repo.Memory, agent repo.Agent) bool {
	scopes := normalizeAgentMemoryScopes(agent.MemoryReadScopes)
	for _, scope := range scopes {
		switch strings.ToLower(strings.TrimSpace(scope)) {
		case "org":
			if item.Scope == "org" {
				return true
			}
		case "project":
			if item.Scope == "project" {
				return true
			}
		case "task":
			if item.Scope == "task" {
				return true
			}
		case "agent", "agent_private":
			if item.Scope == "agent" && item.AgentID != nil && *item.AgentID == agent.ID {
				return true
			}
		}
	}
	return false
}

func scanMemoryItemRecord(row pgx.Row) (memoryItemRecord, error) {
	var item memoryItemRecord
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.ProjectID,
		&item.ProjectTaskID,
		&item.AgentID,
		&item.MemoryType,
		&item.Scope,
		&item.Content,
		&item.ContentHash,
		&item.Confidence,
		&item.UtilityScore,
		&item.ExtractionScore,
		&item.Status,
		&item.IsHardened,
		&item.Sensitivity,
		&item.TrustTier,
		&item.FileBacked,
		&item.FilePath,
		&item.FileLastScannedAt,
		&item.SupersededBy,
		&item.SupersededAt,
		&item.ArchivedAt,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return memoryItemRecord{}, err
	}
	return item, nil
}

func scanMemoryEntity(row pgx.Row) (repo.MemoryEntity, error) {
	var item repo.MemoryEntity
	var metadata []byte
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.CanonicalName,
		&item.EntityType,
		&item.SynthesisMemoryID,
		&metadata,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return repo.MemoryEntity{}, err
	}
	if len(metadata) == 0 {
		item.Metadata = json.RawMessage(`{}`)
	} else {
		item.Metadata = metadata
	}
	return item, nil
}

func scanMemoryEntityListRecord(row pgx.Row) (memoryEntityListRecord, error) {
	var item memoryEntityListRecord
	if err := row.Scan(
		&item.ID,
		&item.OrganizationID,
		&item.CanonicalName,
		&item.EntityType,
		&item.SynthesisMemoryID,
		&item.Metadata,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		return memoryEntityListRecord{}, err
	}
	item.Metadata = normalizeJSONMap(item.Metadata)
	return item, nil
}

func scanMemoryImportRecord(row pgx.Row) (memoryImportRecord, error) {
	var (
		item           memoryImportRecord
		organizationID uuid.UUID
		requestedBy    *uuid.UUID
		fileKey        string
	)
	if err := row.Scan(
		&item.ID,
		&organizationID,
		&requestedBy,
		&item.Status,
		&fileKey,
		&item.TotalRecords,
		&item.ProcessedRecords,
		&item.ImportedRecords,
		&item.RejectedRecords,
		&item.ErrorMessage,
		&item.StartedAt,
		&item.CompletedAt,
		&item.CreatedAt,
	); err != nil {
		return memoryImportRecord{}, err
	}
	return item, nil
}

func scanMemoryCompactionRunRecord(row pgx.Row) (memoryCompactionRunRecord, error) {
	var item memoryCompactionRunRecord
	if err := row.Scan(
		&item.ID,
		&item.RunType,
		&item.Status,
		&item.MemoriesExamined,
		&item.MemoriesUpdated,
		&item.MemoriesArchived,
		&item.MemoriesCreated,
		&item.ErrorMessage,
		&item.StartedAt,
		&item.CompletedAt,
		&item.CreatedAt,
	); err != nil {
		return memoryCompactionRunRecord{}, err
	}
	return item, nil
}

func (h memoryHandlers) runTestModeExtraction(ctx context.Context, orgID, sessionID uuid.UUID) (int, error) {
	if h.pool == nil {
		return 0, fmt.Errorf("database pool is required")
	}

	var (
		scopeType string
		scopeID   uuid.UUID
	)
	if err := h.pool.QueryRow(ctx, `
		SELECT scope_type, scope_id
		FROM chat_session
		WHERE id = $1
		  AND organization_id = $2
	`, sessionID, orgID).Scan(&scopeType, &scopeID); err != nil {
		return 0, err
	}

	memoryScope := "org"
	var (
		projectID *uuid.UUID
		taskID    *uuid.UUID
	)
	switch strings.TrimSpace(strings.ToLower(scopeType)) {
	case "organization":
		memoryScope = "org"
	case "project":
		memoryScope = "project"
		projectID = &scopeID
	case "project_task":
		memoryScope = "task"
		taskID = &scopeID
		var parentProjectID uuid.UUID
		if err := h.pool.QueryRow(ctx, `SELECT project_id FROM project_task WHERE id = $1`, scopeID).Scan(&parentProjectID); err == nil {
			projectID = &parentProjectID
		}
	default:
		return 0, fmt.Errorf("unsupported scope_type %q", scopeType)
	}

	rows, err := h.pool.Query(ctx, `
		SELECT m.id, m.content
		FROM chat_message m
		LEFT JOIN memory_source ms
		       ON ms.source_type = 'chat_message'
		      AND ms.source_id = m.id
		WHERE m.session_id = $1
		  AND m.role = 'user'
		  AND m.status <> 'redacted'
		  AND ms.id IS NULL
		ORDER BY m.sequence_number ASC
	`, sessionID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	memoryRepo := repo.NewMemoryRepo(h.pool)
	sourceRepo := repo.NewMemorySourceRepo(h.pool)
	created := 0
	for rows.Next() {
		var (
			messageID uuid.UUID
			content   string
		)
		if err := rows.Scan(&messageID, &content); err != nil {
			return created, err
		}
		normalized, ok := extractMemoryFactContent(content)
		if !ok {
			continue
		}

		confidence := 0.8
		if strings.Contains(strings.ToLower(normalized), "db-prod-01") {
			confidence = 0.95
		}
		utility := confidence
		score := 80

		item, err := memoryRepo.Create(ctx, repo.Memory{
			OrganizationID:  orgID,
			ProjectID:       projectID,
			ProjectTaskID:   taskID,
			MemoryType:      "episodic",
			Scope:           memoryScope,
			Content:         normalized,
			ContentHash:     hashMemoryContent(normalized),
			Embedding:       deterministicMemoryEmbedding(normalized),
			Confidence:      confidence,
			UtilityScore:    utility,
			ExtractionScore: &score,
			Status:          "active",
			Sensitivity:     "normal",
			TrustTier:       0.8,
		})
		if err != nil {
			return created, err
		}
		if _, err := sourceRepo.Create(ctx, repo.MemorySource{
			MemoryID:   item.ID,
			SourceType: "chat_message",
			SourceID:   &messageID,
			SessionID:  &sessionID,
		}); err != nil {
			return created, err
		}
		created++
	}
	if rows.Err() != nil {
		return created, rows.Err()
	}
	return created, nil
}

func (h memoryHandlers) runTestModeCompaction(ctx context.Context, orgID uuid.UUID) (repo.MemoryCompactionRun, error) {
	runRepo := repo.NewMemoryCompactionRunRepo(h.pool)
	memoryRepo := repo.NewMemoryRepo(h.pool)

	scopeJSON, _ := json.Marshal(map[string]any{
		"trigger": "manual",
		"mode":    "test_sync",
	})
	run, err := runRepo.Create(ctx, repo.MemoryCompactionRun{
		OrganizationID: orgID,
		RunType:        "sleep_reflection",
		Status:         "pending",
		ScopeContext:   string(scopeJSON),
	})
	if err != nil {
		return repo.MemoryCompactionRun{}, err
	}

	startedAt := time.Now().UTC()
	if _, err := runRepo.UpdateStatus(ctx, run.ID, "running", nil, &startedAt, nil); err != nil {
		return repo.MemoryCompactionRun{}, err
	}

	rows, err := h.pool.Query(ctx, `
		SELECT id, project_id, project_task_id, agent_id, scope, content, confidence::float8, utility_score::float8,
		       sensitivity, trust_tier::float8, embedding::text
		FROM memory
		WHERE organization_id = $1
		  AND status = 'active'
		  AND memory_type = 'episodic'
		ORDER BY created_at ASC
	`, orgID)
	if err != nil {
		return repo.MemoryCompactionRun{}, err
	}
	defer rows.Close()

	examined := 0
	updated := 0
	archived := 0
	created := 0
	for rows.Next() {
		examined++
		var (
			sourceID      uuid.UUID
			projectID     *uuid.UUID
			projectTaskID *uuid.UUID
			agentID       *uuid.UUID
			scope         string
			content       string
			confidence    float64
			utilityScore  float64
			sensitivity   string
			trustTier     float64
			embeddingText *string
		)
		if err := rows.Scan(
			&sourceID,
			&projectID,
			&projectTaskID,
			&agentID,
			&scope,
			&content,
			&confidence,
			&utilityScore,
			&sensitivity,
			&trustTier,
			&embeddingText,
		); err != nil {
			return repo.MemoryCompactionRun{}, err
		}

		_ = embeddingText
		embedding := deterministicMemoryEmbedding(content)

		distilledContent := "Distilled memory: " + strings.TrimSpace(content)
		semantic, err := memoryRepo.Create(ctx, repo.Memory{
			OrganizationID: orgID,
			ProjectID:      projectID,
			ProjectTaskID:  projectTaskID,
			AgentID:        agentID,
			MemoryType:     "semantic",
			Scope:          strings.TrimSpace(scope),
			Content:        distilledContent,
			ContentHash:    hashMemoryContent(distilledContent + sourceID.String()),
			Embedding:      embedding,
			Confidence:     clampFloat(confidence+0.1, 0, 1),
			UtilityScore:   clampFloat(utilityScore+0.1, 0, 1),
			Status:         "active",
			Sensitivity:    sensitivity,
			TrustTier:      clampFloat(trustTier, 0, 1),
		})
		if err != nil {
			return repo.MemoryCompactionRun{}, err
		}
		created++

		if _, err := memoryRepo.Supersede(ctx, sourceID, semantic.ID); err != nil {
			return repo.MemoryCompactionRun{}, err
		}
		updated++
		archived++
	}
	if rows.Err() != nil {
		return repo.MemoryCompactionRun{}, rows.Err()
	}

	if _, err := runRepo.UpdateCounts(ctx, run.ID, examined, updated, archived, created); err != nil {
		return repo.MemoryCompactionRun{}, err
	}
	completedAt := time.Now().UTC()
	final, err := runRepo.UpdateStatus(ctx, run.ID, "completed", nil, &startedAt, &completedAt)
	if err != nil {
		return repo.MemoryCompactionRun{}, err
	}
	return final, nil
}

func extractMemoryFactContent(content string) (string, bool) {
	trimmed := strings.TrimSpace(content)
	lower := strings.ToLower(trimmed)
	const prefix = "[memory-fact]"
	if !strings.HasPrefix(lower, prefix) {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[len(prefix):])
	if rest == "" {
		return "", false
	}
	return rest, true
}

func hashMemoryContent(content string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(content)))
	return hex.EncodeToString(sum[:])
}

func deterministicMemoryEmbedding(content string) []float32 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(strings.ToLower(strings.TrimSpace(content))))
	seed := hasher.Sum64()
	out := make([]float32, 1536)
	out[0] = float32(seed%1000)/1000 + 0.001
	out[1] = 1
	return out
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func toMemoryItemRecord(item repo.Memory) memoryItemRecord {
	return memoryItemRecord{
		ID:                item.ID,
		OrganizationID:    item.OrganizationID,
		ProjectID:         item.ProjectID,
		ProjectTaskID:     item.ProjectTaskID,
		AgentID:           item.AgentID,
		MemoryType:        item.MemoryType,
		Scope:             item.Scope,
		Content:           item.Content,
		ContentHash:       item.ContentHash,
		Confidence:        item.Confidence,
		UtilityScore:      item.UtilityScore,
		ExtractionScore:   item.ExtractionScore,
		Status:            item.Status,
		IsHardened:        item.IsHardened,
		Sensitivity:       item.Sensitivity,
		TrustTier:         item.TrustTier,
		FileBacked:        item.FileBacked,
		FilePath:          item.FilePath,
		FileLastScannedAt: item.FileLastScannedAt,
		SupersededBy:      item.SupersededBy,
		SupersededAt:      item.SupersededAt,
		ArchivedAt:        item.ArchivedAt,
		CreatedAt:         item.CreatedAt,
		UpdatedAt:         item.UpdatedAt,
	}
}

func toMemoryImportRecord(item repo.MemoryImport) memoryImportRecord {
	return memoryImportRecord{
		ID:               item.ID,
		Status:           item.Status,
		TotalRecords:     item.TotalRecords,
		ProcessedRecords: item.ProcessedRecords,
		ImportedRecords:  item.ImportedRecords,
		RejectedRecords:  item.RejectedRecords,
		ErrorMessage:     item.ErrorMessage,
		StartedAt:        item.StartedAt,
		CompletedAt:      item.CompletedAt,
		CreatedAt:        item.CreatedAt,
	}
}

type taxonomyNodeComputed struct {
	node  repo.MemoryTaxonomyNode
	path  string
	depth int
	count int
}

func buildTaxonomyTree(nodes []repo.MemoryTaxonomyNode, counts map[uuid.UUID]int) ([]memoryTaxonomyNodeRecord, []memoryTaxonomyNodeRecord) {
	if counts == nil {
		counts = map[uuid.UUID]int{}
	}

	byID := make(map[uuid.UUID]repo.MemoryTaxonomyNode, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}

	computed := make(map[uuid.UUID]taxonomyNodeComputed, len(nodes))
	for _, node := range nodes {
		resolveTaxonomyNode(node.ID, byID, counts, computed, map[uuid.UUID]struct{}{})
	}

	childrenByParent := make(map[uuid.UUID][]uuid.UUID)
	roots := make([]uuid.UUID, 0)
	for _, node := range nodes {
		if node.ParentID == nil {
			roots = append(roots, node.ID)
			continue
		}
		if _, ok := byID[*node.ParentID]; !ok {
			roots = append(roots, node.ID)
			continue
		}
		childrenByParent[*node.ParentID] = append(childrenByParent[*node.ParentID], node.ID)
	}

	sortNodeIDs := func(ids []uuid.UUID) {
		sort.Slice(ids, func(i, j int) bool {
			left := computed[ids[i]]
			right := computed[ids[j]]
			if left.path == right.path {
				return left.node.ID.String() < right.node.ID.String()
			}
			return left.path < right.path
		})
	}
	sortNodeIDs(roots)
	for parentID := range childrenByParent {
		sortNodeIDs(childrenByParent[parentID])
	}

	flat := make([]memoryTaxonomyNodeRecord, 0, len(nodes))
	for _, node := range nodes {
		meta := computed[node.ID]
		flat = append(flat, memoryTaxonomyNodeRecord{
			ID:          node.ID,
			Name:        strings.TrimSpace(node.DisplayName),
			ParentID:    node.ParentID,
			Path:        meta.path,
			Depth:       meta.depth,
			MemoryCount: meta.count,
		})
	}
	sort.Slice(flat, func(i, j int) bool {
		if flat[i].Path == flat[j].Path {
			return flat[i].ID.String() < flat[j].ID.String()
		}
		return flat[i].Path < flat[j].Path
	})

	var buildTree func(nodeID uuid.UUID) memoryTaxonomyNodeRecord
	buildTree = func(nodeID uuid.UUID) memoryTaxonomyNodeRecord {
		meta := computed[nodeID]
		item := memoryTaxonomyNodeRecord{
			ID:          nodeID,
			Name:        strings.TrimSpace(meta.node.DisplayName),
			ParentID:    meta.node.ParentID,
			Path:        meta.path,
			Depth:       meta.depth,
			MemoryCount: meta.count,
		}
		childIDs := childrenByParent[nodeID]
		if len(childIDs) > 0 {
			item.Children = make([]memoryTaxonomyNodeRecord, 0, len(childIDs))
			for _, childID := range childIDs {
				item.Children = append(item.Children, buildTree(childID))
			}
		}
		return item
	}

	tree := make([]memoryTaxonomyNodeRecord, 0, len(roots))
	for _, rootID := range roots {
		tree = append(tree, buildTree(rootID))
	}

	return tree, flat
}

func resolveTaxonomyNode(
	nodeID uuid.UUID,
	byID map[uuid.UUID]repo.MemoryTaxonomyNode,
	counts map[uuid.UUID]int,
	computed map[uuid.UUID]taxonomyNodeComputed,
	stack map[uuid.UUID]struct{},
) taxonomyNodeComputed {
	if existing, ok := computed[nodeID]; ok {
		return existing
	}
	node, ok := byID[nodeID]
	if !ok {
		return taxonomyNodeComputed{}
	}

	slug := strings.TrimSpace(node.Slug)
	if slug == "" {
		slug = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(node.DisplayName), " ", "-"))
		if slug == "" {
			slug = node.ID.String()
		}
	}

	depth := 0
	path := slug
	if node.ParentID != nil {
		if _, seen := stack[nodeID]; !seen {
			parent, exists := byID[*node.ParentID]
			if exists {
				nextStack := make(map[uuid.UUID]struct{}, len(stack)+1)
				for key := range stack {
					nextStack[key] = struct{}{}
				}
				nextStack[nodeID] = struct{}{}
				parentMeta := resolveTaxonomyNode(parent.ID, byID, counts, computed, nextStack)
				depth = parentMeta.depth + 1
				if parentMeta.path != "" {
					path = parentMeta.path + "/" + slug
				}
			}
		}
	}

	meta := taxonomyNodeComputed{
		node:  node,
		path:  path,
		depth: depth,
		count: counts[nodeID],
	}
	computed[nodeID] = meta
	return meta
}

func initDefaultMemoryImporter(pool *pgxpool.Pool, store storage.Store) memoryImporter {
	if pool == nil || store == nil {
		return nil
	}
	importer, err := memoryimporter.NewImporter(memoryimporter.ImporterOptions{
		Pool:  pool,
		Store: store,
	})
	if err != nil {
		return nil
	}
	return importer
}
