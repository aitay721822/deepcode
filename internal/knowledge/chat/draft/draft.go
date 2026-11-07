// Package draft reads an answer the model has written before it is kept:
// whether it thinks aloud, says code it needs was not shown, and which code
// it names. The chat service writes it again, once, when it does.
package draft

import (
	"regexp"
	"slices"
	"strings"
)

// Reading is what a draft shows of itself.
type Reading struct {
	SelfTalk bool     // it thinks aloud, rather than answers
	Gap      string   // the sentences saying code it needs was not shown, "" for none
	Names    []string // the identifiers in its inline code, each once
}

const (
	maxGap   = 600 // bytes of Gap
	maxNames = 32
)

// The pieces of gapRe in English. A complete answer talks about the code, not
// about what it was given, so a name for the sources that can mean nothing
// else (refEn: "the provided sources", "the snippets given"; not "user-provided
// sources", "the SDK's provided snippets" nor "the retrieved sources" of a
// search engine) makes a gap of a negative claim (negEn) that reaches it in
// its clause (clause) through "in", "missing from", "not covered by"… ("no
// handler was found in the provided sources"), of the name opening a clause
// (refLead) and saying what it does not hold (refVerb, hold: "the provided
// sources do not show…", "…say nothing about…"), and of a claim the name
// opens with a comma ("Based on the provided sources, this can't be…"). Names
// that can mean more ("the provided code", "these sources") count only in the
// prescribed forms below. Each name ends its clause (end), so "the sources
// directory" and "the provided code path" are none.
//   - texts: what the sources hold; nouns: those and the bare words "code"
//     and "codebase", as "the provided code" may also be an authorization code.
//   - given: the provided ones (not "the snippet" or "the code shown", which
//     may be the answer's own); provided: given, or "these snippets", after an
//     article and before end.
//   - sources: "the sources" as such, counted only after shownIn or "can't be
//     found/seen in" and not as a bare "in", as "not in these sources" may be
//     CORS origins.
//   - current: what a whole answer can't be confirmed from (confirm, with
//     an adverb, surely), or what "I can't confirm…" (up to eight words after
//     the verb, word; not "we", the voice of a project's docs) can't from: as the
//     prompt words it, "the current code", or the provided sources. "Not in
//     the current code" is how an answer says code was removed, and "it is
//     determined from the current codebase alone" that a build sets it, so
//     "alone" counts only after a verb of confirming (alone).
const (
	texts = `(?:source (?:code(?: files)?|files|snippets)|sources|snippets?|excerpts?)`
	nouns = `(?:` + texts + `|code(?: ?base)?)`
	given = `(?:currently |actually )?(?:provided|given|supplied) (?:[\w.-]+ )?` + nouns +
		`|(?:sources|snippets?|excerpts?|source code) (?:currently )?(?:provided|given|supplied)(?: here)?`
	end = `(?: here| at all)?\s*(?:$|[,;:()\[\]|—–…。，（）` + "`" + `]|\s-{1,2}\s|[.!?][)）\]]*(?:\s|$|[\[|])|` +
		`(?:and|but|so|or|because|since|as|while|which|whether|that|if|when|where|though|although|whereas|therefore|thus|hence|yet|nor|then|for|to)\b)`
	provided = `(?:any of )?(?:(?:the |these |those )?(?:` + given + `)|(?:these|those) (?:specific |particular )?(?:snippets|excerpts))` + end
	sources  = `(?:the |these |those )(?:specific |particular )?sources` + end
	current  = `(?:the |these |those )?(?:(?:currently )?current (?:[\w.-]+ )?` + nouns + `(?: provided)?` +
		`|currently (?:provided|given|supplied|available) (?:[\w.-]+ )?` + nouns +
		`|(?:provided|given|supplied) (?:[\w.-]+ )?` + texts +
		`|(?:sources|snippets?|excerpts?) (?:currently )?(?:provided|given|supplied)(?: here)?` +
		`|(?:source )?code currently (?:provided|given|supplied))` + end
	shownIn = `(?:visible|shown|included|provided|present|available|detailed|covered|described|found|contained|` +
		`appears?|exists?|occurs?) (?:in|among|within|from) `
	confirm = `(?:confirmed|verified|determined|known|checked|established|inferred|validated|ascertained|concluded)(?: with certainty)? ` +
		`(?:from|in|with|using|based on|on|by|through|within|against|given|on the basis of) `
	surely = `(?:fully|completely|entirely|positively|actually|directly|definitively|reliably|conclusively|certainly|really|currently|clearly)`
	alone  = `(?:the )?(?:current (?:code(?: ?base)?|source code)|provided sources) (?:alone|only|on its own|by itself)\b`
	// word is one word of what "I can't confirm" names: a code span, a
	// citation or an aside counts as one, a comma, semicolon or colon ends them.
	word  = "(?:`[^`]*`|\\([^)]*\\)|\\[[^\\]]*\\]|[^\\s,;:()\\[\\]`])+"
	refEn = `(?:the |these |those |any of the |any )?(?:(?:currently )?(?:provided|given|supplied) (?:(?:above|following|available|relevant) )?` +
		`(?:source (?:code(?: files)?|files|snippets?)|code snippets?|sources|snippets?|excerpts?)|` +
		`(?:sources|snippets|excerpts|source code|source files|code snippets) (?:currently )?(?:provided|given|supplied)(?: to me| here)?)`
	negEn = `(?:\b(?:not|no|none|nothing|never|neither|nor|without|cannot|missing|absent|lacks?|lacking|omits?|omitted|` +
		`unable|nowhere|unknown|unclear|unspecified|undocumented|excluded)\b|n['’]t\b)`
	clause  = `[^,;:\n]*?`
	hold    = `(?:show|include|contain|cover|mention|have|has|provide|define|specify|reveal|describe|document|give|offer|say|hold|explain|discuss|indicate|reference|list|display|expose|address)`
	refLead = `(?:^\s*|[^\w\s'’-]\s*|-\s+|\b(?:and|but|so|yet|however|because|since|as|while|though|although|whereas|unfortunately|that|if|when|` +
		`thus|also|additionally|moreover|therefore|hence|otherwise|sadly|note|seems|appears|think|then)\s+)`
	refVerb = `\s+(?:(?:only|just) ` + hold + `|` + hold + `s? only|lacks?|omits?|fails? to ` + hold + `|never ` + hold + `s?|` +
		`(?:do|does|did)(?: not|n['’]t)(?: \w+ly| themselves)?(?: (?:appear|seem) to)? ` + hold + `|` +
		hold + `s? (?:no|nothing|none)|(?:are|is) (?:silent|incomplete|partial|insufficient)|(?:are|is)(?: not|n['’]t) (?:sufficient|complete|enough))\b`
)

