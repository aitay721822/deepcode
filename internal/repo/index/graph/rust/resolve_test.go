package rust

import (
	"slices"
	"strings"
	"testing"

	rustgrammar "deepcode/internal/repo/index/analyzer/rust"
	"deepcode/internal/repo/index/analyzer/syntax"
)

// edges resolves sources, read with the analyzer's Rust grammar, as "src -> dst" lines, sorted and once each.
func edges(sources, configs map[string]string) []string {
	files := map[string][]string{}
	for p, src := range sources {
		_, files[p] = syntax.Parse(rustgrammar.Rust, []byte(src))
	}
	var out []string
	for _, e := range Resolve(files, configs) {
		out = append(out, e[0]+" -> "+e[1])
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func check(t *testing.T, got, want []string) {
	t.Helper()
	slices.Sort(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestWorkspace: two member crates, one using the other by its hyphenated name.
func TestWorkspace(t *testing.T) {
	const core, server = "crates/core/", "crates/server/"
	configs := map[string]string{
		"Cargo.toml":          "[workspace]\nmembers = [\"crates/*\"]\n",
		core + "Cargo.toml":   "[package]\nname = \"web-core\" # the library\nversion = \"0.1.0\"\n\n[dependencies]\nserde = \"1\"\n",
		server + "Cargo.toml": "[package]\nname = \"server\"\n\n[dependencies]\nweb-core = { path = \"../core\" }\n",
		"tools/App.csproj":    "<Project />",
	}
	sources := map[string]string{
		core + "src/lib.rs": `//! Core.
pub mod util;
pub mod model;
pub mod api {
    pub mod v1;
}
pub use crate::{util::helper, model::User};

pub fn run() {
    self::util::helper();
}
`,
		core + "src/util.rs":         "mod fmt;\npub use fmt::pad;\npub fn helper() {}\n",
		core + "src/util/fmt.rs":     "use super::super::model::User;\npub fn pad(u: &User) { super::helper(); }\n",
		core + "src/model/mod.rs":    "mod user;\npub use self::user::{Name, User};\nuse serde::Serialize;\n",
		core + "src/model/user.rs":   "use crate::util::fmt::pad;\npub struct User;\npub struct Name;\n",
		core + "src/api/v1.rs":       "pub fn get() { super::super::util::helper() }\n",
		core + "src/orphan.rs":       "use crate::util::helper;\n", // declared by no module: no crate to read crate:: in
		core + "tests/api.rs":        "mod common;\nuse web_core::util;\n\n#[test]\nfn t() { common::setup(); assert!(web_core::model::User::ok()); }\n",
		core + "tests/common/mod.rs": "pub fn setup() { web_core::run(); }\n",
		core + "examples/demo.rs":    "use web_core::model::user::Name;\nfn main() {}\n",
		server + "src/main.rs":       "mod routes;\nuse web_core::model::User;\n\nfn main() {\n    routes::index();\n    tokio::spawn(async {});\n}\n",
		server + "src/routes.rs":     "use web_core::{helper, util::helper as h};\nuse std::sync::Arc;\npub fn index() {}\n",
	}
	check(t, edges(sources, configs), []string{
		core + "src/lib.rs -> " + core + "src/util.rs", // mod util; and use crate::{util::helper}
		core + "src/lib.rs -> " + core + "src/model/mod.rs",
		core + "src/lib.rs -> " + core + "src/api/v1.rs", // mod v1; inside mod api {}
		core + "src/util.rs -> " + core + "src/util/fmt.rs",
		core + "src/util/fmt.rs -> " + core + "src/model/mod.rs", // super::super::
		core + "src/util/fmt.rs -> " + core + "src/util.rs",
		core + "src/model/mod.rs -> " + core + "src/model/user.rs",
		core + "src/model/user.rs -> " + core + "src/util/fmt.rs",
		core + "src/api/v1.rs -> " + core + "src/util.rs", // v1 sits at api::v1
		core + "tests/api.rs -> " + core + "tests/common/mod.rs",
		core + "tests/api.rs -> " + core + "src/util.rs",
		core + "tests/api.rs -> " + core + "src/model/mod.rs", // a path inside assert!
		core + "tests/common/mod.rs -> " + core + "src/lib.rs",
		core + "examples/demo.rs -> " + core + "src/model/user.rs",
		server + "src/main.rs -> " + server + "src/routes.rs",
		server + "src/main.rs -> " + core + "src/model/mod.rs",
		server + "src/routes.rs -> " + core + "src/lib.rs",
		server + "src/routes.rs -> " + core + "src/util.rs",
	})
}

// TestLibPath: a crate at the root whose [lib] names its own path and name.
func TestLibPath(t *testing.T) {
	configs := map[string]string{"Cargo.toml": "[package]\nname = \"fast-core-lib\"\n\n[lib]\nname = 'fast_core'\npath = \"src/core.rs\"\n\n[[bin]]\nname = \"cli\"\npath = \"cli/run.rs\"\n"}
	sources := map[string]string{
		"src/core.rs":    "mod a;\npub use a::X;\n", // a root: its modules sit beside it, not in src/core/
		"src/a.rs":       "pub struct X;\n",
		"benches/b.rs":   "use fast_core::a::X;\nuse fast_core_lib::Thing;\n",
		"cli/run.rs":     "mod args;\nfn main() { fast_core::a::X; }\n",
		"cli/args.rs":    "use crate::main;\n", // crate:: in a binary is the binary
		"other/lib.rs":   "mod m;\n",
		"other/m/mod.rs": "",
	}
	check(t, edges(sources, configs), []string{
		"src/core.rs -> src/a.rs",
		"benches/b.rs -> src/a.rs",
		"benches/b.rs -> src/core.rs", // by the package's name
		"cli/run.rs -> cli/args.rs",
		"cli/run.rs -> src/a.rs",
		"cli/args.rs -> cli/run.rs",
		"other/lib.rs -> other/m/mod.rs", // no root, but lib.rs declares its modules beside it
	})
}

// TestCrateNames: a crate's name reaches only the crates its package sees: its
// own, its workspace's, and its own or its workspace root's path dependencies;
// listings that stand alone and share names never meet, nor does a file of no
// package, or of a Cargo.toml that is no TOML, reach any.
func TestCrateNames(t *testing.T) {
	configs := map[string]string{
		"Cargo.toml": `[workspace]
members = ["./packages/*", "listings/*"]
exclude = [
  "./listings/", # apart, though listings/* matches them
  'tmp',
]

[workspace.dependencies]
shared = { version = "1", path = "vendor/shared" }
`,
		"packages/tool/Cargo.toml":      "[package]\nname = \"tool\"\n[dependencies]\nshared.workspace = true\n",
		"packages/user/Cargo.toml":      "[package]\nname = \"user\"\n[dependencies]\nnear = { path = \"../../listings/d\" }\n",
		"vendor/shared/Cargo.toml":      "[package]\nname = \"shared\"\n",
		"listings/e/Cargo.toml":         "[package\nname = \"hello\"\n",
		"listings/a/Cargo.toml":         "[package]\nname = \"hello\"\n",
		"listings/b/Cargo.toml":         "[package]\nname = \"hello\"\n",
		"listings/c/Cargo.toml":         "[package]\nname = \"tool\"\n[dependencies.near]\npath = \"../d\"\nversion = \"1\"\n",
		"listings/d/Cargo.toml":         "[package]\nname = \"near\"\n",
		"listings/w/Cargo.toml":         "[workspace]\nmembers = [\"adder\", \"add_one\"]\n",
		"listings/w/adder/Cargo.toml":   "[package]\nname = \"adder\"\n",
		"listings/w/add_one/Cargo.toml": "[package]\nname = \"add_one\"\n",
		"listings/x/add_one/Cargo.toml": "[package]\nname = \"add_one\"\n",
	}
	sources := map[string]string{
		"packages/tool/src/lib.rs":       "use shared::S;\n",
		"packages/user/src/main.rs":      "use tool::run;\nuse hello::Pool;\nuse near::N;\n",
		"vendor/shared/src/lib.rs":       "pub struct S;\n",
		"listings/a/src/lib.rs":          "pub struct Pool;\n",
		"listings/a/src/main.rs":         "use hello::Pool;\n",
		"listings/b/src/lib.rs":          "pub struct Pool;\n",
		"listings/b/src/main.rs":         "use hello::Pool;\nuse tool::run;\n",
		"listings/c/src/lib.rs":          "use near::N;\n",
		"listings/d/src/lib.rs":          "pub struct N;\n",
		"listings/w/adder/src/main.rs":   "use add_one::add;\n",
		"listings/w/add_one/src/lib.rs":  "pub fn add() {}\n",
		"listings/x/add_one/src/lib.rs":  "pub fn add() {}\n",
		"listings/x/add_one/src/main.rs": "use add_one::add;\n",
		"listings/e/src/lib.rs":          "pub struct Pool;\n",
		"listings/e/src/main.rs":         "use hello::Pool;\n",
		"scripts/loose.rs":               "use hello::Pool;\nuse shared::S;\n",
	}
	check(t, edges(sources, configs), []string{
		"packages/tool/src/lib.rs -> vendor/shared/src/lib.rs",  // the workspace root's path dependency
		"packages/user/src/main.rs -> packages/tool/src/lib.rs", // its workspace's tool, not the excluded listing c
		"packages/user/src/main.rs -> listings/d/src/lib.rs",    // its own path dependency, out of its workspace
		"listings/a/src/main.rs -> listings/a/src/lib.rs",
		"listings/b/src/main.rs -> listings/b/src/lib.rs", // not a's, nor the excluded c's tool
		"listings/c/src/lib.rs -> listings/d/src/lib.rs",
		"listings/w/adder/src/main.rs -> listings/w/add_one/src/lib.rs",
		"listings/x/add_one/src/main.rs -> listings/x/add_one/src/lib.rs",
	})
}

// TestReadCargo: what a Cargo.toml's TOML says, written every way Cargo
// takes it: a # in a string, a dotted key, a dependency's own table, an
// array across lines, a [patch].
func TestReadCargo(t *testing.T) {
	c, err := readCargo("x", `[package]
description = "a [b # c"
name = "a-b" # the package
[package.metadata.docs]
name = "no"
[lib]
path = "lib.rs"
[[bin]]
path = "cli/run.rs"
[dev-dependencies]
p = { version = "1", path = "p" }
q = { version = "1", path = '../q' }
s = "1"
[target.'cfg(unix)'.dependencies]
r = {path="r"}
t.path = "t"
[build-dependencies.v]
path = "v"
[patch.crates-io]
u = { path = "../u" }
`)
	deps := strings.Join(slices.Sorted(slices.Values(c.deps)), ",")
	if err != nil || !c.pkg || c.space || strings.Join(c.names, ",") != "a_b" || c.lib != "x/lib.rs" || strings.Join(c.bins, ",") != "x/cli/run.rs" || deps != "q,u,x/p,x/r,x/t,x/v" {
		t.Errorf("%+v %v", c, err)
	}
	w, err := readCargo("w", "[workspace]\nmembers = [\n  \"./crates/*/\", # all\n  \"tool\",\n]\nexclude = [\"crates/old\"]\n")
	if err != nil || w.pkg || !w.space || strings.Join(w.members, ",") != "./crates/*/,tool" {
		t.Errorf("a workspace root: %+v %v", w, err)
	}
	for d, want := range map[string]bool{"w": true, "w/crates/a": true, "w/tool": true, "w/crates/old": false, "w/crates/old/x": false, "w/crates/a/b": false, "w/other": false} {
		if w.member(d) != want {
			t.Errorf("member %s: %v", d, !want)
		}
	}
	if _, err := readCargo("y", "[package\nname = \"y\"\n"); err == nil {
		t.Error("a Cargo.toml that is no TOML read")
	}
}
