package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

const (
	defaultEllieTaxonomyClassifierBatchSize      = 100
	defaultEllieTaxonomyClassifierMaxAssignments = 3
	defaultEllieTaxonomyClassifierMaxRetries     = 1
)

var errEllieTaxonomyMalformedOutput = errors.New("taxonomy classifier malformed output")

type EllieTaxonomyClassifierStore interface {
	ListAllNodes(ctx context.Context, orgID string) ([]store.EllieTaxonomyNode, error)
	ListPendingMemoriesForClassification(ctx context.Context, orgID string, limit int) ([]store.EllieTaxonomyPendingMemory, error)
	UpsertMemoryClassification(ctx context.Context, input store.UpsertEllieMemoryTaxonomyInput) error
	MarkMemoryTaxonomyClassified(
		ctx context.Context,
		orgID,
		memoryID string,
		classifiedAt time.Time,
		classifierModel,
		classifierTraceID string,
	) error
}

type EllieTaxonomyLLMClassificationInput struct {
	OrgID          string
	MemoryID       string
	MemoryTitle    string
	MemoryContent  string
	TaxonomyPaths  []string
	MaxAssignments int
	Prompt         string
}

type EllieTaxonomyLLMClassificationOutput struct {
	Model   string
	TraceID string
	RawJSON string
}

type EllieTaxonomyLLMClassifier interface {
	ClassifyMemory(ctx context.Context, input EllieTaxonomyLLMClassificationInput) (EllieTaxonomyLLMClassificationOutput, error)
}

type EllieTaxonomyClassifierWorkerConfig struct {
	CandidateBatch int
	MaxAssignments int
	MaxRetries     int
	LLM            EllieTaxonomyLLMClassifier
	Now            func() time.Time
}

type EllieTaxonomyClassifierRunResult struct {
	PendingMemories    int
	ClassifiedMemories int
	InvalidOutputs     int
}

type EllieTaxonomyClassifierWorker struct {
	Store          EllieTaxonomyClassifierStore
	CandidateBatch int
	MaxAssignments int
	MaxRetries     int
	LLM            EllieTaxonomyLLMClassifier
	now            func() time.Time
}

type ellieTaxonomyClassificationAssignment struct {
	Path       string
	Confidence float64
}

func NewEllieTaxonomyClassifierWorker(
	taxonomyStore EllieTaxonomyClassifierStore,
	cfg EllieTaxonomyClassifierWorkerConfig,
) *EllieTaxonomyClassifierWorker {
	batchSize := cfg.CandidateBatch
	if batchSize <= 0 {
		batchSize = defaultEllieTaxonomyClassifierBatchSize
	}
	maxAssignments := cfg.MaxAssignments
	if maxAssignments <= 0 {
		maxAssignments = defaultEllieTaxonomyClassifierMaxAssignments
	}
	if maxAssignments > defaultEllieTaxonomyClassifierMaxAssignments {
		maxAssignments = defaultEllieTaxonomyClassifierMaxAssignments
	}
	maxRetries := cfg.MaxRetries
	if maxRetries < 0 {
		maxRetries = defaultEllieTaxonomyClassifierMaxRetries
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}

	return &EllieTaxonomyClassifierWorker{
		Store:          taxonomyStore,
		CandidateBatch: batchSize,
		MaxAssignments: maxAssignments,
		MaxRetries:     maxRetries,
		LLM:            cfg.LLM,
		now:            nowFn,
	}
}

