package analyzer

import (
	"fmt"
	"slices"
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
res.send = function send(body) {
  return this;
};
app.get = (path) => path;
View.prototype.render = function render() {};
exports.query = function () {};
module.exports.json = function json() {};
exports.Route = class {};
res.status = 404;
f().y = function () {};
x[k] = function () {};
this.z = function () {};
req.get = req.header = function header(name) {};
exports = module.exports = createApplication;
exports.wrap = (function () {});
handler = function () {};
/**
 * Sends.
 */

exports.later = function () {};
// a note

exports.alone = function () {};
/** Two lines apart. */


exports.far = function () {};
/*** banner ***/

module.exports = function main() {};
`, []string{"default function 1-1", "gen function 2-2", "A class 3-3", "  m method 3-3", "res.send function 5-7", "app.get function 8-8",
			"View.render function 9-9", "exports.query function 10-10", "module.exports.json function 11-11", "exports.Route class 12-12",
			"req.get function 17-17", "req.header function 17-17", "exports.wrap function 19-19", "exports.later function 21-25",
			"exports.alone function 28-28", "exports.far function 32-32", "module.exports function 35-35"}},
		{"java", "package x;\n\n/**\n * Doc.\n */\n\nclass A {}\n/**/\n\nclass B {}\n", []string{"A class 3-7", "B class 10-10"}},
		{"markdown", "# Title\n", []string{"Title section 1-1"}}, // its sections, without a grammar
		{"text", "# Title\n", nil},
	}
	p := NewParser()
	defer p.Close()
	for _, c := range cases {
		got := flat(p.Parse(c.lang, []byte(c.src)).Symbols, "")
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", c.lang, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
}

// TestSignature: a symbol's signature is the first line of its declaration.
func TestSignature(t *testing.T) {
	p := NewParser()
	defer p.Close()
	syms := p.Parse("go", []byte("package x\n\nfunc Run(a int) error {\n\treturn nil\n}\n")).Symbols
	if len(syms) != 1 || syms[0].Signature != "func Run(a int) error {" {
		t.Fatalf("%+v", syms)
	}
	// Two on a line: the second's starts at it.
	syms = p.Parse("javascript", []byte("function a(){} function b(){}\n")).Symbols
	if len(syms) != 2 || syms[1].Signature != "function b(){}" {
		t.Fatalf("%+v", syms)
	}
}

func TestImports(t *testing.T) {
	cases := []struct{ lang, src, want string }{
		{"go", "package x\n\nimport \"fmt\"\n\nimport (\n\tstdpath \"path\"\n\t_ `example.com/m/raw`\n\t\"example.com/m/pkg\"\n\t\"fmt\"\n)\n\nfunc f() { _ = \"not/an/import\" }\n",
			"fmt path example.com/m/raw example.com/m/pkg"},
		{"typescript", `import a from "./a";
import type { B } from '../b';
import "./side-effect.css";
import x = require("./legacy");
export { c } from "./c";
export * from "./d";
const e = require("./e");
async function f() { return import("./f"); }
const g = "./not-an-import";
// import h from "./h";
require(dynamicName);
`, "./a ../b ./side-effect.css ./legacy ./c ./d ./e ./f"},
		{"tsx", "import { Card } from '@/components/card'\nexport const X = () => <Card />\n", "@/components/card"},
		{"javascript", "const { join } = require('node:path')\nmodule.exports = require(`./tpl`)\nconst s = `${require('./in-tpl')}`\n", "node:path ./in-tpl"},
		{"go", "package x\n\nimport \"a\\nb\"\n", ""}, // a line break would split the stored list
		{"markdown", "import x from './x'\n", ""},
		{"markdown", "# Guide\n```rust\n{{#rustdoc_include ../listings/a/src/main.rs:all}}\n```\n", "../listings/a/src/main.rs"}, // mdBook's includes
	}
	p := NewParser()
	defer p.Close()
	for _, c := range cases {
		if got := strings.Join(p.Parse(c.lang, []byte(c.src)).Imports, " "); got != c.want {
			t.Errorf("%s:\ngot  %q\nwant %q", c.lang, got, c.want)
		}
	}
	const maxImports, maxSpec = 200, 300                               // the syntax package's bounds
	many := "package x\n\nimport (\n" + strings.Repeat("\t\"a\"\n", 2) // a repeat counts once
	for i := range maxImports + 5 {
		many += fmt.Sprintf("\t\"m/p%d\"\n", i)
	}
	if got := p.Parse("go", []byte(many+")\n")).Imports; len(got) != maxImports || got[0] != "a" {
		t.Errorf("got %d imports, first %q", len(got), got[0])
	}
	long := "package x\n\nimport \"" + strings.Repeat("a", maxSpec+1) + "\"\n"
	if got := p.Parse("go", []byte(long)).Imports; len(got) != 0 {
		t.Errorf("an over-long import path was kept: %d", len(got))
	}
	if !HasGrammar("tsx") || HasGrammar("markdown") {
		t.Error("HasGrammar")
	}
	if !ReadsImports("markdown") || !ReadsImports("go") || ReadsImports("text") || !slices.Contains(Imported(), "markdown") || len(Imported()) != len(Grammars())+1 {
		t.Errorf("ReadsImports, Imported %q", Imported())
	}
}

func TestDetect(t *testing.T) {
	for path, want := range map[string]string{
		"src/a.ts": "typescript", "src/b.tsx": "tsx", "x.mjs": "javascript", "cmd/main.go": "go",
		"README.md": "markdown", "Dockerfile": "dockerfile", "docs/guide.mdx": "markdown",
		"node_modules/x/index.js": "", "vendor/a/b.go": "", "dist/app.js": "", "a.min.js": "",
		"package-lock.json": "", "go.sum": "", "logo.png": "", "src/test/jmeter/plan.jmx": "xml",
		// .NET Framework and Core, Java, Rust
		"Shop.Web/Shop.Web.csproj": "msbuild", "Legacy/packages.config": "xml", "Legacy/Web.config": "xml", "eShop.slnx": "xml",
		"Shop.sln": "sln", "Views/Home/Index.cshtml": "razor", "Default.aspx": "aspnet", "Global.asax": "aspnet",
		"Properties/Resources.resx": "xml", "global.json": "json", "App.xaml": "xml", "Program.vb": "vbnet",
		"src/main/resources/application.properties": "properties", "src/lib.rs": "rust", "Order.java": "java",
		"Shop.Web/bin/Debug/net8.0/appsettings.json": "", "Shop.Web/obj/project.assets.json": "",
		"packages/Newtonsoft.Json.13.0.3/lib/x.xml": "", "app/build/classes/A.java": "", "target/debug/x.rs": "",
		".gradle/8.0/x.json": "", "Shop.Web/bin/x64/Release/a.json": "", "Shop.Web/obj/Debug/x.cs": "",
		"Shop.Web/obj/Shop.Web.csproj.nuget.g.props": "", "target/classes/a.properties": "", "Legacy.Web/bin/Legacy.Web.dll.config": "",
		// the same names holding source
		"src/bin/cli.rs": "rust", "build/release.sh": "bash", "packages/zustand/src/index.ts": "typescript",
		"src/bin/debug/main.rs": "rust", "cmd/internal/obj/x86/asm6.go": "go", "internal/target/x.go": "go",
		"src/main/java/com/acme/target/Planner.java": "java", "build/resources/x.sh": "bash",
		"src/main/java/com/acme/build/generated/X.java": "java", "lib/src/org/x/target/classes/Y.java": "java",
	} {
		got, ok := Detect(path)
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q %v, want %q", path, got, ok, want)
		}
	}
}

