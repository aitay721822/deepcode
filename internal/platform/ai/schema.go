package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	checker "github.com/google/jsonschema-go/jsonschema"
	"github.com/invopop/jsonschema"
)

// ErrSchema: an answer asked for in a JSON schema that is not that JSON. A
// server that refuses the request fails with its own words, as for any other
// request.
var ErrSchema = errors.New("the model's answer is not the JSON its schema asks for")

// Where a request for JSON gives its schema (OpenAI.Schema).
const (
	// SchemaServer sends it as response_format, OpenAI's structured outputs:
	// the default.
	SchemaServer = "server"
	// SchemaPrompt writes it last in the request's system message and sends no
	// response_format, for a server that holds the model to no schema anyway
	// and sends an answer in one asks for only once it is whole (Strata).
	SchemaPrompt = "prompt"
)

// SchemaLine opens the schema a request's system message holds under
// SchemaPrompt.
const SchemaLine = "Reply with only JSON matching this JSON schema:"

// Format is a structured output: answers asked for in the JSON schema of a T
// and read into a T.
type Format[T any] struct{ schema }

// schema is a JSON schema, under the name a request gives it, as its JSON
// text, and resolved to check answers by.
type schema struct {
	Name  string
	JSON  map[string]any
	text  string
	check *checker.Resolved
}

// Schema is the Format of T, its schema made as the SDK's README makes one
// (GenerateSchema): every field required, no other, no $ref.
func Schema[T any](name string) Format[T] {
	r := jsonschema.Reflector{AllowAdditionalProperties: false, DoNotReference: true}
	var v T
	data, err := json.Marshal(r.Reflect(v))
	if err != nil {
		panic(err) // a Go type no schema describes: a mistake in the code
	}
	s, err := compile(name, data)
	if err != nil {
		panic(err)
	}
	return Format[T]{s}
}

// Dynamic is the Format of T for a schema built at request time: the same
// request and the same check of the answer as Schema's, the checker resolved
// for this schema. A schema past OpenAI's structured-output limits is an error
// naming the limit, before anything is sent.
func Dynamic[T any](name string, js map[string]any) (Format[T], error) {
	data, err := json.Marshal(js)
	if err != nil {
		return Format[T]{}, fmt.Errorf("schema %s: %w", name, err)
	}
	var doc map[string]any // as JSON reads it: every list []any, every number float64
	if err := json.Unmarshal(data, &doc); err != nil {
		return Format[T]{}, fmt.Errorf("schema %s: %w", name, err)
	}
	var t tally
	if err := t.walk(doc, 0); err != nil {
		return Format[T]{}, fmt.Errorf("schema %s: %w", name, err)
	}
	if err := t.within(); err != nil {
		return Format[T]{}, fmt.Errorf("schema %s: %w", name, err)
	}
	s, err := compile(name, data)
	if err != nil {
		return Format[T]{}, fmt.Errorf("schema %s: %w", name, err)
	}
	return Format[T]{s}, nil
}

// compile is the schema data names, resolved; its JSON is kept raw, so no
// number is rounded through float64.
func compile(name string, data []byte) (schema, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return schema{}, err
	}
	var c checker.Schema
	if err := json.Unmarshal(data, &c); err != nil {
		return schema{}, err
	}
	check, err := c.Resolve(nil)
	if err != nil {
		return schema{}, err
	}
	s := schema{Name: name, JSON: make(map[string]any, len(raw)), text: string(data), check: check}
	for k, v := range raw {
		s.JSON[k] = v
	}
	return s, nil
}

// OpenAI's structured-output limits on a schema: object properties in all,
// levels of nesting, enum values in all, characters of one enum of more than
// bigEnum values, and characters of every property name, enum and const value.
// A schema here has no $defs, allOf or oneOf (strict outputs, DoNotReference).
const (
	maxProperties = 5000
	maxDepth      = 10
	maxEnums      = 1000
	bigEnum       = 250
	maxBigEnum    = 15000
	maxChars      = 120000
)

// tally counts what the limits bound, walking a schema as JSON reads it.
type tally struct{ properties, depth, enums, chars int }

