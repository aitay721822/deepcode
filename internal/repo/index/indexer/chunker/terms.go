package chunker

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var identRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// maxIdent is the longest run Split reads as a name, in bytes: a longer one is
// data, such as base64 or a JWT, whose case changes would only add noise.
const maxIdent = 64

// Terms is a chunk's terms column: the words of the compound identifiers in its
// path and content (getUserToken: get user token). The full-text index keeps
// getUserToken as one token, so without them a question that says "token"
// misses it; snake_case and one-word identifiers, which the index already
// splits or holds, add nothing.
func Terms(path, content string) string {
	return strings.Join(Split(path+"\n"+content), " ")
}

// Split lists the words of each distinct compound identifier in text, in
// order and lowercased: "getUserToken(id, get_userToken)" gives get, user,
// token, user, token. One-word identifiers and runs longer than maxIdent give
// none, and words of one or two letters are left out, since no question
// searches for them.
func Split(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range identRe.FindAllString(text, -1) {
		if seen[id] || len(id) > maxIdent {
			continue
		}
		seen[id] = true
		for _, w := range words(id) {
			if utf8.RuneCountInString(w) >= 3 {
				out = append(out, w)
			}
		}
	}
	return out
}

// words splits an identifier where its case changes, lowercased: getUserToken
// is get, user, token; HTTPServer is http, server; parseURLs is parse, urls;
// base64Encode is base64, encode. An identifier of one word gives none.
func words(id string) []string {
	rs := []rune(id)
	var out []string
	start := 0
	for i := 1; i < len(rs); i++ {
		// A capital starts a word after a small letter or a digit, and the last
		// capital of an acronym starts the word that follows it, unless that
		// is an s: the acronym's plural (userIDs, getAPIsFor).
		if unicode.IsUpper(rs[i]) && (!unicode.IsUpper(rs[i-1]) || i+1 < len(rs) && unicode.IsLower(rs[i+1]) && rs[i+1] != 's') {
			out = append(out, strings.ToLower(string(rs[start:i])))
			start = i
		}
	}
	if start == 0 {
		return nil
	}
	return append(out, strings.ToLower(string(rs[start:])))
}
