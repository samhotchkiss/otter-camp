package validation

import "testing"

func TestHasHTMLTag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "plain text", value: "Builder Agent", want: false},
		{name: "angle bracket non-tag", value: "2 < 3", want: false},
		{name: "script tag", value: "<script>alert(1)</script>", want: true},
		{name: "bold tag", value: "<b>name</b>", want: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := HasHTMLTag(tc.value); got != tc.want {
				t.Fatalf("HasHTMLTag(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