// The pieces of gapRe in Chinese. 提供 alone is anything an API, a module or
// a user provides, 程式碼 as such is the code of the repository (「你提供的程式碼
// 無法編譯」 reviews it), 上述 is the answer's own code block and 給定 is
// mathematical, so the sources are named so nothing else can be meant (cnRef):
// (你/這些/目前…)提供的來源/片段 (cnQual, cnText), opening a clause (cnLead:
// not after a noun, as in 「使用者提供的來源」). As in English, that is a
// gap with 不/未/沒有 leading into them, then 中 (「未被包含在提供的來源中」;
// not 「不要在…」 or 「不過在…」), or after them: any negation (cnNeg) after
// 中, or one saying what they do not hold (cnHold: 「提供的來源沒有…」). Other
// names count only in the prescribed forms ("不在提供的程式碼中").
//   - cnGiven: the ones provided; cnNoun: what they hold, followed by 中 or 裡
//     (cnIn).
//   - cnCode: the current code, as the prompt names it; cnFrom: what a claim
//     can't be made from (the current code, or the provided sources); cnCant,
//     cnVerb: what can't be done from them, closing its clause (cnClose) where
//     the code may be the subject ("目前的程式碼無法確認使用者" is a behaviour).
const (
	cnQual  = `(?:你|您|本次|這次|上述|這些|任何|所有|全部|目前|當前|現有|所|已)*`
	cnGiven = cnQual + `(?:提供|給定?)(?:之|的)`
	cnNoun  = `(?:程式碼(?:庫|片段|檔案)?|原始碼(?:檔案)?|源碼|來源(?:檔案|片段|程式碼)?|片段|檔案)`
	cnText  = `(?:程式碼片段|來源(?:檔案)?|片段)`
	cnIn    = `(?:之?中|當中|之內|裡|裏|內)`
	cnLead  = `(?:^\s*|[^\p{L}\p{N}\s)\]` + "`" + `]\s*|[在但而從於且並如若據依按]|因為|所以|但是|只是|不過|另外|同時|可是|此外|因此|然而|其中|` +
		`畢竟|雖然|儘管|即使|既然|如果|事實上|實際上|看起來|似乎|整體而言|注意|的是|這裡|可惜)`
	cnRef   = cnLead + cnQual + `提供(?:之|的)` + cnText + `(?:範圍)?`
	cnNeg   = `(?:不|未|沒|無|缺|找不到|看不到)`
	cnHold  = `(?:沒有|並無|缺少|缺乏|找不到|看不到|不足以|不含|不完整|不足|只有|僅有|無[^法效需須誤關論]|(?:未|不|只|僅)(?:包含|涵蓋|顯示|出現|提及|提到|說明|展示|記載|定義|列出|含有))`
	cnCode  = `(?:目前|當前|現有)的?(?:程式碼(?:庫|片段)?|原始碼)`
	cnCant  = `(?:無法|難以|沒辦法|沒法|不能|很難|不可能|不足以)`
	cnVerb  = `(?:確認|得知|判斷|確定|驗證|證實|查證|推斷|推論|得出結論|看出|釐清)`
	cnClose = `(?:這點|此點|這一點)?\s*(?:\[[^\]]*\])?(?:$|[。，；：！？,;:.!?)）(（]|是否)`
	cnFrom  = `(?:(?:目前|當前|現有)的?(?:程式碼(?:庫|片段)?|原始碼)|` +
		`(?:你|您|這些|本次|這次|上述|目前|當前|現有)` + cnQual + `提供(?:之|的)?(?:程式碼(?:庫|片段)?|原始碼|來源|片段)|` +
		`(?:所|已)?提供(?:之|的)?` + cnText + `)`
)

