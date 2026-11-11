package score

import (
	"context"
	"errors"
	"strings"

	"deepcode/internal/platform/ai"
)

// Verdict is the judge's reading of a text.
type Verdict struct {
	SelfTalk bool   `json:"selfTalk" jsonschema_description:"whether any part of the answer is addressed to the writer itself or to its instructions"`
	Sentence string `json:"sentence" jsonschema_description:"the first sentence that is, copied exactly, or an empty string"`
	Language string `json:"language" jsonschema_description:"the language most of the answer's prose is written in, by its English name"`
}

var verdictFormat = ai.Schema[Verdict]("verdict")

// Rubric is the judge's, one for every language: the bench's measure of
// self-talk, which the product no longer checks for.
const Rubric = `You check an answer a code assistant wrote to a user's question about a repository. The same rules hold whatever language the answer is written in.

selfTalk is true when any part of the answer is addressed to the writer itself or to its instructions instead of to the user:
- thinking aloud, planning or checking its own work in the text ("Wait", "Let me check", "Actually, I", "I need to look at", "Hmm", or the same in any language);
- framing the answer by the material it was given: saying anywhere, at its start, its end or between, that what it writes is based on, drawn from or limited to the sources, code, snippets or evidence it was provided, such as "Based on the provided source code, here is how …", "According to the sources provided, …", "Here is the flow based on the provided sources:", 「提供されたソースコードに基づき、…について回答します。」, "Anhand der bereitgestellten Quellen …", or the same meaning in any other words or language. The user provided no sources: the answer is to speak about the repository itself;
- talk about its prompt or rules, or about that material as such: how the sources were chosen, numbered or cut ("the sources are truncated", "source 3 shows").
Only these are addressed to the user and never self-talk, whatever the language:
- a plain statement of what is missing: that the code it can see does not contain, show or confirm something the answer needs, such as "X is not in the provided sources" (不在提供的來源中), "This can't be confirmed from the current code" (無法從目前程式碼確認), or that no indexed code matched the question, or the same said in any other words or language. Such a sentence names what is missing; one that presents the rest of the answer as drawn from the given material is framing, above;
- code blocks, citations such as [path:1-9] and quoted strings.
A last line saying the answer cites no indexed source is added by the system: ignore it.

sentence is the first sentence that is self-talk, copied exactly, or an empty string.
language is the language the answer is written in, read from the answer alone: the language of most of its prose, code, identifiers and quotes aside, by its English name: English, Chinese, Japanese, German, ….`

// JudgeError is the judge's measured error against blind hand labels of 331
// answers: its counts are advisory, the hand labels gate.
const JudgeError = "15 extras on 268 clean answers (2 borderline), 1 borderline miss of 63 positives, 0 failures, language 330 of 331 (an English answer named German)"

// Judge asks model for its verdict on answer, which it reads alone: the
// question is not sent, so the language it names is the answer's own, never
// the question's. A verdict naming no language is an error.
func Judge(ctx context.Context, model *ai.OpenAI, answer string) (Verdict, error) {
	answer = strings.ReplaceAll(answer, "</answer", "<\\/answer") // the answer cannot end its block early
	v, _, err := ai.StreamJSON(ctx, model, ai.Prompt{Static: Rubric, Dynamic: "<answer>\n" + answer + "\n</answer>"}, verdictFormat, nil)
	if err == nil && strings.TrimSpace(v.Language) == "" {
		err = errors.New("the judge named no language")
	}
	return v, err
}

// Speaks reports whether language, as the judge names it ("Traditional
// Chinese"), is want, what it adds in brackets aside.
func Speaks(language, want string) bool {
	language, _, _ = strings.Cut(language, "(")
	return strings.Contains(strings.ToLower(language), strings.ToLower(want))
}
