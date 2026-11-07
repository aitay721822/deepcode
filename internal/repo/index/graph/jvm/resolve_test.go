package jvm

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const (
	model = "app/src/main/java/com/acme/model/"
	util  = "app/src/main/java/com/acme/util/"
	web   = "app/src/main/java/com/acme/web/"
)

func TestResolve(t *testing.T) {
	files := map[string][]string{
		model + "User.java":   {"package:com.acme.model", "def:com.acme.model.User"},
		model + "Status.java": {"package:com.acme.model", "def:com.acme.model.Status"},
		model + "Outer.java":  {"package:com.acme.model", "def:com.acme.model.Outer"},
		util + "Strings.java": {"package:com.acme.util", "def:com.acme.util.Strings"},
		util + "Dates.java":   {"package:com.acme.util", "def:com.acme.util.Dates"},
		util + "Helper.java":  {"package:com.acme.util", "def:com.acme.util.Helper"},
		"lib/Log.java":        {"package:com.acme.audit", "def:com.acme.audit.Log"},
		web + "User.java":     {"package:com.acme.web", "def:com.acme.web.User"},
		// Its path does not match its package: same-package use still finds it.
		"misc/Helper.java": {"package:com.acme.web", "def:com.acme.web.Helper", "type:Helper"},
		web + "UserController.java": {
			"package:com.acme.web",
			"import:com.acme.model.User",                     // explicit import, shadowing web's User
			"import:com.acme.model.Outer.Inner",              // a nested class: Outer's file
			"import:com.acme.audit.Log",                      // import static com.acme.audit.Log.info
			"import:org.springframework.http.ResponseEntity", // outside the repository: no edge
			"star:com.acme.util",
			"star:java.util",
			"def:com.acme.web.UserController",
			"type:User", "type:Inner", "type:ResponseEntity", "type:UserController",
			"type:Strings",               // through the star import
			"type:Helper",                // the own package before the star import
			"type:Map.Entry",             // nowhere
			"type:com.acme.model.Status", // spelled out
		},
		"Main.java": {"def:Main", "type:Main"}, // the default package, using only itself
	}
	want := []string{
		web + "UserController.java -> " + model + "User.java",
		web + "UserController.java -> " + model + "Outer.java",
		web + "UserController.java -> lib/Log.java",
		web + "UserController.java -> " + util + "Strings.java",
		web + "UserController.java -> misc/Helper.java",
		web + "UserController.java -> " + model + "Status.java",
	}
	if got := render(Resolve(files, nil)); !slices.Equal(got, sorted(want)) {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(sorted(want), "\n"))
	}
}

// TestDefaultPackage: files without a package use each other by name.
func TestDefaultPackage(t *testing.T) {
	files := map[string][]string{
		"A.java": {"def:A", "type:B"},
		"B.java": {"def:B"},
	}
	if got := render(Resolve(files, nil)); !slices.Equal(got, []string{"A.java -> B.java"}) {
		t.Errorf("got %v", got)
	}
}

// TestCap: a file naming more types than maxEdges uses the first maxEdges files.
func TestCap(t *testing.T) {
	files := map[string][]string{}
	user := []string{"package:p"}
	for i := range maxEdges + 10 {
		files[fmt.Sprintf("p/C%d.java", i)] = []string{"package:p", fmt.Sprintf("def:p.C%d", i)}
		user = append(user, fmt.Sprintf("type:C%d", i))
	}
	files["p/User.java"] = user
	got := Resolve(files, nil)
	if len(got) != maxEdges || got[0] != [2]string{"p/User.java", "p/C0.java"} {
		t.Errorf("got %d edges: %v", len(got), got)
	}
}

func render(edges [][2]string) []string {
	var out []string
	for _, e := range edges {
		out = append(out, e[0]+" -> "+e[1])
	}
	return sorted(out)
}

func sorted(s []string) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	return slices.Compact(s)
}
