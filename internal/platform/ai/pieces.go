package ai

import "unicode/utf8"

// Pieces is the text an Exchange's next request sends, piece by piece: the
// prompt's messages, the tools' definitions, each reply's text and calls,
// each result. A backend's Exchange embeds it, which gives it Size and Added.
type Pieces struct {
	texts []string
	sizes []int // each piece as Size counted it, -1 until it does
	sent  int   // the pieces the last request sent
}

// Add keeps s as the next piece, and says which.
func (p *Pieces) Add(s string) int {
	p.texts, p.sizes = append(p.texts, s), append(p.sizes, -1)
	return len(p.texts) - 1
}

// Piece is piece i's text.
func (p *Pieces) Piece(i int) string { return p.texts[i] }

// Replace makes s piece i's text, to be counted again.
func (p *Pieces) Replace(i int, s string) { p.texts[i], p.sizes[i] = s, -1 }

// Len is how many pieces there are.
func (p *Pieces) Len() int { return len(p.texts) }

// MarkSent records that the request just answered sent the first n pieces.
func (p *Pieces) MarkSent(n int) { p.sent = n }

// Texts are the first n pieces' texts.
func (p *Pieces) Texts(n int) []string { return p.texts[:n:n] }

// Chars is the pieces' characters.
func (p *Pieces) Chars() (n int) {
	for _, s := range p.texts {
		n += utf8.RuneCountInString(s)
	}
	return n
}

// Size is the next request's text in count's units, each piece counted once
// (count must not change), and the part of it the last request sent.
func (p *Pieces) Size(count func(string) int) (next, sent int) {
	for i, s := range p.texts {
		if p.sizes[i] < 0 {
			p.sizes[i] = count(s)
		}
		if next += p.sizes[i]; i < p.sent {
			sent += p.sizes[i]
		}
	}
	return next, sent
}

// Added is the pieces the last request answered did not send: its reply's
// and the results'; all of them before the first reply.
func (p *Pieces) Added() int { return len(p.texts) - p.sent }
