package version

import "testing"

func TestDefaultsPresent(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
	if Commit == "" {
		t.Fatal("Commit must not be empty")
	}
	if BuiltAt == "" {
		t.Fatal("BuiltAt must not be empty")
	}
	if GoVersion == "" {
		t.Fatal("GoVersion must not be empty")
	}
}
