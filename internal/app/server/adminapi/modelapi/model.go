package modelapi

import (
	"time"

	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/acp"
)

// model is the chat model as the admin console shows it: its provider, acp
// or openai ("" when none is set), its name, its context in tokens (0 while
// unknown), the tokens its backend adds to each request and how much larger
// its tokens are than the local tokenizer's (ai.Window), and, for an agent,
// what it is.
type model struct {
	Provider string  `json:"provider"`
	Name     string  `json:"name"`
	Context  int     `json:"context"`
	Overhead int     `json:"overhead"`
	Scale    float64 `json:"scale"`
	Agent    *agent  `json:"agent,omitempty"`
}

// agent is what an ACP agent said it is (acp.Status): its name and version
// ("" before it starts), the value of its model option, and the last failure
// since it last answered, with its time in UTC, as SQLite writes one.
type agent struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Model   string `json:"model"`
	Trouble string `json:"trouble"`
	At      string `json:"at"`
}

// describe is chat as the admin console shows it.
func describe(chat ai.Chat) model {
	if chat == nil {
		return model{}
	}
	w := chat.Window()
	m := model{Provider: "openai", Name: chat.Name(), Context: w.Context, Overhead: w.Overhead, Scale: w.Scale}
	if a, ok := chat.(*acp.Agent); ok {
		s := a.Status()
		m.Provider, m.Agent = "acp", &agent{Name: s.Name, Version: s.Version, Model: s.Model, Trouble: s.Trouble}
		if !s.At.IsZero() {
			m.Agent.At = s.At.UTC().Format(time.DateTime)
		}
	}
	return m
}
