package jvm

import (
	"strings"
	"testing"

	"deepcode/internal/repo/index/analyzer/syntax"
)

func TestFacts(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"every kind", `package com.acme.web;

import java.util.List;
import java.util.*;
import static org.junit.Assert.assertEquals;
import static com.acme.Util.*;
import com.acme.model.Outer.Inner;

@RestController
public class Api<T> extends Base implements Handler<T> {
    @org.junit.Test
    Map.Entry<String, Outer<Integer>.Inner> e;

    void run() throws IOException {
        var list = new ArrayList<User>();
        com.acme.Foo f = Users.find(Status.ACTIVE);
        Runnable r = Helper::run;
        log.info("x");
    }
}

enum Mode { A }
`, []string{
			"package:com.acme.web",
			"import:java.util.List",
			"star:java.util",
			"import:org.junit.Assert",
			"import:com.acme.Util",
			"import:com.acme.model.Outer.Inner",
			"def:com.acme.web.Api",
			"def:com.acme.web.Mode",
			"type:RestController", "type:Base", "type:Handler",
			"type:org.junit.Test", "type:Map.Entry", "type:String", "type:Outer.Inner", "type:Integer",
			"type:IOException", "type:ArrayList", "type:User", "type:com.acme.Foo",
			"type:Users", "type:Status", "type:Runnable", "type:Helper",
		}},
		{"default package", `/** Entry point. */
public record Main(String[] args) {
    public static void main(String[] args) { App.start(args); }
}

@interface Tag {}
`, []string{"def:Main", "def:Tag", "type:String", "type:App"}},
	}
	for _, c := range cases {
		_, facts := syntax.Parse(Java, []byte(c.src))
		if strings.Join(facts, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", c.name, strings.Join(facts, "\n"), strings.Join(c.want, "\n"))
		}
	}
}

// TestGenerics: type parameters and lowercase annotations name no type.
func TestGenerics(t *testing.T) {
	_, got := syntax.Parse(Java, []byte("class Box<T extends Item> { @lombok.Getter @foo <R> R map(T t) { return null; } }"))
	if want := "def:Box type:Item type:lombok.Getter"; strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
	if _, got = syntax.Parse(Java, []byte("package a.;\nimport a.;\nclass B {}")); strings.Join(got, " ") != "def:B" {
		t.Errorf("a broken package and import: %q", got)
	}
	// A parameter named like a type the file imports may mean that type.
	_, got = syntax.Parse(Java, []byte("import a.Item;\nclass Box { <Item> void f(Item i) {} Item it; }"))
	if want := "import:a.Item def:Box type:Item"; strings.Join(got, " ") != want {
		t.Errorf("got %q, want %q", strings.Join(got, " "), want)
	}
}