func (w *EllieTaxonomyClassifierWorker) RunOnce(ctx context.Context, orgID string) (EllieTaxonomyClassifierRunResult, error) {
	if w == nil {
		return EllieTaxonomyClassifierRunResult{}, fmt.Errorf("ellie taxonomy classifier worker is nil")
	}
	if w.Store == nil {
		return EllieTaxonomyClassifierRunResult{}, fmt.Errorf("taxonomy classifier store is required")
	}
	if w.LLM == nil {
		return EllieTaxonomyClassifierRunResult{}, fmt.Errorf("taxonomy llm classifier is required")
	}

	orgID = strings.TrimSpace(orgID)
	if orgID == "" {
		return EllieTaxonomyClassifierRunResult{}, fmt.Errorf("org_id is required")
	}

	nodes, err := w.Store.ListAllNodes(ctx, orgID)
	if err != nil {
		return EllieTaxonomyClassifierRunResult{}, fmt.Errorf("list taxonomy nodes: %w", err)
	}
	if len(nodes) == 0 {
		return EllieTaxonomyClassifierRunResult{}, nil
	}

	pathToNodeID, taxonomyPaths, err := ellieBuildTaxonomyPathIndex(nodes)
	if err != nil {
		return EllieTaxonomyClassifierRunResult{}, err
	}

	pending, err := w.Store.ListPendingMemoriesForClassification(ctx, orgID, w.CandidateBatch)
	if err != nil {
		return EllieTaxonomyClassifierRunResult{}, fmt.Errorf("list pending taxonomy memories: %w", err)
	}

	result := EllieTaxonomyClassifierRunResult{PendingMemories: len(pending)}
	for _, memory := range pending {
		assignments, output, classifyErr := w.classifyWithRetry(ctx, orgID, memory, taxonomyPaths)
		if classifyErr != nil {
			if errors.Is(classifyErr, errEllieTaxonomyMalformedOutput) {
				result.InvalidOutputs += 1
				continue
			}
			return result, classifyErr
		}

		nodeAssignments := make([]store.UpsertEllieMemoryTaxonomyInput, 0, len(assignments))
		seenNodeIDs := make(map[string]struct{}, len(assignments))
		invalidPath := false
		for _, assignment := range assignments {
			nodeID, ok := pathToNodeID[assignment.Path]
			if !ok {
				invalidPath = true
				break
			}
			if _, seen := seenNodeIDs[nodeID]; seen {
				continue
			}
			seenNodeIDs[nodeID] = struct{}{}
			nodeAssignments = append(nodeAssignments, store.UpsertEllieMemoryTaxonomyInput{
				OrgID:      orgID,
				MemoryID:   memory.MemoryID,
				NodeID:     nodeID,
				Confidence: assignment.Confidence,
			})
			if len(nodeAssignments) >= w.MaxAssignments {
				break
			}
		}
		if invalidPath || len(nodeAssignments) == 0 {
			result.InvalidOutputs += 1
			continue
		}

		classifiedAt := w.now().UTC()
		for _, assignment := range nodeAssignments {
			assignment.ClassifiedAt = classifiedAt
			if err := w.Store.UpsertMemoryClassification(ctx, assignment); err != nil {
				return result, fmt.Errorf("upsert taxonomy classification for memory %s: %w", strings.TrimSpace(memory.MemoryID), err)
			}
		}
		if err := w.Store.MarkMemoryTaxonomyClassified(
			ctx,
			orgID,
			strings.TrimSpace(memory.MemoryID),
			classifiedAt,
			strings.TrimSpace(output.Model),
			strings.TrimSpace(output.TraceID),
		); err != nil {
			return result, fmt.Errorf("mark memory taxonomy classified %s: %w", strings.TrimSpace(memory.MemoryID), err)
		}

		result.ClassifiedMemories += 1
	}

	return result, nil
}

