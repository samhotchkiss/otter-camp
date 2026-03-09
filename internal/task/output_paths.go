package task

import (
	"path/filepath"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/taskcheckpoint"
)

const recoveryArtifactPathRoot = ".ottercamp/recovery"

func NormalizeTaskDescriptionOutputPaths(title, description string) string {
	replacements := canonicalTaskOutputReplacements(title)
	if len(replacements) == 0 || strings.TrimSpace(description) == "" {
		return description
	}
	normalized := description
	for oldPath, newPath := range replacements {
		normalized = strings.ReplaceAll(normalized, oldPath, newPath)
		normalized = strings.ReplaceAll(normalized, "`"+oldPath+"`", "`"+newPath+"`")
	}
	return normalized
}

func NormalizeRecoveryCheckpointForTask(title string, checkpoint taskcheckpoint.RecoveryFileWriteCheckpoint) taskcheckpoint.RecoveryFileWriteCheckpoint {
	replacements := canonicalTaskOutputReplacements(title)
	if len(replacements) == 0 {
		return taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
	}

	targetPath := strings.TrimSpace(checkpoint.TargetPath)
	if targetPath == "" {
		if inferred, ok := targetPathFromRecoveryArtifact(strings.TrimSpace(checkpoint.ArtifactPath)); ok {
			targetPath = inferred
		}
	}
	if targetPath != "" {
		targetPath = CanonicalTaskOutputPath(title, targetPath)
		checkpoint.TargetPath = targetPath
		checkpoint.ArtifactPath = recoveryArtifactPathForTarget(targetPath)
	}
	return taskcheckpoint.NormalizeRecoveryFileWriteCheckpoint(checkpoint)
}

func targetPathFromRecoveryArtifact(artifactPath string) (string, bool) {
	artifactPath = filepath.ToSlash(strings.TrimSpace(artifactPath))
	prefix := recoveryArtifactPathRoot + "/"
	if !strings.HasPrefix(artifactPath, prefix) || len(artifactPath) <= len(prefix) {
		return "", false
	}
	return strings.TrimPrefix(artifactPath, prefix), true
}

func CanonicalTaskOutputPath(title, path string) string {
	trimmed := filepath.ToSlash(strings.TrimSpace(path))
	if trimmed == "" {
		return ""
	}
	if replacement, ok := canonicalTaskOutputReplacements(title)[trimmed]; ok {
		return replacement
	}
	return trimmed
}

func CanonicalRecoveryArtifactPath(title, artifactPath string) string {
	artifactPath = filepath.ToSlash(strings.TrimSpace(artifactPath))
	if artifactPath == "" {
		return ""
	}
	if targetPath, ok := targetPathFromRecoveryArtifact(artifactPath); ok {
		return recoveryArtifactPathForTarget(CanonicalTaskOutputPath(title, targetPath))
	}
	return artifactPath
}

func recoveryArtifactPathForTarget(targetPath string) string {
	targetPath = filepath.ToSlash(strings.TrimSpace(targetPath))
	if targetPath == "" {
		return ""
	}
	return filepath.ToSlash(filepath.Join(recoveryArtifactPathRoot, filepath.FromSlash(targetPath)))
}

func canonicalTaskOutputReplacements(title string) map[string]string {
	lowerTitle := strings.ToLower(strings.TrimSpace(title))
	switch {
	case strings.Contains(lowerTitle, "content strategy"):
		return map[string]string{
			"strategy/content-strategy.md":             "docs/content-strategy.md",
			"strategy/audience-personas.md":            "docs/audience-personas.md",
			"strategy/editorial-calendar-framework.md": "docs/editorial-calendar-framework.md",
		}
	case strings.Contains(lowerTitle, "blog post ideas"):
		return map[string]string{
			"content/blog-post-ideas.md": "docs/blog-post-ideas.md",
		}
	default:
		return nil
	}
}
