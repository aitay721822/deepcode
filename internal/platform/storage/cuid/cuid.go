// Package cuid makes CUID2s: ids of 24 characters, a letter and then base-36
// digits, that give away neither how many came before nor the next one. It
// follows github.com/paralleldrive/cuid2: a random letter, then SHA3-512 of
// the time, a random salt, a counter and this process's fingerprint.
package cuid

import (
	"crypto/rand"
	"crypto/sha3"
	"math/big"
	mrand "math/rand/v2"
	"strconv"
	"sync/atomic"
	"time"
)

const length = 24

var (
	count       atomic.Int64 // from a random start, as cuid2's counter
	fingerprint = hash(rand.Text())[:32]
)

func init() { count.Store(mrand.Int64N(476782367)) }

// New returns a new CUID2.
func New() string {
	in := strconv.FormatInt(time.Now().UnixMilli(), 36) + rand.Text() + strconv.FormatInt(count.Add(1), 36) + fingerprint
	return string(rune('a'+mrand.IntN(26))) + hash(in)[1:length]
}

// hash is the SHA3-512 of s in base 36, without its first digit, which leans
// to the small ones.
func hash(s string) string {
	sum := sha3.Sum512([]byte(s))
	return new(big.Int).SetBytes(sum[:]).Text(36)[1:]
}
