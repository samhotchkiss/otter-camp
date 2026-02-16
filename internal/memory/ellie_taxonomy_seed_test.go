package memory

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/store"
	"github.com/stretchr/testify/require"
)

func TestTaxonomySeedDefaults(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)

	orgID := createTaxonomySeedTestOrganization(t, db, "taxonomy-seed-defaults")
	taxonomyStore := store.NewEllieTaxonomyStore(db)

	err := SeedDefaultEllieTaxonomy(context.Background(), taxonomyStore, orgID)
	require.NoError(t, err)

	roots, err := taxonomyStore.ListNodesByParent(context.Background(), orgID, nil, 100)
	require.NoError(t, err)
	require.Len(t, roots, 5)

	slugs := make([]string, 0, len(roots))
	for _, node := range roots {
		require.Equal(t, 0, node.Depth)
		require.Nil(t, node.ParentID)
		require.NotNil(t, node.Description)
		slugs = append(slugs, node.Slug)
	}
	sort.Strings(slugs)
	require.Equal(t, []string{"agents", "personal", "process", "projects", "technical"}, slugs)
}

func TestTaxonomySeedIsIdempotent(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)

	orgID := createTaxonomySeedTestOrganization(t, db, "taxonomy-seed-idempotent")
	taxonomyStore := store.NewEllieTaxonomyStore(db)

	require.NoError(t, SeedDefaultEllieTaxonomy(context.Background(), taxonomyStore, orgID))
	require.NoError(t, SeedDefaultEllieTaxonomy(context.Background(), taxonomyStore, orgID))

	roots, err := taxonomyStore.ListNodesByParent(context.Background(), orgID, nil, 100)
	require.NoError(t, err)
	require.Len(t, roots, 5)

	counts := map[string]int{}
	for _, node := range roots {
		counts[node.Slug]++
	}
	for _, slug := range []string{"personal", "projects", "technical", "agents", "process"} {
		require.Equal(t, 1, counts[slug], "expected exactly one %s root", slug)
	}
}

func TestTaxonomySeedOrgIsolation(t *testing.T) {
	db := setupEmbeddingWorkerTestDatabase(t)

	orgA := createTaxonomySeedTestOrganization(t, db, "taxonomy-seed-org-a")
	orgB := createTaxonomySeedTestOrganization(t, db, "taxonomy-seed-org-b")
	taxonomyStore := store.NewEllieTaxonomyStore(db)

	require.NoError(t, SeedDefaultEllieTaxonomy(context.Background(), taxonomyStore, orgA))

	rootsA, err := taxonomyStore.ListNodesByParent(context.Background(), orgA, nil, 100)
	require.NoError(t, err)
	require.Len(t, rootsA, 5)

	rootsB, err := taxonomyStore.ListNodesByParent(context.Background(), orgB, nil, 100)
	require.NoError(t, err)
	require.Len(t, rootsB, 0)
}

func createTaxonomySeedTestOrganization(t *testing.T, db *sql.DB, slug string) string {
	t.Helper()

	var orgID string
	err := db.QueryRow(
		`INSERT INTO organizations (name, slug, tier)
		 VALUES ($1, $2, 'free')
		 RETURNING id`,
		"Org "+slug,
		slug,
	).Scan(&orgID)
	require.NoError(t, err)
	return orgID
}