var (
	// talkRe matches a sentence thinking aloud: checking its own work or the
	// sources' completeness, in the text. One may be style; two are a habit.
	talkRe = regexp.MustCompile(`(?im)(?:^|[.!?:]\s+|\*\*)(?:wait\b|hmm+\b|ah,|actually,|okay,? so\b|let me\b|let's (?:check|look|re-?examine|verify|assume|see)\b|` +
		`i (?:will|must|should|need to) (?:check|stick|provide|present|advise|look|verify)\b|i'll (?:stick|provide|present|advise|check|look)\b|` +
		`(?:let me )?double-check\b|final check\b|answer structure\b|did i miss\b)|the prompt says|等等|讓我(?:看看|檢查|確認|再)|我再(?:看|檢查|確認)`)
	// gapRe matches a sentence saying code the answer needs was not among the
	// sources. It is the system prompt's contract: "not in the provided sources"
	// (不在提供的來源中) or, for a whole answer, "can't be confirmed from the
	// current code" (無法從目前程式碼確認; also "I can't confirm this from the
	// current code", though not "we can't…"), and the ways models vary them. The
	// sources are named as the provided ones, or as the snippets, excerpts or
	// sources where the clause ends: "if the token is not provided, the code
	// returns 401", "not in the sources directory", "not in the source files",
	// 「若請求不在這些來源中」 and 「編譯器無法從程式碼確認型別」 are no gap.
	gapRe = regexp.MustCompile(`(?i)` +
		negEn + clause + `\b(?:in|among|within|inside) ` + refEn + end + `|` +
		`(?:\b(?:missing|absent|excluded|omitted)|(?:\bnot|n['’]t) (?:\w+ly )?(?:available|visible|shown|found|present|covered|described|documented|mentioned))` +
		`(?: anywhere| at all)? (?:from|by) ` + refEn + end + `|` +
		refLead + refEn + refVerb + `|\b(?:none of|nothing in|there(?:['’]s| is) nothing in) ` + refEn + `|` +
		`\b(?:in|within|among|from|based on|according to|given|using) ` + refEn + `\s*,` + clause + negEn + `|` +
		`\b(?:from|based on|according to|given|with|using|in) (?:the )?current (?:code(?:base)?|source code)\s*,` + clause +
		`(?:can['’]t|cannot|can not|could not|couldn['’]t) be (?:\w+ )?(?:confirmed|verified|determined|established|known)|` +
		`\b(?:can['’]t|cannot|can not|could not|couldn['’]t) be (?:\w+ )?(?:confirmed|verified|established) (?:from|with|using|based on) ` + alone + `|` +
		`\bi(?: am| was|['’]m)? (?:(?:simply|just|honestly|really|still|truly) )?` +
		`(?:can['’]t|cannot|can not|could not|couldn['’]t|(?:unable|not able|wasn['’]t able|haven['’]t been able) to)(?: ` + surely + `)? ` +
		`(?:confirm|verify|determine|establish|tell|say) (?:` + word + ` ){0,8}?(?:from|with|using|based on) (?:` + alone + `|` + current + `)|` +
		`(?:並)?(?:不|未|沒有?)(?:被)?(?:包含|列|顯示|出現|存在|記載|提及|定義|涵蓋|找到|看到)?` + cnRef + cnIn + `|` + cnRef + `(?:` + cnIn + `[，,]?[\s並都也仍]*` + cnNeg + `|[，,]?[\s並都也仍]*` + cnHold + `)|` +
		`(?:\bnot|n['’]t) (?:(?:currently|actually|fully|explicitly|directly|really|here) )?(?:` + shownIn + `(?:` + provided + `|` + sources + `)|` +
		`(?:in|among|within|from|part of) ` + provided + `)|\b(?:missing|absent) (?:from|in) ` + provided + `|` +
		"\\bno (?:[\\w@`.-]+ ){1,6}(?:(?:is|are) " + shownIn + `|appears? (?:in|among|within) )(?:` + provided + `|` + sources + `)|` +
		`\bthere(?:['’]s| is| are) no (?:\S+ ){1,6}(?:in|among|within) ` + provided + `|` +
		`\b(?:can['’]t|cannot|can not|could not|couldn['’]t) be (?:found|located|seen) (?:in|among|within) (?:` + provided + `|` + sources + `)|` +
		`\b(?:can['’]t|cannot|can not|could not|couldn['’]t)(?: actually| really| currently)? be ` +
		`(?:` + surely + ` )?` + confirm + current + `|` +
		`(?:不|未|沒有)(?:在|存在於|包含在|出現在|顯示在|列在|記載於|提及於)` + cnGiven + cnNoun + cnIn + `|(?:不|未|沒有)在這些片段` + cnIn + `|` +
		`在` + cnGiven + cnNoun + cnIn + `並?(?:不存在|缺少|缺失|找不到|沒有出現|未出現)|` +
		cnCant + `(?:完全|直接|明確|單|僅|只|單獨)?(?:[從由在依以靠憑]|根據|依據|透過|藉由)` + cnFrom + cnIn + `?來?(?:完全|直接|明確)?` + cnVerb + `|` +
		`目前` + cnCant + `[從由](?:程式碼|原始碼)` + cnIn + `?來?` + cnVerb + `|` + // as prescribed, the claim may go on
		cnCode + cnIn + `?並?` + cnCant + `(?:完全|直接|明確)?` + cnVerb + cnClose + `|` +
		`(?:根據|依據|依|以|基於|憑|就|從|在)(?:` + cnCode + `|` + cnGiven + cnNoun + `)(?:來看|而言|` + cnIn + `)?[，,]?\s*(?:我們?)?(?:這點|此點|此處)?(?:並|也|仍)?` +
		cnCant + `(?:完全|直接|明確)?` + cnVerb + cnClose)
	// plain drops the emphasis and quote marks a sentence wraps its words in;
	// it is for matching only, so snake_case joined up does no harm.
	plain      = strings.NewReplacer("*", "", "_", "", "「", "", "」", "", "『", "", "』", "", "“", "", "”", "", `"`, "", "\u00a0", " ")
	sentenceRe = regexp.MustCompile(`(?:[^.!?。？！\n]|[.!?][^\s.!?。？！])+[.!?。？！]?`) // a stop inside a word, as in config.go, ends nothing
	fenceRe    = regexp.MustCompile("(?s)```.*?(?:```|$)")
	inlineRe   = regexp.MustCompile("`([^`\n]+)`")
	identRe    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
)

// Read reads a draft; its code blocks, which may hold any words, count for nothing.
func Read(text string) Reading {
	prose := fenceRe.ReplaceAllString(text, "\n")
	r := Reading{SelfTalk: len(talkRe.FindAllStringIndex(prose, 2)) == 2}
	var gap []string
	for _, s := range sentenceRe.FindAllString(prose, -1) {
		if gapRe.MatchString(plain.Replace(s)) {
			gap = append(gap, strings.TrimSpace(s))
		}
	}
	if r.Gap = strings.Join(gap, " "); len(r.Gap) > maxGap {
		r.Gap = strings.ToValidUTF8(r.Gap[:maxGap], "")
	}
	for _, m := range inlineRe.FindAllStringSubmatch(prose, -1) {
		for _, name := range identRe.FindAllString(m[1], -1) {
			if !slices.Contains(r.Names, name) && len(r.Names) < maxNames {
				r.Names = append(r.Names, name)
			}
		}
	}
	return r
}
