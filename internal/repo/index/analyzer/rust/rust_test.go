package rust

import (
	"fmt"
	"strings"
	"testing"

	"deepcode/internal/repo/index/analyzer/syntax"
)

const sample = `//! Crate docs.
use crate::routing::{Router, method_routing::{get, post as p}};
use crate::a::{self, b::*};
use super::extract::State;
use self::inner::Thing as T;
use ::serde::Serialize;
use std::sync::Arc;
mod inner;
pub mod handler;

/// A server.
#[derive(Debug, Clone)]
pub struct Server<S> {
    state: S,
}

/** Kinds. */
pub enum Kind { A, B }
pub union U { a: u32 }
pub type Res<T> = Result<T, Error>;

/// A service.
pub trait Service {
    /// Calls it.
    fn call(&self) -> u8;
    fn ready(&self) -> bool {
        true
    }
}

impl<S: Clone> Server<S> {
    /// Makes one.
    #[inline]
    pub fn new(state: S) -> Self {
        crate::util::helper();
        Self { state }
    }
}

impl<T> Service for crate::wrap::Wrapper<T> {
    type Out = u8;
    fn call(&self) -> u8 {
        routing::get();
        1
    }
}

#[macro_export]
macro_rules! route {
    ($t:ty) => { $crate::util::log($t::helper::new()) };
}

pub mod nested {
    pub mod deeper;
    use super::Kind;
    pub fn f() {
        super::super::up::g();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn works() {
        assert_eq!(crate::util::check(), Vec::new());
    }
}
`

// flat renders symbols as "QualifiedName kind start-end", children indented.
func flat(syms []syntax.Symbol, parent, indent string) []string {
	var out []string
	for _, s := range syms {
		name := s.QualifiedName(parent)
		out = append(out, fmt.Sprintf("%s%s %s %d-%d", indent, name, s.Kind, s.StartLine, s.EndLine))
		out = append(out, flat(s.Children, name, indent+"  ")...)
	}
	return out
}

func TestSymbols(t *testing.T) {
	syms, _ := syntax.Parse(Rust, []byte(sample))
	want := []string{
		"Server struct 11-15",
		"Kind enum 17-18",
		"U union 19-19",
		"Res type 20-20",
		"Service trait 22-29",
		"  Service.call method 24-25",
		"  Service.ready method 26-28",
		"Server.new method 32-37",
		"Wrapper.call method 42-45",
		"route macro 48-51",
		"nested namespace 53-59",
		"  nested.f function 56-58",
		"tests namespace 61-69",
		"  tests.works function 65-68",
	}
	if got := flat(syms, "", ""); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, s := range syms {
		if s.Kind == "method" && s.Name == "new" && (s.Container != "Server" || s.Signature != "pub fn new(state: S) -> Self {") {
			t.Errorf("new: %+v", s)
		}
	}
}

func TestFacts(t *testing.T) {
	_, got := syntax.Parse(Rust, []byte(sample))
	want := []string{
		"mod:inner", "mod:handler", "mod:nested/deeper",
		"use:crate::routing::Router", "use:crate::routing::method_routing::get", "use:crate::routing::method_routing::post",
		"use:crate::a", "use:crate::a::b", "use:super::extract::State", "use:self::inner::Thing", "use:serde::Serialize",
		"use:self::Kind", "use:self", // super:: inside mod nested and mod tests
		"use:crate::util::helper", "use:crate::wrap::Wrapper", "use:routing::get",
		"use:crate::util::log", // $crate:: in macro_rules!, and no helper::new from $t::helper::new
		"use:super::up::g",
		"use:crate::util::check", // inside assert_eq!
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestFromFile: a path inside inline modules is read from the file's module.
func TestFromFile(t *testing.T) {
	cases := []struct {
		path, inline, want string
	}{
		{"super::x", "", "super::x"},
		{"self::x", "a::b", "self::a::b::x"},
		{"super::x", "a::b", "self::a::x"},
		{"super::super::x", "a::b", "self::x"},
		{"super::super::super::x", "a::b", "super::x"},
		{"crate::x", "a", "crate::x"},
		{"util::x", "a", "util::x"},
	}
	for _, c := range cases {
		var inline []string
		if c.inline != "" {
			inline = strings.Split(c.inline, "::")
		}
		if got := strings.Join(fromFile(strings.Split(c.path, "::"), inline), "::"); got != c.want {
			t.Errorf("%s in %s: got %s, want %s", c.path, c.inline, got, c.want)
		}
	}
}

// TestStd: paths through a name a use brings from std are std's, an extern
// block's functions are symbols, and a comment trailing a line is not the
// next item's.
func TestStd(t *testing.T) {
	src := "use std::{fmt::{self, Write}, io as sio};\nuse crate::fmt::Local;\nimpl fmt::Display for S {}\n" +
		"fn f() { sio::stdout(); fmt::format(); crate::fmt::x(); }\nextern \"C\" { fn ext(); }\nstruct T; // t\nstruct U;\n"
	syms, facts := syntax.Parse(Rust, []byte(src))
	if got, want := strings.Join(facts, " "), "use:crate::fmt::Local use:crate::fmt::x"; got != want {
		t.Errorf("facts %q, want %q", got, want)
	}
	if got, want := strings.Join(flat(syms, "", ""), ", "), "f function 4-4, ext function 5-5, T struct 6-6, U struct 7-7"; got != want {
		t.Errorf("symbols %q, want %q", got, want)
	}
}

// TestInnerDocs: //! describes the module, not the next item, and a glob from
// std names nothing a path starts from.
func TestInnerDocs(t *testing.T) {
	syms, facts := syntax.Parse(Rust, []byte("//! crate doc\nuse std::collections::*;\nmod collections;\nfn f() { collections::x(); }\n"))
	if len(syms) != 1 || syms[0].StartLine != 4 {
		t.Errorf("symbols %+v", syms)
	}
	if got, want := strings.Join(facts, " "), "mod:collections use:collections::x"; got != want {
		t.Errorf("facts %q, want %q", got, want)
	}
}
