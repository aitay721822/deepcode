package python

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"deepcode/internal/repo/index/syntax"
)

// TestSymbols: functions, async functions, classes and their methods, nested
// classes, decorators widening the start line, and the docstring left in the
// body.
func TestSymbols(t *testing.T) {
	src := []byte(`import os

@cache
def top(a, b=1):
    """Docs."""
    def inner():
        pass

async def fetch(url):
    pass

class Repo(Base):
    """A repo."""
    value = 1

    def __init__(self):
        self.x = 1

    async def load(self):
        pass

    class Nested:
        def n(self):
            pass
`)
	syms, _ := syntax.Parse(Python, src)
	var got []string
	var walk func([]syntax.Symbol, string)
	walk = func(list []syntax.Symbol, container string) {
		for _, s := range list {
			c := s.Container
			if c == "" {
				c = container
			}
			got = append(got, fmt.Sprintf("%s:%s@%s:%d-%d", s.Name, s.Kind, c, s.StartLine, s.EndLine))
			walk(s.Children, c)
		}
	}
	walk(syms, "")
	want := []string{
		"top:function@:3-7",
		"fetch:function@:9-10",
		"Repo:class@:12-24", "__init__:method@Repo:16-17", "load:method@Repo:19-20",
		"Nested:class@Repo:22-24", "n:method@Nested:23-24",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("symbols:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if syms[0].Signature != "def top(a, b=1):" || !strings.Contains(syms[1].Signature, "def fetch") {
		t.Errorf("signatures %q %q", syms[0].Signature, syms[2].Signature)
	}
}

// TestFacts: imports, aliases, relative imports, star imports and __future__.
func TestFacts(t *testing.T) {
	src := []byte(`import a.b
import c.d as e
from a.b import C, D as E
from . import sibling
from ..pkg import thing
from .mod import *
from __future__ import annotations

def f():
    import local.mod
    from .inner import Value
`)
	_, facts := syntax.Parse(Python, src)
	want := []string{"module:a.b", "module:c.d", "from:a.b", "name:a.b.C", "name:a.b.D",
		"from:.", "name:.sibling", "from:..pkg", "name:..pkg.thing", "from:.mod",
		"module:local.mod", "from:.inner", "name:.inner.Value"}
	if !slices.Equal(facts, want) {
		t.Fatalf("facts %q\nwant %q", facts, want)
	}
}