func TestSecondary(t *testing.T) {
	for p, want := range map[string]bool{"web/src/a.test.ts": true, "x.spec.js": true, "test_x.py": true, "a_test.go": true,
		"docs/x.ts": true, "src/__tests__/a.ts": true, "src/contest.ts": false, "latest.go": false, "attest/x.go": false,
		"tests/Ordering.UnitTests/OrderTest.cs": true, "src/Basket.FunctionalTests/Api.cs": true, "src/Contest.cs": false,
		"src/test/java/org/x/OwnerControllerTests.java": true, "src/main/java/org/x/Owner.java": false, "OwnerIT.java": true,
		"benches/routing.rs": true, "axum/benches/x.rs": true, "src/routing.rs": false, "README.md": true,
		"e2e/login.setup.ts": true, "web/e2e/cart.ts": true, "src/e2e2/x.ts": false,
		"test-shrinker/src/main/java/com/example/A.java": true, "lib/test-utils/a.ts": true, "src/latest-x/a.go": false, "contest-x/a.go": false,
		".github/workflows/ci.yml": true, "web/.storybook/main.ts": true, ".eslintrc.js": false, "src/a.b/c.go": false, "a/.b": false} {
		if Secondary(p) != want {
			t.Errorf("Secondary(%q) = %v", p, !want)
		}
	}
}

