package chunker

import (
	"reflect"
	"strings"
	"testing"
)

// TestWords: an identifier splits where its case changes, acronyms (with
// their plural s) and digits staying with their word; one word gives none.
func TestWords(t *testing.T) {
	for id, want := range map[string]string{
		"getUserToken":   "get user token",
		"GetUserToken":   "get user token",
		"HTTPServer":     "http server",
		"XMLHttpRequest": "xml http request",
		"userID":         "user id",
		"IOError":        "io error",
		"parseURLs":      "parse urls",
		"userIDs":        "user ids",
		"getAPIsFor":     "get apis for",
		"HTTPSession":    "http session",
		"XMLs":           "",
		"base64Encode":   "base64 encode",
		"toInt64":        "to int64",
		"ÜberMaß":        "über maß",
		"token":          "",
		"ID":             "",
		"md5sum":         "",
		"x":              "",
	} {
		if got := strings.Join(words(id), " "); got != want {
			t.Errorf("%s: %q, want %q", id, got, want)
		}
	}
}

// TestTerms: the words of each distinct compound identifier in a chunk's path
// and content, snake_case parts too; one-word identifiers, words of one or two
// letters and runs too long to be names add nothing.
func TestTerms(t *testing.T) {
	got := Terms("web/src/pages/FileView.tsx", "const fileView = getUserToken(id); getUserToken(token_store, get_userToken)")
	if want := "file view file view get user token user token"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := Terms("main.go", "package main\n\nfunc main() {}"); got != "" {
		t.Fatalf("one-word identifiers gave %q", got)
	}
	blob := strings.Repeat("aB3", 30) // base64, say: 90 bytes
	if got := Terms("ids.go", `var userID, xPos = "`+blob+`"`); got != "user pos" {
		t.Fatalf("short words and a long run gave %q, want %q", got, "user pos")
	}
	if got := Split("refreshUserToken or userToken?"); !reflect.DeepEqual(got, []string{"refresh", "user", "token", "user", "token"}) {
		t.Fatalf("split %v", got)
	}
}
