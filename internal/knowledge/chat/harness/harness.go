// Package harness fits one question's model requests to the model's context
// (ai.OpenAI.Context), counted in the model's tokens (Tokens). A request may
// take a threshold of the context: CompactRatio of it, and never the room the
// answer needs (ai.OpenAI.Room). Before an answer is gathered, the room the
// threshold leaves is shared out (Share): the evidence first, then the
// conversation so far, older turns summed up by the model when they do not fit
// (Recall), and the background last. A gathering's request that would pass the
// threshold has its older tool results masked to a line each (Next), and stops
// when it still would. A deep answer's evidence takes all the room the other
// parts leave (Share's rest). A server that refuses a request as too long and
// says by how much is learned from, and the request fitted again and sent once
// more. Code is never summed up by the model: its names and lines are what an
// answer cites. With no context known, every share is the fixed one and
// nothing is masked or summed up.
package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"deepcode/internal/platform/ai"
)

const (
	defaultRatio = 0.8
	minEvidence  = 2500 // tokens of evidence the least answer needs: some 8000 bytes of code
	summaryRoom  = 600  // tokens the conversation keeps for its summary when its turns do not fit
	maxSummary   = 4000 // bytes a summary is stored with, so that rolling it forward stays bounded
	// perMessage is the tokens a chat template wraps a message in, at most
	// (ChatML's are 5); toolsNote, the tokens its tool instructions take,
	// until a reply's usage measured the template.
	perMessage = 10
	toolsNote  = 256
)

// Fit is one question's: its model, the tokenizer its requests are counted
// by, what the server's counts taught it, how many tool results it masked,
// and the tokens it counted for the last request Next sent (0 with no
// context known).
type Fit struct {
	model    *ai.OpenAI
	tokens   *Tokens
	counted  map[string]int // Count's, by text
	overhead int            // the template's tokens in the gathering's last request, as the server counted them (its reply's usage, or its refusal); -1 until it did
	slack    int            // the tokens a refusal said an answer's request took past its count
	Masked   int
	Sent     int
}

// New is a Fit for a question to model, counted by tokens.
func New(model *ai.OpenAI, tokens *Tokens) *Fit {
	return &Fit{model: model, tokens: tokens, counted: map[string]int{}, overhead: -1}
}

// Count is the tokens s takes, each text counted once a question: an
// evidence block as the gathering admits it, then as the answer shows it.
func (f *Fit) Count(s string) int {
	n, ok := f.counted[s]
	if !ok {
		n = f.tokens.Count(s)
		f.counted[s] = n
	}
	return n
}

// Prompt is the tokens p's request takes: its messages, each in the most a
// template wraps one in.
func (f *Fit) Prompt(p ai.Prompt) int {
	size, n := p.Size(f.tokens.Message)
	return size + n*perMessage
}

// Threshold is the tokens a request may take: CompactRatio of the context, but
// never past the context less the answer's room; 0 when no context is known.
func (f *Fit) Threshold() int { return f.threshold(f.model.Context()) }

// threshold is Threshold for a context of window tokens.
func (f *Fit) threshold(window int) int {
	if window == 0 {
		return 0
	}
	return min(int(cmp.Or(f.model.CompactRatio, defaultRatio)*float64(window)), window-f.model.Room())
}

// Context is the model's context in tokens, 0 when unknown.
func (f *Fit) Context() int { return f.model.Context() }

// Room is the tokens of an answer's request that pass the threshold with the
// slack a refusal of one taught, or math.MaxInt when no context is known.
func (f *Fit) Room() int {
	if f.model.Context() == 0 {
		return math.MaxInt
	}
	return f.Threshold() - f.slack
}

// Over reports whether a gathering's request of tokens tokens, its template's
// as the server counted them, would pass the threshold.
func (f *Fit) Over(tokens int) bool { return tokens > f.Threshold() }

// Learn reports whether err refused an answer's request, counted at tokens as
// its parts were fitted (each piece alone, never the request whole), as longer
// than the context, the server saying its tokens and the context's. It then
// keeps the context for the model, and the tokens the server counted past ours
// as slack, which Room leaves out from then on: no reply measures an answer's
// template, as one does a gathering's (Next).
func (f *Fit) Learn(err error, tokens int) bool {
	if tokens <= 0 {
		return false
	}
	prompt, ok := f.refused(err)
	if ok {
		f.slack = max(f.slack, prompt-tokens)
	}
	return ok
}

// refused is the tokens the server counted in a request err refused as longer
// than the context, which it then keeps for the model (ai.OpenAI.Learn); false
// when err does not say both.
func (f *Fit) refused(err error) (int, bool) {
	var e *ai.ErrContext
	if !errors.As(err, &e) || e.Context <= 0 || e.Prompt <= 0 {
		return 0, false
	}
	f.model.Learn(e.Context)
	return e.Prompt, true
}

// Shares are the tokens an answer's request gives each of its parts.
type Shares struct{ Evidence, History, Background int }

// ErrSmall: the model's context cannot hold a question's request with the
// least evidence an answer needs; Need is a context, in tokens, that can.
type ErrSmall struct{ Need int }

