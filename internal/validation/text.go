package validation

import "regexp"

var htmlTagPattern = regexp.MustCompile(`(?i)<\s*/?\s*[a-z][^>]*>`)

func HasHTMLTag(value string) bool {
	return htmlTagPattern.MatchString(value)
}
