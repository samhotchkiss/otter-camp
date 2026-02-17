package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrgStoreGetBySlug(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "org-store-by-slug")
	store := NewOrgStore(db)

	org, err := store.GetBySlug(context.Background(), "org-store-by-slug")
	require.NoError(t, err)
	require.NotNil(t, org)
	assert.Equal(t, orgID, org.ID)
	assert.Equal(t, "org-store-by-slug", org.Slug)
}

func TestOrgStoreGetBySlugNotFound(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewOrgStore(db)

	org, err := store.GetBySlug(context.Background(), "missing-org")
	assert.Error(t, err)
	assert.Nil(t, org)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestOrgStoreGetBySlugNormalizesInput(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	orgID := createTestOrganization(t, db, "mixed-case-org")
	store := NewOrgStore(db)

	org, err := store.GetBySlug(context.Background(), "  MIXED-CASE-ORG ")
	require.NoError(t, err)
	require.NotNil(t, org)
	assert.Equal(t, orgID, org.ID)
	assert.Equal(t, "mixed-case-org", org.Slug)
}

func TestOrgStoreGetBySlugRejectsEmptySlug(t *testing.T) {
	connStr := getTestDatabaseURL(t)
	db := setupTestDatabase(t, connStr)

	store := NewOrgStore(db)

	org, err := store.GetBySlug(context.Background(), "   ")
	assert.Error(t, err)
	assert.Nil(t, org)
	assert.ErrorIs(t, err, ErrValidation)
}
