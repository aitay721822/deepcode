package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"deepcode/internal/platform/ai/usage/store"
	"deepcode/internal/platform/storage"
)

// TestRead: each API's usage is read from its own fields: OpenAI's and llama.cpp's cached tokens in
// prompt_tokens_details, DeepSeek's in prompt_cache_hit_tokens, the larger when a server sends both; the
// Responses API's in input_tokens_details; an embedding's input alone. A usage the server left out is 0.
func TestRead(t *testing.T) {
	chat := func(s string) Usage {
		var u openai.CompletionUsage
		if err := json.Unmarshal([]byte(s), &u); err != nil {
			t.Fatal(err)
		}
		return Chat(u)
	}
	for s, want := range map[string]Usage{
		`{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":64}}`:                         {InTokens: 100, OutTokens: 20, CachedTokens: 64},
		`{"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20}`:           {InTokens: 100, OutTokens: 20, CachedTokens: 80},
		`{"prompt_tokens":9,"completion_tokens":1,"prompt_cache_hit_tokens":3,"prompt_tokens_details":{"cached_tokens":5}}`: {InTokens: 9, OutTokens: 1, CachedTokens: 5},
		`{}`: {},
	} {
		if got := chat(s); got != want {
			t.Errorf("%s: %+v, want %+v", s, got, want)
		}
	}
	var r responses.ResponseUsage
	if err := json.Unmarshal([]byte(`{"input_tokens":12,"output_tokens":3,"input_tokens_details":{"cached_tokens":8}}`), &r); err != nil {
		t.Fatal(err)
	}
	if got := Responses(r); got != (Usage{InTokens: 12, OutTokens: 3, CachedTokens: 8}) {
		t.Errorf("responses: %+v", got)
	}
	var e openai.CreateEmbeddingResponseUsage
	if err := json.Unmarshal([]byte(`{"prompt_tokens":4,"total_tokens":4}`), &e); err != nil {
		t.Fatal(err)
	}
	if got := Embedding(e); got != (Usage{InTokens: 4}) {
		t.Errorf("embedding: %+v", got)
	}
	u := Usage{InChars: 1, OutChars: 2, InTokens: 3, OutTokens: 4, CachedTokens: 5}
	if u.Add(u); u != (Usage{InChars: 2, OutChars: 4, InTokens: 6, OutTokens: 8, CachedTokens: 10}) {
		t.Errorf("add: %+v", u)
	}
}

// TestManager: a request's tokens are summed on its repository's row of the day and model, its context's
// cancelling notwithstanding; one no repository asked for counts on repository 0, one with no tokens not at all.
// Totals read the sums back, the largest input first, within the days asked for and the repositories kept,
// a repository with no row (deleted) with no name.
func TestManager(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range []int64{1, 2} {
		if _, err := db.Exec(`INSERT INTO repositories(id, public_id, url, owner, name) VALUES (?, ?, ?, 'o', ?)`,
			id, "p"+string(rune('0'+id)), "https://github.com/o/r"+string(rune('0'+id)), "r"+string(rune('0'+id))); err != nil {
			t.Fatal(err)
		}
	}
	m := &Manager{Store: &store.Store{DB: db}}
	one := WithRepository(ctx, 1)
	cancelled, cancel := context.WithCancel(WithRepository(ctx, 2))
	cancel()
	m.Record(one, "chat", Usage{InChars: 400, InTokens: 100, CachedTokens: 60, OutTokens: 10})
	m.Record(one, "chat", Usage{InTokens: 50, OutTokens: 5})
	m.Record(one, "embed", Usage{InTokens: 7})
	m.Record(ctx, "chat", Usage{InTokens: 3})
	m.Record(cancelled, "chat", Usage{OutTokens: 1})
	m.Record(WithRepository(ctx, 9), "chat", Usage{InTokens: 2})
	m.Record(one, "chat", Usage{InChars: 9}) // no tokens: a refused request
	if _, err := db.Exec(`INSERT INTO llm_usage(repository_id, day, model, calls, input_tokens) VALUES (1, date('now', '-40 days'), 'chat', 1, 1000)`); err != nil {
		t.Fatal(err)
	}
	all := func(int64) bool { return true }
	recent := []store.Total{
		{RepoID: 1, RepoPublicID: "p1", RepoOwner: "o", RepoName: "r1", Model: "chat", Calls: 2, Input: 150, Cached: 60, Output: 15},
		{RepoID: 1, RepoPublicID: "p1", RepoOwner: "o", RepoName: "r1", Model: "embed", Calls: 1, Input: 7},
		{RepoID: 0, Model: "chat", Calls: 1, Input: 3},
		{RepoID: 9, Model: "chat", Calls: 1, Input: 2},
		{RepoID: 2, RepoPublicID: "p2", RepoOwner: "o", RepoName: "r2", Model: "chat", Calls: 1, Output: 1},
	}
	for _, tc := range []struct {
		days int
		keep func(int64) bool
		want []store.Total
	}{
		{30, all, recent},
		{1, all, recent},
		{0, all, append([]store.Total{{RepoID: 1, RepoPublicID: "p1", RepoOwner: "o", RepoName: "r1", Model: "chat", Calls: 3, Input: 1150, Cached: 60, Output: 15}}, recent[1:]...)},
		{30, func(id int64) bool { return id == 1 }, recent[:2]},
	} {
		got, err := m.Totals(ctx, tc.days, tc.keep)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%d days: %+v %v,\nwant %+v", tc.days, got, err, tc.want)
		}
	}
	// The window's edges: n days are today and the n-1 before it.
	for _, ago := range []int{1, 29, 30} {
		if _, err := db.Exec(`INSERT INTO llm_usage(repository_id, day, model, calls, input_tokens) VALUES (2, date('now', ?), 'old', 1, ?)`,
			fmt.Sprintf("-%d days", ago), ago); err != nil {
			t.Fatal(err)
		}
	}
	for days, want := range map[int]int64{1: 0, 2: 1, 29: 1, 30: 30, 31: 60} {
		got, err := m.Totals(ctx, days, func(id int64) bool { return id == 2 })
		var input int64
		for _, r := range got {
			if r.Model == "old" {
				input = r.Input
			}
		}
		if err != nil || input != want {
			t.Errorf("%d days: the old rows' input %d %v, want %d", days, input, err, want)
		}
	}
}
