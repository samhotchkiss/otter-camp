package main

import (
	"context"
	"testing"
	"time"
)

type fakeSeedStore struct {
	countResult int
	countErr    error

	createOrgID  string
	createOrgErr error

	createUserID  string
	createUserErr error

	createSessionErr error

	createOrgCalls     int
	createUserCalls    int
	createSessionCalls int

	lastSessionToken   string
	lastSessionExpires time.Time
}

func (f *fakeSeedStore) CountOrganizations(context.Context) (int, error) {
	return f.countResult, f.countErr
}

func (f *fakeSeedStore) CreateOrganization(context.Context, string, string) (string, error) {
	f.createOrgCalls++
	if f.createOrgID == "" {
		f.createOrgID = "org-1"
	}
	return f.createOrgID, f.createOrgErr
}

func (f *fakeSeedStore) CreateUser(context.Context, string, string, string, string, string) (string, error) {
	f.createUserCalls++
	if f.createUserID == "" {
		f.createUserID = "user-1"
	}
	return f.createUserID, f.createUserErr
}

func (f *fakeSeedStore) CreateSession(_ context.Context, _ string, _ string, token string, expiresAt time.Time) error {
	f.createSessionCalls++
	f.lastSessionToken = token
	f.lastSessionExpires = expiresAt
	return f.createSessionErr
}

func TestSeedDefaultWorkspaceSkipsWhenOrgAlreadyExists(t *testing.T) {
	store := &fakeSeedStore{countResult: 1}

	result, err := seedDefaultWorkspace(context.Background(), store, time.Date(2026, 2, 9, 0, 0, 0, 0, time.UTC), "oc_local_token")
	if err != nil {
		t.Fatalf("seedDefaultWorkspace() error = %v", err)
	}
	if result.Created {
		t.Fatalf("expected Created=false when orgs already exist")
	}
	if store.createOrgCalls != 0 || store.createUserCalls != 0 || store.createSessionCalls != 0 {
		t.Fatalf("expected no create calls when seed is skipped")
	}
}

func TestSeedDefaultWorkspaceCreatesOrgUserSession(t *testing.T) {
	now := time.Date(2026, 2, 9, 12, 0, 0, 0, time.UTC)
	store := &fakeSeedStore{countResult: 0}

	result, err := seedDefaultWorkspace(context.Background(), store, now, "oc_local_fixed_token")
	if err != nil {
		t.Fatalf("seedDefaultWorkspace() error = %v", err)
	}
	if !result.Created {
		t.Fatalf("expected Created=true")
	}
	if store.createOrgCalls != 1 || store.createUserCalls != 1 || store.createSessionCalls != 1 {
		t.Fatalf("expected one call for each create step")
	}
	if store.lastSessionToken != "oc_local_fixed_token" {
		t.Fatalf("expected session token to match provided token")
	}
	expectedExpiry := now.Add(365 * 24 * time.Hour)
	if !store.lastSessionExpires.Equal(expectedExpiry) {
		t.Fatalf("expected expiry %s, got %s", expectedExpiry, store.lastSessionExpires)
	}
}

func TestSeedDefaultWorkspaceGeneratesTokenWhenMissing(t *testing.T) {
	store := &fakeSeedStore{countResult: 0}

	result, err := seedDefaultWorkspace(context.Background(), store, time.Date(2026, 2, 9, 12, 0, 0, 0, time.UTC), "")
	if err != nil {
		t.Fatalf("seedDefaultWorkspace() error = %v", err)
	}
	if result.Token == "" {
		t.Fatalf("expected token to be generated")
	}
	if len(result.Token) <= len("oc_local_") {
		t.Fatalf("expected generated token to include random suffix")
	}
	if result.Token[:len("oc_local_")] != "oc_local_" {
		t.Fatalf("expected generated token prefix oc_local_, got %s", result.Token)
	}
}