// walk counts s, depth the objects with properties above it.
func (t *tally) walk(s map[string]any, depth int) error {
	if props, ok := s["properties"].(map[string]any); ok {
		depth++
		t.depth = max(t.depth, depth)
		t.properties += len(props)
		for name, sub := range props {
			t.chars += utf8.RuneCountInString(name)
			if err := t.sub(sub, depth); err != nil {
				return err
			}
		}
	}
	if err := t.sub(s["items"], depth); err != nil {
		return err
	}
	branches, _ := s["anyOf"].([]any)
	for _, sub := range branches {
		if err := t.sub(sub, depth); err != nil {
			return err
		}
	}
	if values, ok := s["enum"].([]any); ok {
		n := 0
		for _, v := range values {
			n += utf8.RuneCountInString(fmt.Sprint(v))
		}
		if len(values) > bigEnum && n > maxBigEnum {
			return fmt.Errorf("an enum of %d values holds %d characters, past the %d structured outputs allow one of more than %d values",
				len(values), n, maxBigEnum, bigEnum)
		}
		t.enums += len(values)
		t.chars += n
	}
	if c, ok := s["const"]; ok {
		t.chars += utf8.RuneCountInString(fmt.Sprint(c))
	}
	return nil
}

// sub walks v when it is a schema.
func (t *tally) sub(v any, depth int) error {
	if s, ok := v.(map[string]any); ok {
		return t.walk(s, depth)
	}
	return nil
}

// within names the first limit the tally passes.
func (t *tally) within() error {
	switch {
	case t.properties > maxProperties:
		return fmt.Errorf("%d object properties, past the %d structured outputs allow", t.properties, maxProperties)
	case t.depth > maxDepth:
		return fmt.Errorf("%d levels of nesting, past the %d structured outputs allow", t.depth, maxDepth)
	case t.enums > maxEnums:
		return fmt.Errorf("%d enum values, past the %d structured outputs allow", t.enums, maxEnums)
	case t.chars > maxChars:
		return fmt.Errorf("%d characters of names, enum and const values, past the %d structured outputs allow", t.chars, maxChars)
	}
	return nil
}

// Decode reads text, f's JSON, into a T, its schema checked, so {} is not a T
// with nothing in it.
func Decode[T any](f Format[T], text string) (v T, err error) {
	var doc any
	if err = json.Unmarshal([]byte(text), &doc); err == nil {
		err = f.check.Validate(doc)
	}
	if err == nil {
		err = json.Unmarshal([]byte(text), &v)
	}
	if err != nil {
		return v, &DecodeError{Name: f.Name, Err: err}
	}
	return v, nil
}

// DecodeError is text that is not format Name's JSON, in the JSON reader's or
// the schema validator's words.
type DecodeError struct {
	Name string
	Err  error
}

func (e *DecodeError) Error() string { return fmt.Sprintf("not the JSON of %s (%v)", e.Name, e.Err) }

func (e *DecodeError) Unwrap() error { return e.Err }

// indented closes the instructions of every request for JSON: a server that
// does not constrain the model's decoding (Strata) gets JSON written on one
// line, which the model ended a closer short in 8 of 24 replies to a plan's
// outline, three objects deep; written a key a line, in none of 40.
const indented = "Write the JSON indented, each key on a line of its own."

// Prompt is p as StreamJSON sends it to c for f's JSON, what such a request
// is sized by: its instructions end, under SchemaPrompt, with SchemaLine and
// f's schema, then with indented. The first request and a repair after it
// hold the same instructions, so a prompt cache keeps them.
func (f Format[T]) Prompt(c *OpenAI, p Prompt) Prompt {
	if c.Schema == SchemaPrompt {
		p.Static += "\n\n" + SchemaLine + "\n" + f.text
	}
	if p.Static == "" {
		p.Static = indented
	} else {
		p.Static += "\n" + indented
	}
	return p
}

// StreamJSON streams c's answer to f.Prompt(c, p) in f's JSON, relaying its
// text as it comes, and reads it into a T (Decode); reply is the answer as
// written. Under SchemaPrompt it sends no response_format. An answer that is
// not f's JSON is an error wrapping ErrSchema; one cut at the output budget
// says so instead.
func StreamJSON[T any](ctx context.Context, c *OpenAI, p Prompt, f Format[T], onDelta func(string)) (v T, reply string, err error) {
	s := &f.schema
	if c.Schema == SchemaPrompt {
		s = nil
	}
	reply, spent, err := c.stream(ctx, f.Prompt(c, p).messages(), s, onDelta)
	if err != nil {
		return v, reply, err
	}
	if spent && !json.Valid([]byte(reply)) {
		return v, reply, fmt.Errorf("%s: the answer reached the output budget before its JSON ended: raise DEEPCODE_MAX_OUTPUT_TOKENS", c.ModelName)
	}
	if v, err = Decode(f, reply); err != nil {
		return v, reply, fmt.Errorf("%s: the answer is %w: %w", c.ModelName, err, ErrSchema)
	}
	return v, reply, nil
}
