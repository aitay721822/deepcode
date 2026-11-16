// Package mermaid checks a diagram with Mermaid's own parser, the one web/
// draws with (scripts/mermaid bundles it, scripts/build.sh gzips it into
// dist), run in goja: a diagram that passes is one the page can draw.
package mermaid

import (
	"bytes"
	"compress/gzip"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
)

// dist holds parse.js.gz once scripts/build.sh has run; "all:" keeps the
// .gitkeep placeholder so the package compiles before.
//
//go:embed all:dist
var dist embed.FS

// ErrUnbuilt: this binary holds no parser, as scripts/build.sh did not build it.
var ErrUnbuilt = errors.New("the Mermaid parser is not built into this binary: run scripts/build.sh")

// limit is the longest a parse may run: the parser reads a diagram in about
// 40 ms, so one past this is a parser stuck on what a model wrote. A var: a
// test shortens it.
var limit = 5 * time.Second

// most is the most of a parser's error Check returns: "No diagram type
// detected" quotes the whole diagram, and the repair quotes the reply already.
const most = 400

// eng is the one goja runtime, built on the first Check (about 1 s and 70 MB)
// and again after a parse that left it stuck; goja is not safe for concurrent
// use, so mu holds it.
// ponytail: one runtime under a lock, ~40 ms a diagram; pool runtimes (70 MB
// each) if pages ever wait on it.
var (
	mu      sync.Mutex
	eng     *engine
	unbuilt sync.Once
)

type engine struct {
	vm    *goja.Runtime
	parse goja.Callable
}

// load runs the bundle in a new runtime, with what it needs of a browser:
// console (silent), window and self, setTimeout (Mermaid's parse never waits
// on one, so a callback is dropped) and structuredClone (the pie parser
// clones its plain config; JSON's copy suffices).
func load() (*engine, error) {
	gz, err := dist.ReadFile("dist/parse.js.gz")
	if err != nil {
		unbuilt.Do(func() { log.Printf("wiki diagrams are not checked: %v", ErrUnbuilt) })
		return nil, ErrUnbuilt
	}
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	src, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	vm := goja.New()
	g := vm.GlobalObject()
	noop := func(goja.FunctionCall) goja.Value { return goja.Undefined() }
	console := vm.NewObject()
	for _, k := range []string{"log", "info", "warn", "error", "debug", "trace"} {
		_ = console.Set(k, noop)
	}
	for k, v := range map[string]any{"console": console, "window": g, "self": g, "setTimeout": noop, "clearTimeout": noop} {
		_ = g.Set(k, v)
	}
	if _, err := vm.RunString("globalThis.structuredClone = (v) => v === undefined ? v : JSON.parse(JSON.stringify(v))"); err != nil {
		return nil, err
	}
	if _, err := vm.RunScript("parse.js", string(src)); err != nil {
		return nil, fmt.Errorf("loading the Mermaid parser: %w", err)
	}
	parse, ok := goja.AssertFunction(g.Get("mermaidParse"))
	if !ok {
		return nil, errors.New("the Mermaid bundle defines no mermaidParse")
	}
	return &engine{vm: vm, parse: parse}, nil
}

// Check is nil when Mermaid reads code as a diagram, else the parser's error,
// in its own words ("Parse error on line 2: … Expecting 'SQE', … got 'PS'"),
// cut to its first most bytes; ErrUnbuilt when this binary holds no parser.
func Check(code string) error {
	mu.Lock()
	defer mu.Unlock()
	if eng == nil {
		e, err := load()
		if err != nil {
			return err
		}
		eng = e
	}
	settled, err := eng.run(code)
	if !settled { // stopped mid-parse, Mermaid's queue stays busy: every later parse would wait on it
		eng = nil
	}
	return err
}

// run parses code; settled is false when the parse was cut short (the limit,
// a throw out of parse, a panic) or never ended, which leaves the runtime
// unfit for another.
func (e *engine) run(code string) (settled bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			settled, err = false, fmt.Errorf("the Mermaid parser failed: %v", r)
		}
	}()
	// The watch has ended before the interrupt is cleared: none lands on the next parse.
	done, watched := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watched)
		select {
		case <-done:
		case <-time.After(limit):
			e.vm.Interrupt("the Mermaid parse took too long")
		}
	}()
	defer func() {
		close(done)
		<-watched
		e.vm.ClearInterrupt()
	}()
	v, err := e.parse(goja.Undefined(), e.vm.ToValue(code))
	if err != nil {
		return false, err
	}
	// goja runs the promise's jobs before the call returns: it has settled.
	promise, ok := v.Export().(*goja.Promise)
	if !ok {
		return false, fmt.Errorf("mermaidParse returned %T, not a promise", v.Export())
	}
	switch promise.State() {
	case goja.PromiseStateFulfilled:
		return true, nil
	case goja.PromiseStateRejected:
		msg := promise.Result().String()
		if o, ok := promise.Result().(*goja.Object); ok {
			if m := o.Get("message"); m != nil && !goja.IsUndefined(m) {
				msg = m.String()
			}
		}
		if len(msg) > most {
			msg = strings.ToValidUTF8(msg[:most], "") + "…"
		}
		return true, errors.New(msg)
	}
	return false, errors.New("the Mermaid parse never settled")
}
