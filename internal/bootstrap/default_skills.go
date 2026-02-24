package bootstrap

import (
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed defaults/skills/*.md
var defaultSkillsFS embed.FS

type embeddedSkill struct {
	Slug        string
	DisplayName string
	Description string
	FileName    string
	Content     []byte
}

var defaultSkillMetadata = map[string]struct {
	DisplayName string
	Description string
}{
	"summarize": {
		DisplayName: "Summarize",
		Description: "Summarize context into concise, accurate takeaways.",
	},
	"code-review": {
		DisplayName: "Code Review",
		Description: "Review code for correctness, risk, and missing coverage.",
	},
	"plan-task": {
		DisplayName: "Plan Task",
		Description: "Turn objectives into concrete, verifiable implementation steps.",
	},
}

func loadDefaultSkills() ([]embeddedSkill, error) {
	matches, err := fs.Glob(defaultSkillsFS, "defaults/skills/*.md")
	if err != nil {
		return nil, fmt.Errorf("glob default skills: %w", err)
	}
	sort.Strings(matches)

	skills := make([]embeddedSkill, 0, len(matches))
	for _, path := range matches {
		content, err := defaultSkillsFS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read default skill %s: %w", path, err)
		}

		fileName := filepath.Base(path)
		slug := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		meta, ok := defaultSkillMetadata[slug]
		if !ok {
			return nil, fmt.Errorf("missing metadata for default skill %q", slug)
		}

		skills = append(skills, embeddedSkill{
			Slug:        slug,
			DisplayName: meta.DisplayName,
			Description: meta.Description,
			FileName:    fileName,
			Content:     content,
		})
	}

	return skills, nil
}