func (w *EllieTaxonomyClassifierWorker) classifyWithRetry(
	ctx context.Context,
	orgID string,
	memory store.EllieTaxonomyPendingMemory,
	taxonomyPaths []string,
) ([]ellieTaxonomyClassificationAssignment, EllieTaxonomyLLMClassificationOutput, error) {
	attempts := w.MaxRetries + 1
	if attempts <= 0 {
		attempts = 1
	}

	var (
		lastParseErr error
		lastLLMErr   error
	)
	for attempt := 0; attempt < attempts; attempt += 1 {
		response, err := w.LLM.ClassifyMemory(ctx, EllieTaxonomyLLMClassificationInput{
			OrgID:          orgID,
			MemoryID:       strings.TrimSpace(memory.MemoryID),
			MemoryTitle:    strings.TrimSpace(memory.Title),
			MemoryContent:  strings.TrimSpace(memory.Content),
			TaxonomyPaths:  append([]string(nil), taxonomyPaths...),
			MaxAssignments: w.MaxAssignments,
			Prompt: buildEllieTaxonomyClassificationPrompt(
				strings.TrimSpace(memory.Title),
				strings.TrimSpace(memory.Content),
				taxonomyPaths,
				w.MaxAssignments,
			),
		})
		if err != nil {
			lastLLMErr = err
			continue
		}

		assignments, parseErr := parseEllieTaxonomyClassifications(response.RawJSON, w.MaxAssignments)
		if parseErr != nil {
			lastParseErr = parseErr
			continue
		}
		return assignments, response, nil
	}

	if lastParseErr != nil {
		return nil, EllieTaxonomyLLMClassificationOutput{}, fmt.Errorf("%w: %v", errEllieTaxonomyMalformedOutput, lastParseErr)
	}
	if lastLLMErr != nil {
		return nil, EllieTaxonomyLLMClassificationOutput{}, fmt.Errorf("llm taxonomy classification failed: %w", lastLLMErr)
	}
	return nil, EllieTaxonomyLLMClassificationOutput{}, fmt.Errorf("%w: no usable classification output", errEllieTaxonomyMalformedOutput)
}

func buildEllieTaxonomyClassificationPrompt(title, content string, taxonomyPaths []string, maxAssignments int) string {
	if maxAssignments <= 0 {
		maxAssignments = defaultEllieTaxonomyClassifierMaxAssignments
	}
	if maxAssignments > defaultEllieTaxonomyClassifierMaxAssignments {
		maxAssignments = defaultEllieTaxonomyClassifierMaxAssignments
	}

	var builder strings.Builder
	builder.WriteString("You classify a memory into a fixed taxonomy. Return strict JSON only.\\n")
	builder.WriteString("Output schema: {\"classifications\":[{\"path\":\"a/b\",\"confidence\":0.0}]}\\n")
	builder.WriteString(fmt.Sprintf("Return 1-%d classifications.\\n", maxAssignments))
	builder.WriteString("Allowed taxonomy paths:\\n")
	for _, path := range taxonomyPaths {
		builder.WriteString("- ")
		builder.WriteString(path)
		builder.WriteString("\\n")
	}
	builder.WriteString("Memory title:\\n")
	builder.WriteString(title)
	builder.WriteString("\\nMemory content:\\n")
	builder.WriteString(content)
	return builder.String()
}

type ellieTaxonomyClassificationResponse struct {
	Classifications []ellieTaxonomyClassificationRow `json:"classifications"`
	Nodes           []ellieTaxonomyClassificationRow `json:"nodes"`
}

type ellieTaxonomyClassificationRow struct {
	Path       string   `json:"path"`
	NodePath   string   `json:"node_path"`
	Confidence *float64 `json:"confidence"`
}

