package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// o200k counts the tests' requests: OpenAI's o200k_base, the built-in.
var o200k = func() *Tokens {
	t, err := LoadTokens("")
	if err != nil {
		panic(err)
	}
	return t
}()

// TestTokens: with no tokenizer.json the count is o200k_base's; one is
// counted by its BPE (a's and b's merged, a space joined to a word), parsed
// again past reparse bytes counted, a message's text trimmed as chat
// templates trim it; a file that is missing, or that tensai cannot read, is
// an error naming it.
func TestTokens(t *testing.T) {
	if o200k.Name != "o200k_base" || o200k.Count("hello world") != 2 || o200k.Message(" hello world\n\n") != 2 || o200k.Count(" hello world\n\n") == 2 {
		t.Errorf("%s: %d, as a message %d", o200k.Name, o200k.Count("hello world"), o200k.Message(" hello world\n\n"))
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tokenizer.json")
	os.WriteFile(path, []byte(`{"model":{"type":"BPE","vocab":{"a":0,"b":1,"ab":2,"Ġ":3,"Ġab":4},"merges":["a b","Ġ ab"]},`+
		`"pre_tokenizer":{"type":"ByteLevel","add_prefix_space":false,"use_regex":true},"added_tokens":[]}`), 0o600)
	bpe, err := LoadTokens(path)
	if err != nil || bpe.Name != path || bpe.Count("ab ab ab") != 3 || bpe.Count("aab") != 2 || bpe.Message("ab ab\n\n") != 2 {
		t.Fatalf("%v: %d %d", err, bpe.Count("ab ab ab"), bpe.Count("aab"))
	}
	parsed := bpe.bpe
	if bpe.counted = reparse; bpe.Count("ab ab") != 2 || bpe.counted != len("ab ab") || bpe.bpe == parsed {
		t.Errorf("reparsed: counted %d, parsed again %v", bpe.counted, bpe.bpe != parsed)
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"normalizer":{"type":"NFKC"},"model":{"type":"BPE","vocab":{"a":0},"merges":[]},"pre_tokenizer":{"type":"ByteLevel"}}`), 0o600)
	for _, p := range []string{filepath.Join(dir, "none.json"), bad} {
		if _, err := LoadTokens(p); err == nil || !strings.Contains(err.Error(), filepath.Base(p)) {
			t.Errorf("%s: %v", p, err)
		}
	}
}
