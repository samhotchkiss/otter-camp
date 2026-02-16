package memory

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var markdownLinkPattern = regexp.MustCompile(`\[[^\]]+\]\(([^)]+)\)`)

type EllieProjectDocChangeStatus string

const (
	EllieProjectDocChangeStatusNew       EllieProjectDocChangeStatus = "new"
	EllieProjectDocChangeStatusChanged   EllieProjectDocChangeStatus = "changed"
	EllieProjectDocChangeStatusUnchanged EllieProjectDocChangeStatus = "unchanged"
)

type EllieKnownProjectDoc struct {
	FilePath    string
	ContentHash string
}

type EllieDiscoveredProjectDoc struct {
	FilePath        string
	Title           string
	Content         string
	ContentHash     string
	ChangeStatus    EllieProjectDocChangeStatus
	StartHereLinked bool
}

type EllieProjectDocsScanInput struct {
	ProjectRoot string
	KnownDocs   []EllieKnownProjectDoc
}

type EllieProjectDocsScanResult struct {
	Documents    []EllieDiscoveredProjectDoc
	DeletedPaths []string
}

type EllieProjectDocsScanner struct{}

func (s *EllieProjectDocsScanner) Scan(
	ctx context.Context,
	input EllieProjectDocsScanInput,
) (EllieProjectDocsScanResult, error) {
	root := strings.TrimSpace(input.ProjectRoot)
	if root == "" {
		return EllieProjectDocsScanResult{}, fmt.Errorf("project root is required")
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return EllieProjectDocsScanResult{}, fmt.Errorf("resolve project root: %w", err)
	}
	docsRoot := filepath.Join(absRoot, "docs")

	knownHashes := make(map[string]string, len(input.KnownDocs))
	for _, knownDoc := range input.KnownDocs {
		normalizedPath := normalizeProjectDocPath(knownDoc.FilePath)
		if normalizedPath == "" {
			continue
		}
		knownHashes[normalizedPath] = strings.TrimSpace(knownDoc.ContentHash)
	}

	if info, err := os.Stat(docsRoot); err != nil || !info.IsDir() {
		deletedPaths := make([]string, 0, len(knownHashes))
		for path := range knownHashes {
			deletedPaths = append(deletedPaths, path)
		}
		sort.Strings(deletedPaths)
		return EllieProjectDocsScanResult{DeletedPaths: deletedPaths}, nil
	}

	documents := make([]EllieDiscoveredProjectDoc, 0)
	err = filepath.WalkDir(docsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}

		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read project doc %q: %w", path, err)
		}

		relPath, err := filepath.Rel(absRoot, path)
		if err != nil {
			return fmt.Errorf("compute project doc relative path: %w", err)
		}
		normalizedPath := normalizeProjectDocPath(relPath)
		content := string(contentBytes)
		contentHash := md5HexString(contentBytes)
		title := extractMarkdownTitle(normalizedPath, content)

		changeStatus := EllieProjectDocChangeStatusNew
		if existingHash, ok := knownHashes[normalizedPath]; ok {
			if existingHash == contentHash {
				changeStatus = EllieProjectDocChangeStatusUnchanged
			} else {
				changeStatus = EllieProjectDocChangeStatusChanged
			}
			delete(knownHashes, normalizedPath)
		}

		documents = append(documents, EllieDiscoveredProjectDoc{
			FilePath:     normalizedPath,
			Title:        title,
			Content:      content,
			ContentHash:  contentHash,
			ChangeStatus: changeStatus,
		})
		return nil
	})
	if err != nil {
		return EllieProjectDocsScanResult{}, err
	}

	sort.Slice(documents, func(i, j int) bool {
		return documents[i].FilePath < documents[j].FilePath
	})

	startHereLinks := discoverStartHereLinkedDocs(absRoot, docsRoot, documents)
	for i := range documents {
		if _, ok := startHereLinks[documents[i].FilePath]; ok {
			documents[i].StartHereLinked = true
		}
	}

	deletedPaths := make([]string, 0, len(knownHashes))
	for path := range knownHashes {
		deletedPaths = append(deletedPaths, path)
	}
	sort.Strings(deletedPaths)

	return EllieProjectDocsScanResult{
		Documents:    documents,
		DeletedPaths: deletedPaths,
	}, nil
}

func discoverStartHereLinkedDocs(
	projectRoot string,
	docsRoot string,
	documents []EllieDiscoveredProjectDoc,
) map[string]struct{} {
	linked := make(map[string]struct{})
	startHerePath := "docs/START-HERE.md"

	var startHereContent string
	for _, doc := range documents {
		if doc.FilePath == startHerePath {
			startHereContent = doc.Content
			break
		}
	}
	if startHereContent == "" {
		return linked
	}

	baseDir := filepath.Join(projectRoot, "docs")
	matches := markdownLinkPattern.FindAllStringSubmatch(startHereContent, -1)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		target := strings.TrimSpace(match[1])
		if target == "" || strings.HasPrefix(target, "#") {
			continue
		}
		targetLower := strings.ToLower(target)
		if strings.HasPrefix(targetLower, "http://") ||
			strings.HasPrefix(targetLower, "https://") ||
			strings.HasPrefix(targetLower, "mailto:") {
			continue
		}

		if hash := strings.Index(target, "#"); hash >= 0 {
			target = target[:hash]
		}
		if query := strings.Index(target, "?"); query >= 0 {
			target = target[:query]
		}
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		var candidate string
		if strings.HasPrefix(target, "/") {
			candidate = filepath.Join(projectRoot, target)
		} else {
			candidate = filepath.Join(baseDir, target)
		}
		candidate = filepath.Clean(candidate)
		relToDocs, err := filepath.Rel(docsRoot, candidate)
		if err != nil {
			continue
		}
		if relToDocs == ".." || strings.HasPrefix(relToDocs, ".."+string(os.PathSeparator)) {
			continue
		}
		if !strings.EqualFold(filepath.Ext(candidate), ".md") {
			continue
		}

		relToProject, err := filepath.Rel(projectRoot, candidate)
		if err != nil {
			continue
		}
		normalizedPath := normalizeProjectDocPath(relToProject)
		if normalizedPath == "" || normalizedPath == startHerePath {
			continue
		}
		linked[normalizedPath] = struct{}{}
	}
	return linked
}

func extractMarkdownTitle(filePath, content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if title != "" {
			return title
		}
	}
	base := filepath.Base(filePath)
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func md5HexString(payload []byte) string {
	sum := md5.Sum(payload)
	return hex.EncodeToString(sum[:])
}

func normalizeProjectDocPath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.ToSlash(filepath.Clean(trimmed))
	cleaned = strings.TrimPrefix(cleaned, "./")
	if cleaned == "." || cleaned == "" {
		return ""
	}
	return cleaned
}
