package scipimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"deepcode/internal/repo/git"
)

func TestSCIPContainerCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX shell")
	}
	dir := t.TempDir()
	docker := filepath.Join(dir, "docker")
	t.Setenv("TEST_DOCKER_ARGS", filepath.Join(dir, "args"))
	if err := os.WriteFile(docker, []byte(`#!/bin/sh
if [ "$1" = run ]; then
  printf '%s\n' "$@" > "$TEST_DOCKER_ARGS"
  exec sleep 60
fi
printf '%s\n' "$@" >> "$TEST_DOCKER_ARGS"
`), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := runSCIP(ctx, docker, "local-test-image", dir, "", "commit", "test-scip"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{
		"\n--pull=never\n", "\n--network=none\n", "\n--read-only\n", "\n--user\n65534:65534\n", "\n--cap-drop=ALL\n",
		"\n--memory=1g\n", "\n--pids-limit=128\n", "\nGOPROXY=off\n", ",target=/src,readonly\n", "\nrm\n-f\ntest-scip\n",
	} {
		if !strings.Contains(string(raw), flag) {
			t.Errorf("missing Docker argument sequence %q", flag)
		}
	}
}

func TestBytePosition(t *testing.T) {
	lines := []string{"a𝒳éz", ""}
	for _, tc := range []struct {
		name            string
		line, col, want int32
		ok              bool
	}{
		{"start", 0, 0, 0, true}, {"ascii", 0, 1, 1, true}, {"surrogate interior", 0, 2, 0, false},
		{"astral end", 0, 3, 5, true}, {"bmp end", 0, 4, 7, true}, {"line end", 0, 5, 8, true},
		{"past end", 0, 6, 0, false}, {"negative column", 0, -1, 0, false}, {"negative line", -1, 0, 0, false},
		{"missing line", 2, 0, 0, false}, {"empty", 1, 0, 0, true}, {"empty overflow", 1, 1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := bytePosition(lines, scip.Position{Line: tc.line, Character: tc.col})
			if ok != tc.ok || ok && (got.Character != tc.want || got.Line != tc.line) {
				t.Fatalf("got %v,%v; want column %d,%v", got, ok, tc.want, tc.ok)
			}
		})
	}
	for _, col := range []int32{2, 3, 4, 6} {
		if validPosition(lines, scip.Position{Character: col}) {
			t.Errorf("UTF-8 interior %d accepted", col)
		}
	}
}

func TestSCIPAnchorsAndIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		name, symbol          string
		spec                  int
		line, col, end, roles int32
		ok                    bool
	}{
		{"TS module", "scip-typescript npm p v `alpha.ts`/", 1, 0, 0, 0, 1, true},
		{"Python module", "scip-python python p v alpha/__init__:", 2, 0, 0, 0, 1, true},
		{"Go namespace", "scip-go gomod p v alpha/", 0, 0, 0, 0, 1, false},
		{"ordinary empty", "scip-typescript npm p v alpha/resolve().", 1, 0, 0, 0, 1, false},
		{"namespace reference", "scip-typescript npm p v alpha/", 1, 0, 0, 0, 0, false},
		{"wrong location", "scip-typescript npm p v alpha/", 1, 1, 0, 0, 1, false},
		{"wrong column", "scip-typescript npm p v alpha/", 1, 0, 1, 1, 1, false},
		{"Python other meta", "scip-python python p v alpha/other:", 2, 0, 0, 0, 1, false},
		{"Python ordinary namespace", "scip-python python p v alpha/", 2, 0, 0, 0, 1, false},
		{"inverted", "scip-typescript npm p v alpha/", 1, 0, 1, 0, 1, false},
		{"malformed", "broken", 1, 0, 0, 0, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &scip.Occurrence{Symbol: tc.symbol, SymbolRoles: tc.roles, Range: []int32{tc.line, tc.col, tc.end}}
			r, _ := o.SourceRange()
			if got := moduleAnchor(o, r, specs[tc.spec]); got != tc.ok {
				t.Fatalf("anchor %v; want %v", got, tc.ok)
			}
			_, err := appendDocument(nil, "fixture", &scip.Document{Occurrences: []*scip.Occurrence{o}}, []string{"valid", "line"}, map[string]bool{}, specs[tc.spec])
			if !tc.ok && tc.col == tc.end && tc.roles == 1 && err == nil {
				t.Fatal("non-anchor empty range accepted")
			}
		})
	}
	for _, tc := range []struct {
		s            string
		goOK, js, py bool
	}{
		{"resolve", true, true, true}, {"$resolve", false, true, false}, {"解決", true, true, true},
		{"x́", false, true, true}, {"1x", false, false, false}, {"a-b", false, false, false}, {"", false, false, false},
		{"class", true, false, false}, {"func", false, true, true},
	} {
		for i, want := range []bool{tc.goOK, tc.js, tc.py} {
			if got := specs[i].identifier(tc.s); got != want {
				t.Errorf("%s identifier %q=%v; want %v", specs[i].name, tc.s, got, want)
			}
		}
	}
}