func (e *ErrSmall) Error() string {
	return fmt.Sprintf("模型的上下文太小，放不下這個問題需要的程式碼：模型伺服器的 context 與 DEEPCODE_CHAT_CONTEXT 都要至少 %d tokens，或調低 DEEPCODE_MAX_OUTPUT_TOKENS（留給回答的 tokens 也在 context 內）", e.Need)
}

// Share divides the room the threshold leaves past fixed, the tokens of the
// request's own lines (its instructions, question and closing lines), among
// want's parts, each getting at most what it wants. The evidence comes first,
// but the conversation keeps summaryRoom for its summary while the evidence
// gets more than minEvidence; then the conversation; the background gets what
// is left. With rest, a deep answer's, the evidence also takes all the room the
// conversation and the background leave when they get what they want: never
// less than without. With no context known, each part gets what it wants.
func (f *Fit) Share(fixed int, want Shares, rest bool) (Shares, error) {
	if f.model.Context() == 0 {
		return want, nil
	}
	room := f.Room() - fixed
	if f.Threshold() <= 0 || room < minEvidence {
		return Shares{}, &ErrSmall{Need: f.need(fixed)}
	}
	evidence := min(want.Evidence, max(room-min(want.History, summaryRoom), minEvidence))
	if rest {
		evidence = max(evidence, room-want.History-want.Background)
	}
	history := min(want.History, room-evidence)
	return Shares{Evidence: evidence, History: history, Background: min(want.Background, room-evidence-history)}, nil
}

// need is the least context, in tokens, whose threshold holds fixed, the
// slack and minEvidence: the threshold grows with the context, so a binary
// search finds it.
func (f *Fit) need(fixed int) int {
	return sort.Search(math.MaxInt32, func(n int) bool { return f.threshold(n)-f.slack-fixed >= minEvidence })
}

// ErrFull: a gathering's request would not fit the model's context, its older
// results masked, or the server refused it as too long.
var ErrFull = errors.New("the request does not fit the model's context")

// Next asks ex for the model's next reply. With a context known, a request
// that would pass the threshold first has the results masks gives masked
// (ai.Exchange.Compact): those before the last reply's; if it still would, it
// is not sent and the error is ErrFull. A refusal as too long that says by how
// much measures the template as a reply's usage does: the server's count of
// the request less its pieces'; the context is learned, and the request fitted
// again and sent once more. Another refusal, or a second, is ErrFull.
func (f *Fit) Next(ctx context.Context, ex *ai.Exchange, masks func() map[int]string) (string, []ai.ToolCall, error) {
	for retried := false; ; retried = true {
		if f.model.Context() > 0 {
			if f.Over(f.size(ex)) {
				if f.Masked += ex.Compact(masks()); f.Over(f.size(ex)) {
					return "", nil, ErrFull
				}
			}
			f.Sent = f.size(ex)
		}
		text, calls, err := ex.Next(ctx)
		if err == nil {
			if f.model.Context() > 0 {
				f.anchor(ex)
			}
			return text, calls, nil
		}
		if !errors.As(err, new(*ai.ErrContext)) {
			return "", nil, err
		}
		prompt, ok := f.refused(err)
		if retried || !ok {
			return "", nil, fmt.Errorf("%w: %w", ErrFull, err)
		}
		next, _ := ex.Size(f.tokens.Message) // a refused request added nothing to ex
		f.overhead = max(prompt-next-perMessage*ex.Added(), 0)
	}
}

// size is the tokens ex's next request takes: its pieces, each counted as a
// message's text (Tokens.Message), and the template's tokens as the server
// last counted them, with perMessage for each piece since; before the server
// counted one, perMessage for each and toolsNote.
func (f *Fit) size(ex *ai.Exchange) int {
	next, _ := ex.Size(f.tokens.Message)
	if f.overhead < 0 {
		return next + perMessage*ex.Added() + toolsNote
	}
	return next + f.overhead + perMessage*ex.Added()
}

// anchor takes the template's tokens from the reply ex just had: those the
// server counted in its request less the pieces it sent.
func (f *Fit) anchor(ex *ai.Exchange) {
	if in := ex.Used().InTokens; in > 0 {
		_, sent := ex.Size(f.tokens.Message)
		f.overhead = max(in-sent, 0)
	}
}

// Turn is a message of the conversation so far, with its id, which orders them.
type Turn struct {
	ID int64
	ai.Message
}

// Summary is the conversation up to message Through, as the model summed it up.
type Summary struct {
	Through int64
	Text    string
}

// Recalled is the conversation so far as fitted to its share (Recall): the
// turns kept, as messages; Lead, the summary of those before them when no turn
// is kept, to head the request's new message, so that no two user messages
// follow each other; Fresh, a summary the model wrote, to store for the next
// question; and Summed, the turns the summary stands for.
type Recalled struct {
	Kept   []ai.Message
	Lead   string
	Fresh  *Summary
	Summed int
}

// History is the tokens turns take as messages.
func (f *Fit) History(turns []Turn) (n int) {
	for _, t := range turns {
		n += f.turn(t)
	}
	return n
}

