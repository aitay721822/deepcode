package acptest

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/usage"
)

// Metered keeps what a model meters (a usage.Meter), its time left out.
type Metered struct {
	mu   sync.Mutex
	rows []usage.Usage
}

// Record keeps u.
func (m *Metered) Record(_ context.Context, _ string, u usage.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u.Elapsed = 0 // the Windows clock may say 0 for a fast request
	m.rows = append(m.rows, u)
}

// All is what was metered so far.
func (m *Metered) All() []usage.Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]usage.Usage(nil), m.rows...)
}

// Quarter counts a fourth of a text's bytes as tokens, as the fake does.
func Quarter(s string) int { return len(s) / 4 }

// Echo is what the fake got for p from c, fake:echo added to what is new.
func Echo(t *testing.T, ctx context.Context, c ai.Chat, p ai.Prompt) map[string]any {
	t.Helper()
	p.Dynamic += " fake:echo"
	text, err := ai.Generate(ctx, c, p)
	var got map[string]any
	if err != nil || json.Unmarshal([]byte(text), &got) != nil {
		t.Fatalf("echo: %q, %v", text, err)
	}
	return got
}

// At is the value at path in v, decoded JSON.
func At(v any, path ...string) any {
	for _, k := range path {
		m, _ := v.(map[string]any)
		v = m[k]
	}
	return v
}
