package jobs

import "testing"

func TestRetentionConstantsModelInvocationDays(t *testing.T) {
	if RetentionModelInvocationDays != 90 {
		t.Fatalf("RetentionModelInvocationDays = %d, want 90", RetentionModelInvocationDays)
	}
}
