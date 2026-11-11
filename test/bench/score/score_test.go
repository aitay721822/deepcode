package score

import (
	"slices"
	"strings"
	"testing"
)

// TestCover: a topic is covered when a heading holds one of its words as a
// whole word, a name's words counting and a hyphen as a space, and not inside
// a longer word; headings in code blocks do not count.
func TestCover(t *testing.T) {
	topics := []Topic{{"Mux", []string{"mux"}}, {"CI", []string{"ci"}}, {"Context", []string{"route context"}}, {"app.param", []string{"app.param"}}}
	n, missing := Cover(topics, []string{"The chi.Mux router", "Circuit breakers", "Route Context and URLParam", "Using app.params"})
	if n != 2 || strings.Join(missing, ",") != "CI,app.param" {
		t.Errorf("covered %d, missing %q; want Mux and Context covered, not CI (in Circuit) or app.param (in app.params)", n, missing)
	}
	for _, h := range []struct {
		heading, word string
		want          bool
	}{
		{"WelcomeController", "welcome", true}, {"Unwelcome", "welcome", false}, {"HTTPServer", "server", true}, {"HTTPServer", "http", true},
		{"CIPipeline", "ci", true}, {"CIRCUIT", "ci", false}, {"OAuth2Client", "client", true}, {"OAuth2 Login", "oauth", true},
		{"Base64 Encoding", "base", true}, {"Base64encode", "encode", false}, {"REST APIs", "api", true}, {"CIs", "ci", true},
		{"APIsController", "api", true}, {"APIsimple", "api", false}, {"Apis", "api", true}, {"İ server", "server", true},
		{"使用server", "server", true}, {"GracefulShutdown", "graceful", true}, {"Ungraceful", "graceful", false},
		{"Auto Generating Selectors", "auto-generating", true}, {"Mobile-BFF", "mobile bff", true}, {"Auto-generator", "auto generating", false},
		// Inflections of a word of letters alone, not of a name.
		{"Versioned APIs", "versioning", true}, {"API Versions", "versioning", true}, {"Versionless", "version", false}, {"Routing", "route", true},
		{"Router", "route", false}, {"Caches", "cache", true}, {"Classes", "class", true}, {"Statuses", "status", true}, {"Strategies", "strategy", true},
		{"Mapping", "map", true}, {"Mapped Types", "mapping", true}, {"Passed", "pass", true}, {"Auto Generated Selectors", "auto-generating", true},
		{"Settings", "setting", true}, {"Mappings", "map", true}, {"Embedding", "embed", true}, {"Filing", "files", false}, {"Strings", "string", true},
		{"RouteDefinition", "route", true}, {"CacheDefaults", "cache", true}, {"RouteSet", "route", true}, {"RoutedHandler", "route", true},
		{"State", "stat", false}, {"Authy", "auth", false}, {"State", "state", true}, {"Strategy", "strategies", true},
		{"State Management", "stats", false}, {"State", "states", true}, {"Plays", "play", true}, {"Playing", "play", true}, {"Keys", "key", true},
	} {
		if got := holds(h.heading, h.word); got != h.want {
			t.Errorf("holds(%q, %q) = %v", h.heading, h.word, got)
		}
	}
	page := "## One\n```sh\n## a comment\n```\n### Two\n#### Three\n  ~~~~\n## in tildes\n```\n## still in\n~~~~\n## Four\r\n" +
		"````\n```\n## in four\n```go\n````\n    ```\n## Five\n```\n## in three\n```go\n## still in three\n```  \n## Six\n"
	if got := Headings(page); !slices.Equal(got, []string{"One", "Two", "Four", "Five", "Six"}) {
		t.Errorf("Headings = %q, want One, Two, Four, Five and Six, nothing of the code blocks", got)
	}
}
