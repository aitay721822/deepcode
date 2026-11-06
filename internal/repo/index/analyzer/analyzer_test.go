package analyzer

import (
	"fmt"
	"strings"
	"testing"
)

// flat renders symbols as "Name kind start-end" with children indented.
func flat(syms []Symbol, indent string) []string {
	var out []string
	for _, s := range syms {
		name := s.Name
		if s.Container != "" {
			name = s.Container + "." + name
		}
		out = append(out, fmt.Sprintf("%s%s %s %d-%d", indent, name, s.Kind, s.StartLine, s.EndLine))
		out = append(out, flat(s.Children, indent+"  ")...)
	}
	return out
}

func TestSymbols(t *testing.T) {
	cases := []struct {
		lang, src string
		want      []string
	}{
		{"go", `package x

// Store keeps things.
type Store struct{}

type (
	ID   int
	Repo interface{ Get(ID) }
)

// Get returns one.
func (s *Store) Get(id ID) {}

func New[T any]() *Store { return nil }
`, []string{"Store struct 3-4", "ID type 7-7", "Repo interface 8-8", "Store.Get method 11-12", "New function 14-14"}},
		{"typescript", `import x from "y";

/** The app. */
export abstract class App<T> extends Base {
  private readonly name = "a";
  onClick = () => {};
  constructor() { super(); }
  async start(): Promise<void> {
    await run();
  }
}

export interface Options { a: string }
export type Mode = "a" | "b";
enum Color { Red }
export const helper = (n: number) => n * 2;
const a = 1, b = function () {};
namespace Util {
  export function pad() {}
}
declare function external(): void;
export const createStore = ((init) => init) as CreateStore;
const fmt = ((n: number) => String(n)) satisfies Fmt;
`, []string{
			"App class 3-11", "  onClick method 6-6", "  constructor method 7-7", "  start method 8-10",
			"Options interface 13-13", "Mode type 14-14", "Color enum 15-15", "helper function 16-16",
			"b function 17-17", "Util namespace 18-20", "  pad function 19-19", "external function 21-21",
			"createStore function 22-22", "fmt function 23-23",
		}},
		{"tsx", `export function Card({ title }: Props) {
  return <div>{title}</div>;
}
`, []string{"Card function 1-3"}},
		{"javascript", `export default function () {}
function* gen() {}
class A { m() {} }
module.exports = {};
`, []string{"default function 1-1", "gen function 2-2", "A class 3-3", "  m method 3-3"}},
		{"markdown", "# Title\n", nil},
	}
	p := NewParser()
	defer p.Close()
	for _, c := range cases {
		got := flat(p.Symbols(c.lang, []byte(c.src)), "")
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", c.lang, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
}

// TestSignature: a symbol's signature is the first line of its declaration.
func TestSignature(t *testing.T) {
	p := NewParser()
	defer p.Close()
	syms := p.Symbols("go", []byte("package x\n\nfunc Run(a int) error {\n\treturn nil\n}\n"))
	if len(syms) != 1 || syms[0].Signature != "func Run(a int) error {" {
		t.Fatalf("%+v", syms)
	}
}

func TestDetect(t *testing.T) {
	for path, want := range map[string]string{
		"src/a.ts": "typescript", "src/b.tsx": "tsx", "x.mjs": "javascript", "cmd/main.go": "go",
		"README.md": "markdown", "Dockerfile": "dockerfile", "docs/guide.mdx": "markdown",
		"node_modules/x/index.js": "", "vendor/a/b.go": "", "dist/app.js": "", "a.min.js": "",
		"package-lock.json": "", "go.sum": "", "logo.png": "",
	} {
		got, ok := Detect(path)
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q %v, want %q", path, got, ok, want)
		}
	}
}
