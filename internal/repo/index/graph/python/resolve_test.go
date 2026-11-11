package python

import (
	"reflect"
	"testing"
)

// TestResolve: absolute modules under the root and a src/ tree, package
// __init__.py files, a name that is a submodule, and relative imports against
// the importing file's package.
func TestResolve(t *testing.T) {
	files := map[string][]string{
		"app/main.py":       {"from:pkg.mod", "name:pkg.mod.Thing", "name:pkg.util"},
		"pkg/__init__.py":   nil,
		"pkg/mod.py":        {"from:.", "name:.util", "from:..pkg", "name:..pkg.mod", "from:os", "name:os.path"},
		"pkg/util.py":       {"module:os", "from:pkg", "name:pkg.mod"},
		"pkg/__main__.py":   {"from:.", "name:.util"},
		"src/lib/core.py":   {"from:.", "name:.helper", "from:lib", "name:lib.helper"},
		"src/lib/helper.py": nil,
	}
	got := Resolve(files, nil)
	want := [][2]string{
		{"app/main.py", "pkg/mod.py"},
		{"app/main.py", "pkg/util.py"},
		{"pkg/__main__.py", "pkg/__init__.py"},
		{"pkg/__main__.py", "pkg/util.py"},
		{"pkg/mod.py", "pkg/__init__.py"},
		{"pkg/mod.py", "pkg/util.py"},
		{"pkg/util.py", "pkg/__init__.py"},
		{"pkg/util.py", "pkg/mod.py"},
		{"src/lib/core.py", "src/lib/helper.py"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("edges %v\nwant %v", got, want)
	}
}

// TestWheres: pyproject.toml and setup.cfg name a source root.
func TestWheres(t *testing.T) {
	if got := wheres("pyproject.toml", "[tool.setuptools]\npackage-dir = {\"\" = \"lib\"}\n[tool.setuptools.packages.find]\nwhere = [\"src\"]\n"); !reflect.DeepEqual(got, []string{"lib", "src"}) {
		t.Errorf("pyproject wheres %q", got)
	}
	if got := wheres("setup.cfg", "[options.packages.find]\nwhere = src, lib\n"); !reflect.DeepEqual(got, []string{"src", "lib"}) {
		t.Errorf("setup.cfg wheres %q", got)
	}
	if got := wheres("setup.py", "find_packages(where=\"src\")"); !reflect.DeepEqual(got, []string{"src"}) {
		t.Errorf("setup.py wheres %q", got)
	}
}