func parseEllieTaxonomyClassifications(raw string, maxAssignments int) ([]ellieTaxonomyClassificationAssignment, error) {
	if maxAssignments <= 0 {
		maxAssignments = defaultEllieTaxonomyClassifierMaxAssignments
	}
	if maxAssignments > defaultEllieTaxonomyClassifierMaxAssignments {
		maxAssignments = defaultEllieTaxonomyClassifierMaxAssignments
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("empty taxonomy classification output")
	}

	var response ellieTaxonomyClassificationResponse
	if err := json.Unmarshal([]byte(trimmed), &response); err != nil {
		return nil, fmt.Errorf("decode taxonomy classification output: %w", err)
	}

	rows := response.Classifications
	if len(rows) == 0 {
		rows = response.Nodes
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("taxonomy classification output is empty")
	}

	assignments := make([]ellieTaxonomyClassificationAssignment, 0, len(rows))
	seenPaths := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		path := normalizeEllieTaxonomyPath(row.Path)
		if path == "" {
			path = normalizeEllieTaxonomyPath(row.NodePath)
		}
		if path == "" {
			continue
		}
		if _, seen := seenPaths[path]; seen {
			continue
		}
		seenPaths[path] = struct{}{}

		confidence := 0.5
		if row.Confidence != nil {
			confidence = *row.Confidence
		}
		if math.IsNaN(confidence) || confidence < 0 {
			confidence = 0
		}
		if confidence > 1 {
			confidence = 1
		}

		assignments = append(assignments, ellieTaxonomyClassificationAssignment{
			Path:       path,
			Confidence: confidence,
		})
		if len(assignments) >= maxAssignments {
			break
		}
	}

	if len(assignments) == 0 {
		return nil, fmt.Errorf("taxonomy classification output has no valid node paths")
	}
	return assignments, nil
}

func ellieBuildTaxonomyPathIndex(nodes []store.EllieTaxonomyNode) (map[string]string, []string, error) {
	nodeByID := make(map[string]store.EllieTaxonomyNode, len(nodes))
	for _, node := range nodes {
		nodeID := strings.TrimSpace(node.ID)
		if nodeID == "" {
			continue
		}
		nodeByID[nodeID] = node
	}

	pathCache := make(map[string]string, len(nodeByID))
	resolving := make(map[string]struct{}, len(nodeByID))
	var resolvePath func(nodeID string) (string, error)
	resolvePath = func(nodeID string) (string, error) {
		nodeID = strings.TrimSpace(nodeID)
		if nodeID == "" {
			return "", fmt.Errorf("taxonomy node id is empty")
		}
		if path, ok := pathCache[nodeID]; ok {
			return path, nil
		}
		if _, seen := resolving[nodeID]; seen {
			return "", fmt.Errorf("taxonomy path cycle detected")
		}
		node, ok := nodeByID[nodeID]
		if !ok {
			return "", fmt.Errorf("taxonomy node %s not found", nodeID)
		}
		slug := strings.TrimSpace(strings.ToLower(node.Slug))
		if slug == "" {
			return "", fmt.Errorf("taxonomy node %s has empty slug", nodeID)
		}

		resolving[nodeID] = struct{}{}
		defer delete(resolving, nodeID)

		path := slug
		if node.ParentID != nil {
			parentID := strings.TrimSpace(*node.ParentID)
			if parentID != "" {
				parentPath, err := resolvePath(parentID)
				if err != nil {
					return "", err
				}
				path = parentPath + "/" + slug
			}
		}
		pathCache[nodeID] = path
		return path, nil
	}

	pathToNodeID := make(map[string]string, len(nodeByID))
	paths := make([]string, 0, len(nodeByID))
	for nodeID := range nodeByID {
		path, err := resolvePath(nodeID)
		if err != nil {
			return nil, nil, err
		}
		normalized := normalizeEllieTaxonomyPath(path)
		if normalized == "" {
			continue
		}
		pathToNodeID[normalized] = nodeID
		paths = append(paths, normalized)
	}
	sort.Strings(paths)
	return pathToNodeID, paths, nil
}

func normalizeEllieTaxonomyPath(path string) string {
	path = strings.TrimSpace(strings.ToLower(path))
	if path == "" {
		return ""
	}
	path = strings.ReplaceAll(path, "\\\\", "/")
	segments := strings.Split(path, "/")
	normalized := make([]string, 0, len(segments))
	for _, segment := range segments {
		trimmed := strings.TrimSpace(segment)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return strings.Join(normalized, "/")
}
