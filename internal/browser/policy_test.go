package browser

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

type policyReaderStub struct {
	policy BrowserPolicy
	err    error
}

func (s policyReaderStub) GetBrowserPolicy(context.Context, uuid.UUID) (BrowserPolicy, error) {
	return s.policy, s.err
}

func TestDomainPolicyCheckerIsAllowed_Defaults(t *testing.T) {
	checker := NewDomainPolicyChecker(policyReaderStub{})
	orgID := uuid.New()

	if checker.IsAllowed(context.Background(), orgID, "https://accounts.google.com") {
		t.Fatal("accounts.google.com should be denied")
	}
	if !checker.IsAllowed(context.Background(), orgID, "https://en.wikipedia.org/wiki/Go_(programming_language)") {
		t.Fatal("en.wikipedia.org should be allowed")
	}
}

func TestDomainPolicyCheckerIsAllowed_OrgDenylist(t *testing.T) {
	checker := NewDomainPolicyChecker(policyReaderStub{policy: BrowserPolicy{Denylist: []string{"example.org"}}})
	if checker.IsAllowed(context.Background(), uuid.New(), "https://docs.example.org/path") {
		t.Fatal("org denylist domain should be denied")
	}
}
