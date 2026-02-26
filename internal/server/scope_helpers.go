package server

import "strings"

func requireReadScope(resource string) []string {
	resource = strings.ToLower(strings.TrimSpace(resource))
	if resource == "" {
		return []string{"admin:*"}
	}
	return []string{
		"read:" + resource,
		resource + ":read",
		"write:" + resource,
		resource + ":write",
		"admin:*",
		"admin:" + resource,
		resource + ":admin",
	}
}

func requireWriteScope(resource string) []string {
	resource = strings.ToLower(strings.TrimSpace(resource))
	if resource == "" {
		return []string{"admin:*"}
	}
	return []string{
		"write:" + resource,
		resource + ":write",
		"admin:*",
		"admin:" + resource,
		resource + ":admin",
	}
}

func requireAdminScope(resource string) []string {
	resource = strings.ToLower(strings.TrimSpace(resource))
	if resource == "" {
		return []string{"admin:*"}
	}
	return []string{
		"admin:*",
		"admin:" + resource,
		resource + ":admin",
	}
}
