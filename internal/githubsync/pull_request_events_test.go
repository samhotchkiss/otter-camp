package githubsync

import (
	"testing"
	"time"
)

func TestMapPullRequestWebhookTransition(t *testing.T) {
	now := time.Date(2026, 2, 6, 14, 30, 0, 0, time.UTC)

	tests := []struct {
		name       string
		action     string
		merged     bool
		wantState  string
		wantDraft  *bool
		wantMerged *bool
		wantClosed bool
		wantErr    bool
	}{
		{name: "opened", action: "opened", merged: false, wantState: "open", wantMerged: boolRef(false)},
		{name: "synchronize", action: "synchronize", merged: false, wantState: "open", wantMerged: boolRef(false)},
		{name: "ready for review", action: "ready_for_review", merged: false, wantState: "open", wantDraft: boolRef(false)},
		{name: "converted to draft", action: "converted_to_draft", merged: false, wantState: "open", wantDraft: boolRef(true)},
		{name: "closed unmerged", action: "closed", merged: false, wantState: "closed", wantMerged: boolRef(false), wantClosed: true},
		{name: "closed merged", action: "closed", merged: true, wantState: "closed", wantMerged: boolRef(true), wantClosed: true},
		{name: "unsupported action", action: "assigned", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			transition, err := MapPullRequestWebhookTransition(tc.action, tc.merged, now)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if transition.State != tc.wantState {
				t.Fatalf("expected state=%q, got %q", tc.wantState, transition.State)
			}
			if (tc.wantDraft == nil) != (transition.Draft == nil) {
				t.Fatalf("unexpected draft pointer state")
			}
			if tc.wantDraft != nil && *transition.Draft != *tc.wantDraft {
				t.Fatalf("expected draft=%v, got %v", *tc.wantDraft, *transition.Draft)
			}
			if (tc.wantMerged == nil) != (transition.Merged == nil) {
				t.Fatalf("unexpected merged pointer state")
			}
			if tc.wantMerged != nil && *transition.Merged != *tc.wantMerged {
				t.Fatalf("expected merged=%v, got %v", *tc.wantMerged, *transition.Merged)
			}
			if tc.wantClosed {
				if transition.ClosedAt == nil || !transition.ClosedAt.Equal(now) {
					t.Fatalf("expected closed_at=%s, got %+v", now, transition.ClosedAt)
				}
			} else if transition.ClosedAt != nil {
				t.Fatalf("did not expect closed_at for action %q", tc.action)
			}
		})
	}
}
