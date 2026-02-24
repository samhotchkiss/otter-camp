package repo

import "testing"

func TestHashesConflict(t *testing.T) {
	if hashesConflict("abc", "abc") {
		t.Fatal("same request hash should not conflict")
	}
	if !hashesConflict("abc", "xyz") {
		t.Fatal("different request hash should conflict")
	}
}
