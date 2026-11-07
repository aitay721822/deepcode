package cuid

import (
	"regexp"
	"testing"
)

// TestNew: a letter and 23 base-36 digits, never the same twice.
func TestNew(t *testing.T) {
	shape := regexp.MustCompile(`^[a-z][0-9a-z]{23}$`)
	seen := map[string]bool{}
	for range 10000 {
		id := New()
		if !shape.MatchString(id) || seen[id] {
			t.Fatalf("id %q: malformed or repeated", id)
		}
		seen[id] = true
	}
}
