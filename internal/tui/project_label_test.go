package tui

import "testing"

func TestResolveProjectLabelPrefersNameThenSlugBeforeFallback(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		displayName  string
		slug         string
		sessionTitle string
		want         string
	}{
		{
			name:         "display name wins",
			displayName:  "Alpha Project",
			slug:         "alpha-project",
			sessionTitle: "Project Alpha PM",
			want:         "Alpha Project",
		},
		{
			name:         "slug used when display name missing",
			slug:         "alpha-project",
			sessionTitle: "Project Alpha PM",
			want:         "alpha-project",
		},
		{
			name:         "synthetic id label ignored in favor of slug",
			displayName:  "Project 19b5a684",
			slug:         "alpha-project",
			sessionTitle: "Project Alpha PM",
			want:         "alpha-project",
		},
		{
			name:         "session title used after slug",
			sessionTitle: "Alpha PM Session",
			want:         "Alpha PM Session",
		},
		{
			name:        "generic fallback",
			displayName: "Project 19b5a684",
			want:        "Untitled project",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := ResolveProjectLabel(tc.displayName, tc.slug, tc.sessionTitle); got != tc.want {
				t.Fatalf("ResolveProjectLabel(%q, %q, %q) = %q, want %q", tc.displayName, tc.slug, tc.sessionTitle, got, tc.want)
			}
		})
	}
}