// turn is the tokens t takes as a message, in the most a template wraps one in.
func (f *Fit) turn(t Turn) int { return f.tokens.Message(t.Content) + perMessage }

// Recall fits turns, the conversation so far oldest first, to share tokens:
// the newest turns that fit whole, after a summary of those before them, at
// the head of the first turn kept when that is the user's, else in a message
// of its own, or as Lead when none is kept. The summary is the stored one load
// gives when it covers them; else the model sums them up from it, a request
// sent before the answer's. Turns that all fit come back as they are, and
// nothing is loaded or asked; with no room, none come back. A summary that
// fails leaves the turns kept, and the error.
func (f *Fit) Recall(ctx context.Context, turns []Turn, share int, load func(context.Context) (Summary, error)) (Recalled, error) {
	if f.History(turns) <= share {
		return Recalled{Kept: Messages(turns)}, nil
	}
	if share <= 0 {
		return Recalled{}, nil
	}
	k, used := len(turns), 0 // the turns kept are turns[k:]
	for room := share - min(share, summaryRoom); k > 0 && used+f.turn(turns[k-1]) <= room; k-- {
		used += f.turn(turns[k-1])
	}
	r := Recalled{Kept: Messages(turns[k:])}
	sum, err := load(ctx)
	if err != nil {
		return r, err
	}
	if sum.Through < turns[k-1].ID {
		var since []ai.Message
		for _, t := range turns[:k] {
			if t.ID > sum.Through {
				since = append(since, t.Message)
			}
		}
		if sum.Text, err = f.summarize(ctx, sum.Text, since); err != nil {
			return r, err
		}
		sum.Through, r.Fresh = turns[k-1].ID, &sum
	}
	text := f.cut("Earlier in this conversation, summed up: "+sum.Text, share-used-perMessage) // in a message of its own, or joined to one
	switch {
	case len(r.Kept) == 0:
		r.Lead = text + "\n\n"
	case r.Kept[0].Role == "user":
		r.Kept[0].Content = text + "\n\n" + r.Kept[0].Content
	default:
		r.Kept = append([]ai.Message{{Role: "user", Content: text}}, r.Kept...)
	}
	r.Summed = k
	return r, nil
}

// cut is s, or its longest head that with "…" after it takes at most n
// tokens; "…" alone when none does.
func (f *Fit) cut(s string, n int) string {
	if f.tokens.Count(s) <= n {
		return s
	}
	lo, hi := 0, len(s)
	for lo < hi { // ponytail: a head's tokens grow almost with its length; a head found is checked, not proven the longest
		if mid := (lo + hi + 1) / 2; f.tokens.Count(strings.ToValidUTF8(s[:mid], "")+"…") <= n {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return strings.ToValidUTF8(s[:lo], "") + "…"
}

// Messages are turns' messages, in their order.
func Messages(turns []Turn) []ai.Message {
	out := make([]ai.Message, len(turns))
	for i, t := range turns {
		out[i] = t.Message
	}
	return out
}

// summed is what the model writes of a conversation: the structured output a
// summary is asked in.
type summed struct {
	Topics []string `json:"topics" jsonschema_description:"what was asked and answered, one line each"`
	Names  []string `json:"names" jsonschema_description:"every function, type, file, setting and command named, exactly as written"`
	Open   []string `json:"open" jsonschema_description:"the questions not yet answered"`
}

var summedFormat = ai.Schema[summed]("conversation_summary")

const summaryPrompt = `You sum up the earlier part of a conversation about a repository's code, so that later questions can refer back to it. Keep every function, type, file, setting and command it named exactly as written. Reply with the JSON object only.`

// summarize asks the model to sum up prior, a summary so far ("" for none),
// with messages, each cut to fit the threshold. The names come first, so that
// a summary cut to its share or to maxSummary loses topics before them.
func (f *Fit) summarize(ctx context.Context, prior string, messages []ai.Message) (string, error) {
	p := ai.Prompt{Static: summaryPrompt}
	room := f.Room() - f.tokens.Count(p.JSON().Static) - f.tokens.Count(prior) - 300 // the request's own lines, and the schema's
	each := room / max(len(messages), 1)
	if each < 60 {
		return "", errors.New("the model's context has no room to sum up the conversation")
	}
	var b strings.Builder
	if prior != "" {
		fmt.Fprintf(&b, "The summary so far:\n%s\n\n", prior)
	}
	b.WriteString("The messages to add to it:\n")
	for _, m := range messages {
		fmt.Fprintf(&b, "\n%s: %s\n", m.Role, f.cut(m.Content, each))
	}
	p.Dynamic = b.String()
	s, _, err := ai.StreamJSON(ctx, f.model, p, summedFormat, nil)
	if err != nil {
		return "", err
	}
	text := fmt.Sprintf("Named: %s. Topics: %s. Open: %s.", cmp.Or(strings.Join(s.Names, ", "), "none"), strings.Join(s.Topics, "; "), cmp.Or(strings.Join(s.Open, "; "), "none"))
	if len(text) > maxSummary {
		text = strings.ToValidUTF8(text[:maxSummary-len("…")], "") + "…"
	}
	return text, nil
}