func TestSCIPProjectRoots(t *testing.T) {
	var tree []git.TreeEntry
	for _, name := range []string{"tsconfig.json", "jsconfig.json", "web/jsconfig.json", "web/nested/tsconfig.json", "node_modules/x/tsconfig.json", "vendor/tsconfig.json", ".hidden/tsconfig.json", "x/_private/tsconfig.json", "pkg/go.mod", "vendor/go.mod", "a.py"} {
		tree = append(tree, git.TreeEntry{Path: name})
	}
	if got, want := nodeProjects(tree), ([]project{{root: ""}, {root: "web"}, {root: "web/nested"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v; want %v", got, want)
	}
	if got := pythonProjects(tree); !reflect.DeepEqual(got, []project{{}}) {
		t.Fatal(got)
	}
	if got := moduleRoots(tree); !reflect.DeepEqual(got, []string{"pkg"}) {
		t.Fatal(got)
	}
	if got := nodeProjects([]git.TreeEntry{{Path: "src/a.mjs"}}); !reflect.DeepEqual(got, []project{{infer: true}}) {
		t.Fatal(got)
	}
	if len(nodeProjects(nil)) != 0 || len(pythonProjects(nil)) != 0 || len(nodeProjects([]git.TreeEntry{{Path: "vendor/a.ts"}})) != 0 {
		t.Fatal("empty or excluded projects")
	}
}

func TestSCIPRejectsUnknownMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, tool, label, ext string
		spec                   int
		encoding               scip.PositionEncoding
		message                string
	}{
		{"unknown tool", "other", "TypeScript", ".ts", 1, scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, "requires UTF-8 indexes"},
		{"unknown label", "scip-typescript", "typescript", ".ts", 1, scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, "unsupported"},
		{"wrong label for extension", "scip-typescript", "JavaScript", ".ts", 1, scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, "unsupported"},
		{"unknown extension", "scip-typescript", "TypeScript", ".vue", 1, scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, "document path"},
		{"unspecified TS offsets", "scip-typescript", "TypeScript", ".ts", 1, scip.PositionEncoding_UnspecifiedPositionEncoding, "unsupported"},
		{"UTF-8 TS offsets", "scip-typescript", "TypeScript", ".ts", 1, scip.PositionEncoding_UTF8CodeUnitOffsetFromLineStart, "unsupported"},
		{"Python label not default", "scip-python", "python", ".py", 2, scip.PositionEncoding_UnspecifiedPositionEncoding, "unsupported"},
		{"Python encoding not default", "scip-python", "", ".py", 2, scip.PositionEncoding_UTF16CodeUnitOffsetFromLineStart, "unsupported"},
		{"Go still strict", "scip-go", "go", ".go", 0, scip.PositionEncoding_UnspecifiedPositionEncoding, "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "source" + tc.ext
			idx := &scip.Index{Metadata: &scip.Metadata{ToolInfo: &scip.ToolInfo{Name: tc.tool}, TextDocumentEncoding: scip.TextEncoding_UTF8}, Documents: []*scip.Document{{RelativePath: name, Language: tc.label, PositionEncoding: tc.encoding}}}
			raw, err := proto.Marshal(idx)
			if err != nil {
				t.Fatal(err)
			}
			_, err = readLanguageIndex(t.Context(), nil, 1, "", map[string]string{name: strings.Repeat("a", 40)}, raw, nil, specs[tc.spec], nil)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("got %v; want %q", err, tc.message)
			}
		})
	}
	if _, err := readLanguageIndex(t.Context(), nil, 1, "", nil, []byte{255}, nil, specs[1], nil); err == nil {
		t.Fatal("malformed index accepted")
	}
}

func TestSCIPUTF16DocumentRanges(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start, end int32
		definition bool
		want       int
		valid      bool
	}{
		{"reference after astral", 3, 10, false, 5, true}, {"definition after astral", 3, 10, true, 5, true},
		{"surrogate start", 2, 10, false, 0, false}, {"surrogate end", 1, 2, false, 0, false},
		{"past line", 3, 11, false, 0, false}, {"empty ordinary", 3, 3, false, 0, false}, {"inverted", 10, 3, false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &scip.Occurrence{Symbol: "scip-typescript npm p v source/resolve().", Range: []int32{0, tc.start, tc.end}}
			if tc.definition {
				o.SymbolRoles = 1
			}
			rows, err := appendDocument(nil, "source.ts", &scip.Document{Occurrences: []*scip.Occurrence{o}}, []string{"a𝒳resolve"}, map[string]bool{}, specs[1])
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if tc.valid && (len(rows) != 1 || rows[0].ByteColumn != tc.want || tc.definition && rows[0].DefinitionName != "resolve") {
				t.Fatal(rows)
			}
		})
	}
}
