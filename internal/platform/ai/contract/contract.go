// Package contract holds a model's structured reply to the rules its request
// states. The schema states them to the model, and a server that constrains
// decoding enforces them; one that does not (Strata) only shows them. So code
// checks every rule, asks once for a repair naming what broke, and fixes what
// is left, counting each rule at each step.
package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"deepcode/internal/platform/ai"
)

// Rule names a rule a reply may break.
type Rule string

// Violation is one way a reply breaks a rule: where, and what, in the words
// the repair note quotes to the model.
type Violation struct {
	Rule   Rule
	Where  string // a key, heading, unit or path
	Detail string
	Minor  bool // code fixes it alone: no repair is asked for it
}

// Count is a rule's violations: in the first reply, in the reply after the
// repair (when one was asked), and fixed by code at the end.
type Count struct {
	First    int `json:"first"`
	Repaired int `json:"afterRepair"`
	Residual int `json:"residual"`
}

// Counts are a run's, by rule; safe for requests made at a time.
type Counts struct {
	mu    sync.Mutex
	by    map[Rule]*Count
	other string // the validator's words on the run's first schema.other
}

// step is where in Run a violation is counted.
type step int

const (
	first step = iota
	repaired
	residual
)

// add counts vs at step s.
func (c *Counts) add(s step, vs ...Violation) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.by == nil {
		c.by = map[Rule]*Count{}
	}
	for _, v := range vs {
		n := c.by[v.Rule]
		if n == nil {
			n = &Count{}
			c.by[v.Rule] = n
		}
		switch s {
		case first:
			n.First++
		case repaired:
			n.Repaired++
		case residual:
			n.Residual++
		}
	}
}

// Snapshot is a copy of the counts.
func (c *Counts) Snapshot() map[Rule]Count {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[Rule]Count, len(c.by))
	for r, n := range c.by {
		out[r] = *n
	}
	return out
}

// Residual counts n of rule r that code found left with no model step: files
// placement left on no page.
func (c *Counts) Residual(r Rule, n int) {
	for range n {
		c.add(residual, Violation{Rule: r})
	}
}

// Other is the validator's words on the run's first schema.other, "" when
// there was none: one that is not an extra key is a check missing.
func (c *Counts) Other() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.other
}

// ErrLoose: a reply that reads into its T but fails its schema. Ask returns
// it with the reply read, so Check can name what broke.
var ErrLoose = errors.New("the reply reads, but breaks its schema")

// Turn is one request held to a contract.
type Turn[T any] struct {
	// Kind names the request's json and schema.other rules: "section" counts
	// "section.json" and "section.schema.other".
	Kind string
	// Ask sends p and reads the reply. A reply that does not read into T is
	// an error wrapping ai.ErrSchema; one that reads but fails the schema is
	// v with an error wrapping ErrLoose.
	Ask func(ctx context.Context, p ai.Prompt) (v T, reply string, err error)
	// Check names every rule the schema states that v breaks, and what the
	// schema cannot say.
	Check func(T) []Violation
	// Note is the repair's message, naming every violation in the model's
	// own terms.
	Note func([]Violation) string
	// Merge joins the repair into the first reply; nil: the repair replaces it.
	Merge func(first, fix T, repaired []Violation) T
	// Fix is code's fix for every violation Check names.
	Fix func(*T)
}

// jsonNote asks again for a reply that is not the JSON asked for, saying how
// in the reader's or validator's words (ai.DecodeError), not the endpoint's.
const jsonNote = "Your reply is %s. Reply again with only the JSON asked for."

// Run asks, checks, repairs once with p.After(reply, note), checks again, has
// code fix the rest, and counts each rule at each step. A reply that does not
// read into T counts as Kind.json and is repaired like any other violation;
// if the repair does not read either, Run fails with ai.ErrSchema. A first
// reply that read stands, as it was, when its repair fails for any reason but
// ctx's end, as a page's draft does when writing it again fails. A loose
// reply goes to Check like any other; if Check names nothing, it counts as
// Kind.schema.other and stands unrepaired. No repair is asked for Minor
// violations alone.
func Run[T any](ctx context.Context, p ai.Prompt, t Turn[T], c *Counts) (T, error) {
	unread := Rule(t.Kind + ".json")
	v, reply, err := t.Ask(ctx, p)
	var vs []Violation
	switch {
	case errors.Is(err, ai.ErrSchema):
		vs = []Violation{{Rule: unread, Detail: err.Error()}}
		if d := (*ai.DecodeError)(nil); errors.As(err, &d) {
			vs[0].Detail = d.Error()
		}
	case err != nil && !errors.Is(err, ErrLoose):
		return v, err
	default:
		if vs = t.Check(v); len(vs) == 0 && err != nil {
			c.other1(Rule(t.Kind+".schema.other"), err.Error())
		}
	}
	c.add(first, vs...)
	if slices.ContainsFunc(vs, func(x Violation) bool { return !x.Minor }) {
		note := fmt.Sprintf(jsonNote, vs[0].Detail)
		if vs[0].Rule != unread {
			note = t.Note(vs)
		}
		fix, _, err := t.Ask(ctx, p.After(reply, note))
		failed := err != nil && !errors.Is(err, ErrLoose)
		switch {
		case failed && (vs[0].Rule == unread || ctx.Err() != nil):
			if errors.Is(err, ai.ErrSchema) {
				c.add(repaired, vs...)
			}
			return v, err
		case failed:
			c.add(repaired, vs...) // the first stands, as it was
		default:
			if vs[0].Rule == unread || t.Merge == nil {
				v = fix
			} else {
				v = t.Merge(v, fix, vs)
			}
			if vs = t.Check(v); len(vs) == 0 && err != nil { // a loose repair Check finds nothing in
				c.other1(Rule(t.Kind+".schema.other"), err.Error())
			}
			c.add(repaired, vs...)
		}
	}
	if len(vs) > 0 {
		c.add(residual, vs...)
		t.Fix(&v)
	}
	return v, nil
}

// other1 counts a schema.other, keeping the validator's words on the run's first.
func (c *Counts) other1(r Rule, words string) {
	c.add(first, Violation{Rule: r})
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.other == "" {
		c.other = words
	}
}

// JSON is the Ask of a structured output in f, by model: a reply that reads
// into T but fails f's schema is read leniently (json.Unmarshal ignores keys T
// lacks and leaves the missing ones zero) and returned with ErrLoose.
func JSON[T any](model *ai.OpenAI, f ai.Format[T]) func(context.Context, ai.Prompt) (T, string, error) {
	return func(ctx context.Context, p ai.Prompt) (T, string, error) {
		v, reply, err := ai.StreamJSON(ctx, model, p, f, nil)
		if errors.Is(err, ai.ErrSchema) {
			var loose T
			if json.Unmarshal([]byte(reply), &loose) == nil {
				return loose, reply, fmt.Errorf("%w: %s", ErrLoose, err.Error())
			}
		}
		return v, reply, err
	}
}
