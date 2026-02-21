package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/samhotchkiss/otter-camp/internal/store"
)

type ellieTaxonomySeedRoot struct {
	Slug        string
	DisplayName string
	Description string
}

var defaultEllieTaxonomyRoots = []ellieTaxonomySeedRoot{
	{
		Slug:        "personal",
		DisplayName: "Personal",
		Description: "Family, vehicles, health, location, and preferences.",
	},
	{
		Slug:        "projects",
		DisplayName: "Projects",
		Description: "Project-specific operational and product memory.",
	},
	{
		Slug:        "technical",
		DisplayName: "Technical",
		Description: "Decisions, patterns, architecture, and infrastructure.",
	},
	{
		Slug:        "agents",
		DisplayName: "Agents",
		Description: "Agent definitions, roles, and behavior constraints.",
	},
	{
		Slug:        "process",
		DisplayName: "Process",
		Description: "Workflows, pipelines, and engineering practices.",
	},
}

func SeedDefaultEllieTaxonomy(ctx context.Context, taxonomyStore *store.EllieTaxonomyStore, orgID string) error {
	if taxonomyStore == nil {
		return fmt.Errorf("taxonomy store is required")
	}

	normalizedOrgID := strings.TrimSpace(orgID)
	if normalizedOrgID == "" {
		return fmt.Errorf("org_id is required")
	}

	existingRoots, err := taxonomyStore.ListNodesByParent(ctx, normalizedOrgID, nil, 100)
	if err != nil {
		return fmt.Errorf("list existing taxonomy roots: %w", err)
	}

	existingBySlug := make(map[string]struct{}, len(existingRoots))
	for _, node := range existingRoots {
		existingBySlug[strings.ToLower(strings.TrimSpace(node.Slug))] = struct{}{}
	}

	for _, root := range defaultEllieTaxonomyRoots {
		slug := strings.ToLower(strings.TrimSpace(root.Slug))
		if _, exists := existingBySlug[slug]; exists {
			continue
		}

		description := root.Description
		_, err := taxonomyStore.CreateNode(ctx, store.CreateEllieTaxonomyNodeInput{
			OrgID:       normalizedOrgID,
			Slug:        slug,
			DisplayName: root.DisplayName,
			Description: &description,
		})
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				continue
			}
			return fmt.Errorf("seed default taxonomy root %q: %w", slug, err)
		}
	}

	return nil
}
