package worker

import (
	"context"
	"crypto/sha256"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
)

func TestDeterministicQueryEmbedderUsesStableHashProjection(t *testing.T) {
	embedder := deterministicQueryEmbedder{}

	vectors, err := embedder.Embed(context.Background(), uuid.New(), "memory_retrieval", []string{"  hello world  "})
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vectors) != 1 {
		t.Fatalf("vector count = %d, want 1", len(vectors))
	}

	vector := vectors[0]
	if len(vector) != deterministicQueryEmbeddingDimensions {
		t.Fatalf("vector dimensions = %d, want %d", len(vector), deterministicQueryEmbeddingDimensions)
	}

	sum := sha256.Sum256([]byte("hello world"))
	if vector[0] != float32(sum[0])/255.0 {
		t.Fatalf("vector[0] = %f, want %f", vector[0], float32(sum[0])/255.0)
	}
	if vector[1] != 1 {
		t.Fatalf("vector[1] = %f, want 1", vector[1])
	}
	for i := 0; i < len(sum); i++ {
		want := float32(sum[i]) / 255.0
		if vector[i+2] != want {
			t.Fatalf("vector[%d] = %f, want %f", i+2, vector[i+2], want)
		}
	}
}

func TestDeterministicQueryEmbedderTrimsInput(t *testing.T) {
	embedder := deterministicQueryEmbedder{}

	vectors, err := embedder.Embed(context.Background(), uuid.New(), "memory_retrieval", []string{"hello", "  hello  "})
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if len(vectors) != 2 {
		t.Fatalf("vector count = %d, want 2", len(vectors))
	}

	if len(vectors[0]) != len(vectors[1]) {
		t.Fatalf("vector dimensions differ: %d != %d", len(vectors[0]), len(vectors[1]))
	}
	for i := range vectors[0] {
		if vectors[0][i] != vectors[1][i] {
			t.Fatalf("trimmed and untrimmed embeddings differ at index %d", i)
		}
	}
}

func TestWorkerTurnEngineWiresBootstrapPromotionDependencies(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	workerFile := filepath.Join(filepath.Dir(thisFile), "worker.go")

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, workerFile, nil, 0)
	if err != nil {
		t.Fatalf("ParseFile worker.go: %v", err)
	}

	required := map[string]bool{
		"Projects":        false,
		"Organizations":   false,
		"FlowNodes":       false,
		"FlowAdvancer":    false,
		"Assignments":     false,
		"Environments":    false,
		"TaskTransitions": false,
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "NewEngine" {
			return true
		}
		composite, ok := call.Args[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		typeSel, ok := composite.Type.(*ast.SelectorExpr)
		if !ok || typeSel.Sel == nil || typeSel.Sel.Name != "Options" {
			return true
		}
		for _, elt := range composite.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			if _, exists := required[key.Name]; exists {
				required[key.Name] = true
			}
		}
		return false
	})

	for field, found := range required {
		if !found {
			t.Fatalf("turn.NewEngine worker wiring is missing %s", field)
		}
	}
}
