package jvm

import (
	"fmt"
	"strings"
	"testing"

	"deepcode/internal/repo/index/analyzer/syntax"
)

// flat renders symbols as "Qualified.Name kind start-end", children indented.
func flat(syms []syntax.Symbol, parent, indent string) []string {
	var out []string
	for _, s := range syms {
		q := s.QualifiedName(parent)
		out = append(out, fmt.Sprintf("%s%s %s %d-%d", indent, q, s.Kind, s.StartLine, s.EndLine))
		out = append(out, flat(s.Children, q, indent+"  ")...)
	}
	return out
}

func TestSymbols(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string
	}{
		{"spring controller", `/*
 * Copyright header.
 */
package com.acme.web;

import com.acme.service.UserService;
import org.springframework.web.bind.annotation.*;

/**
 * Serves users.
 */
@RestController
@RequestMapping("/users")
public class UserController {
    private final UserService service;

    /** Wires the service. */
    public UserController(UserService service) {
        this.service = service;
    }

    /**
     * Finds one user.
     */
    @GetMapping("/{id}")
    public ResponseEntity<User> get(@PathVariable long id) {
        return ResponseEntity.ok(service.find(id));
    }

    // Lists them.
    @GetMapping
    public List<User> list() {
        return service.all();
    }
}
`, []string{
			"UserController class 9-35",
			"  UserController.UserController constructor 17-20",
			"  UserController.get method 22-28",
			"  UserController.list method 30-34",
		}},
		{"enum with methods", `package com.acme.model;

/** A user's status. */
public enum Status {
    /** Can sign in. */
    ACTIVE("a"),
    BLOCKED("b") {
        @Override
        public boolean canSignIn() { return false; }
    };

    private final String code;

    Status(String code) {
        this.code = code;
    }

    /** Whether the user may sign in. */
    public boolean canSignIn() {
        return true;
    }
}
`, []string{
			"Status enum 3-22",
			"  Status.Status constructor 14-16",
			"  Status.canSignIn method 18-21",
		}},
		{"record and annotation type", `package com.acme.model;

import java.util.List;

/** A page of results. */
public record Page<T extends Comparable<T>>(List<T> items, int total) implements Iterable<T> {
    /** Rejects a negative total. */
    public Page {
        if (total < 0) throw new IllegalArgumentException("total");
    }

    public Page(List<T> items) {
        this(items, items.size());
    }

    @Override
    public Iterator<T> iterator() { return items.iterator(); }

    public <R> Page<R> map(Function<? super T, ? extends R> f) {
        return new Page<>(items.stream().map(f).toList(), total);
    }
}

/**
 * Marks an audited endpoint.
 */
@Retention(RetentionPolicy.RUNTIME)
@Target(ElementType.METHOD)
@interface Audited {
    /** The action logged. */
    String value() default "";
    int level() default 1;
}
`, []string{
			"Page record 5-22",
			"  Page.Page constructor 7-10",
			"  Page.Page constructor 12-14",
			"  Page.iterator method 16-17",
			"  Page.map method 19-21",
			"Audited annotation 24-33",
			"  Audited.value method 30-31",
			"  Audited.level method 32-32",
		}},
		{"generics and nested types", `package com.acme.cache;

public interface Cache<K, V> {
    V get(K key);

    /** A cache entry. */
    interface Entry<K, V> {
        K key();
    }

    default void clear() {}
}

class LruCache<K, V> implements Cache<K, V> {
    private final Map<K, Node<K, V>> index = new HashMap<>();

    public V get(K key) { return null; }

    static final class Node<K, V> {
        Node<K, V> prev, next;

        Node() {}

        class Handle {
            void release() {}
        }
    }
}
`, []string{
			"Cache interface 3-12",
			"  Cache.get method 4-4",
			"  Cache.Entry interface 6-9",
			"    Cache.Entry.key method 8-8",
			"  Cache.clear method 11-11",
			"LruCache class 14-28",
			"  LruCache.get method 17-17",
			"  LruCache.Node class 19-27",
			"    LruCache.Node.Node constructor 22-22",
			"    LruCache.Node.Handle class 24-26",
			"      LruCache.Node.Handle.release method 25-25",
		}},
	}
	for _, c := range cases {
		syms, _ := syntax.Parse(Java, []byte(c.src))
		if got := flat(syms, "", ""); strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("%s:\ngot:\n%s\nwant:\n%s", c.name, strings.Join(got, "\n"), strings.Join(c.want, "\n"))
		}
	}
}

// TestSignature: a signature is the line naming the declaration, not the
// annotation above it.
func TestSignature(t *testing.T) {
	src := "@RestController\npublic class A {\n    @GetMapping(\"/x\")\n    public String x(\n        long id) { return \"\"; }\n}\n"
	syms, _ := syntax.Parse(Java, []byte(src))
	if len(syms) != 1 || len(syms[0].Children) != 1 {
		t.Fatalf("%+v", syms)
	}
	if got := syms[0].Signature; got != "public class A {" {
		t.Errorf("class: %q", got)
	}
	if got := syms[0].Children[0].Signature; got != "public String x(" {
		t.Errorf("method: %q", got)
	}
}

// TestBraceComment: a note on a body's opening line is not its first member's.
func TestBraceComment(t *testing.T) {
	syms, _ := syntax.Parse(Java, []byte("class A { // c\n  void f() {}\n}\nenum E { X, Y; // c\n  void g() {}\n}\n"))
	if len(syms) != 2 || syms[0].Children[0].StartLine != 2 || syms[1].Children[0].StartLine != 5 {
		t.Fatalf("%+v", syms)
	}
}