// TestAside: what a secondary file is; prose outside docs/ is a guide unless
// it is a README, changelog or community file.
func TestAside(t *testing.T) {
	for p, want := range map[string]string{".github/ISSUE_TEMPLATE/bug.md": Tooling, "a_test.go": Tests, "examples/rest/main.go": Examples,
		"docs/README.md": Docs, "UserGuide.md": Docs, "Troubleshooting.md": Docs, "README.md": Tooling, "axum/CHANGELOG.md": Tooling,
		"CODE_OF_CONDUCT.md": Tooling, "Release-Process.md": Tooling, "main.go": ""} {
		if got := Aside(p); got != want {
			t.Errorf("Aside(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestGenerated(t *testing.T) {
	for _, c := range []struct {
		path, content string
		want          bool
	}{
		{"Forms/Main.Designer.cs", "partial class Main {}", true},
		{"Properties/AssemblyInfo.cs", "[assembly: AssemblyTitle(\"x\")]", true},
		{"obj2/Api.g.cs", "", true},
		{"Service References/Billing/Reference.cs", "", true},
		{"Migrations/OrderingContextModelSnapshot.cs", "// <auto-generated />\nusing System;", true},
		{"api/types.pb.go", "// Code generated by protoc-gen-go. DO NOT EDIT.\n\npackage api", true},
		{"Domain/Reference.cs", "public class Reference {}", false},
		{"Order.cs", strings.Repeat("x", 3000) + "<auto-generated>", false},  // only the top counts
		{"docs/codegen.md", "// Code generated by x. DO NOT EDIT.\n", false}, // quoted in a document
		{"gen.go", "// <auto-generated />\npackage gen", false},              // .NET's mark, in Go
		{"obj/Debug/App.g.i.cs", "", true},
		{"Forms/Main.Designer.vb", "", true},
		{"My Project/AssemblyInfo.vb", "", true},
		{"Connected Services/Billing/Reference.cs", "", true},
		{"Module1.vb", "' <auto-generated>\nModule Module1\nEnd Module", true},
		{"api/x.pb.go", "// Code generated by protoc-gen-go. DO NOT EDIT.\r\n\r\npackage api", true}, // CRLF
		// Markdown: an HTML comment opening it, saying both
		{"nostarch/ch01.md", "<!-- DO NOT EDIT THIS FILE.\n\nThis file is periodically generated from /src/. -->\n# One", true},
		{"a.md", "\ufeff\n  <!-- Generated by mdgen; do not edit -->\n", true},
		{"b.md", "<!-- do not edit: the style guide -->\n# B", false},                  // not said to be generated
		{"c.md", "<!-- generated tables below -->\n# C", false},                        // nor not to be edited
		{"d.md", "# D\n<!-- generated, do not edit -->\n", false},                      // not at its top
		{"e.md", "<!-- generated, do not edit\n# E\n", false},                          // never closed
		{"f.md", "<!-- notes -->\n<!-- generated, do not edit -->\n", false},           // the first comment only
		{"g.txt", "<!-- generated, do not edit -->\n", false},                          // text is never generated
		{"h.md", strings.Repeat(" ", 2048) + "<!-- generated, do not edit -->", false}, // past the top
	} {
		if got := Generated(c.path, c.content); got != c.want {
			t.Errorf("Generated(%q) = %v", c.path, got)
		}
	}
}

// TestComments: a comment run right above a type group belongs to its first
// type, and one trailing a line is not the next declaration's.
func TestComments(t *testing.T) {
	p := NewParser()
	defer p.Close()
	src := "package x\n\n// Group.\ntype (\n\tA struct{}\n\tB int\n)\n\nvar v = 1 // the v\nfunc F() {}\n"
	got := flat(p.Parse("go", []byte(src)).Symbols, "")
	if want := []string{"A struct 3-5", "B type 6-6", "F function 10-10"}; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	// Only Rust's //! is about what holds it: in Go it is a doc like any other.
	if got := flat(p.Parse("go", []byte("package x\n\n//! F does.\nfunc F() {}\n")).Symbols, ""); strings.Join(got, ",") != "F function 3-4" {
		t.Errorf("go //!: %q", got)
	}
}
