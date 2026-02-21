package memory

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEllieDedupLLMDecisionValidationAcceptsValidDecision(t *testing.T) {
	raw := `{"keep":"b","deprecate":["a"],"merge":null}`
	decision, err := ParseAndValidateEllieDedupDecision([]string{"a", "b", "c"}, raw)
	require.NoError(t, err)
	require.Equal(t, "b", decision.Keep)
	require.Equal(t, []string{"a"}, decision.Deprecate)
	require.Nil(t, decision.Merge)
}

func TestEllieDedupLLMDecisionValidationRejectsInvalidDecision(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "keep outside cluster",
			raw:  `{"keep":"z","deprecate":["a"]}`,
		},
		{
			name: "deprecate includes keep",
			raw:  `{"keep":"b","deprecate":["b"]}`,
		},
		{
			name: "empty decision without merge",
			raw:  `{"keep":"","deprecate":[]}`,
		},
		{
			name: "merge missing content",
			raw:  `{"keep":"","deprecate":["a","b"],"merge":{"title":"Merged","content":""}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAndValidateEllieDedupDecision([]string{"a", "b", "c"}, tc.raw)
			require.Error(t, err)
		})
	}
}
