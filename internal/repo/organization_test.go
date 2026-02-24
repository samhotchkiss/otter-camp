package repo

import (
	"reflect"
	"testing"
)

func TestReplaceOrganizationSettingsUsesFullReplace(t *testing.T) {
	current := OrganizationSettings{
		Agents: OrganizationAgentsSettings{MaxConcurrentTemps: 12},
		Redaction: OrganizationRedactionSettings{
			Enabled: true,
			Policy:  "strict",
		},
	}

	next := OrganizationSettings{
		Redaction: OrganizationRedactionSettings{
			Enabled: false,
			Policy:  "off",
		},
	}

	replaced := replaceOrganizationSettings(current, next)
	if !reflect.DeepEqual(replaced, next) {
		t.Fatalf("replaceOrganizationSettings result = %+v, want %+v", replaced, next)
	}
}
