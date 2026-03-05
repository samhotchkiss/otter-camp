package version

import (
	_ "embed"
	"strings"
)

//go:embed repo_version.txt
var repoVersionRaw string

// RepoVersion is a monotonic repo-stored counter used for runtime freshness checks.
var RepoVersion = normalizeRepoVersion(repoVersionRaw)

func normalizeRepoVersion(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "0"
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return "0"
		}
	}
	return trimmed
}
