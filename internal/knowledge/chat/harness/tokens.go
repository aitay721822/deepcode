package harness

import (
	"fmt"
	"os"
	"strings"
	"sync"

	tensai "github.com/mattn/tensai/tokenizer"
	"github.com/tiktoken-go/tokenizer/codec"
)

// reparse is the text counted, in bytes, after which a tokenizer.json is
// parsed again, dropping the word cache tensai's tokenizer grows without end.
const reparse = 64 << 20

// Tokens counts text in a model's tokens: by the model's own tokenizer.json
// (DEEPCODE_CHAT_TOKENIZER) when one is set, else by OpenAI's o200k_base,
// built in. A tokenizer.json is byte-level BPE as Qwen, Llama 3 and GPT-4o
// ship it; a Qwen3.8 one counted 3182 of 3182 code chunks and answers as the
// model server does. Counting is serial: tensai's tokenizer is not safe for
// concurrent use.
type Tokens struct {
	Name    string // the tokenizer.json's path, "o200k_base", or Counting's name
	mu      sync.Mutex
	raw     []byte // the tokenizer.json, parsed again every reparse bytes counted
	bpe     *tensai.Tokenizer
	count   func(string) int // with no tokenizer.json: o200k_base's count, or Counting's
	counted int
}

// Counting is Tokens counted by count.
func Counting(name string, count func(string) int) *Tokens {
	return &Tokens{Name: name, count: count}
}

// LoadTokens is the tokenizer.json at path, or o200k_base when path is "".
func LoadTokens(path string) (*Tokens, error) {
	if path == "" {
		o200k := codec.NewO200kBase() // its vocabulary alone: tokenizer.Get links all of them
		return Counting("o200k_base", func(s string) int { n, _ := o200k.Count(s); return n }), nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	bpe, err := tensai.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &Tokens{Name: path, raw: raw, bpe: bpe}, nil
}

// Count is the tokens s takes.
func (t *Tokens) Count(s string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.bpe == nil {
		return t.count(s)
	}
	if t.counted += len(s); t.counted > reparse { // ponytail: a fresh parse (0.2 s) bounds the cache; a bounded cache of our own if counts slow
		t.bpe, _ = tensai.Parse(t.raw) // it parsed once already
		t.counted = len(s)
	}
	return len(t.bpe.Encode(s))
}

// Message is the tokens s takes as a message's whole text: chat templates
// trim it (Qwen3.8's does).
func (t *Tokens) Message(s string) int { return t.Count(strings.TrimSpace(s)) }
