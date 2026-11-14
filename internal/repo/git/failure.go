package git

import (
	"errors"
	"slices"
	"strings"
)

// ErrUnavailable is a remote git could not reach, or one that failed for now:
// no connection, a dropped or stalled transfer, a 408, 429 or 5xx. A later try
// may get through.
var ErrUnavailable = errors.New("the git server is unavailable")

// ErrRefused is a remote that refused the request for good: the credentials,
// a repository it does not have, or a commit it no longer has (force-pushed
// away). No retry fixes it.
var ErrRefused = errors.New("the git server refused the request")

// What git, in the C locale, and curl say for each; seen from git 2.4x
// against a server answering each status, a closed port, an unknown host and
// a dropped connection.
var (
	refused = []string{"terminal prompts disabled", "authentication failed", "returned error: 401", "returned error: 403",
		"returned error: 404", "' not found", "repository not found", "not our ref", "unadvertised object", "couldn't find remote ref"}
	unavailable = []string{"could not resolve host", "could not resolve proxy", "failed to connect", "could not connect to server",
		"couldn't connect to server", "connection refused", "connection reset", "connection timed out", "operation timed out",
		"operation too slow", "empty reply from server", "early eof", "the remote end hung up unexpectedly", "rpc failed",
		"transfer closed", "unexpected disconnect", "recv failure", "send failure",
		"returned error: 408", "returned error: 429", "returned error: 5"}
)

// classify is err, git's failure to talk to a remote that said msg, as an
// ErrRefused or ErrUnavailable when msg says which; a refusal wins, as a
// dropped connection may follow it.
func classify(err error, msg string) error {
	m := strings.ToLower(msg)
	has := func(s string) bool { return strings.Contains(m, s) }
	switch {
	case slices.ContainsFunc(refused, has):
		return kinded{err, ErrRefused}
	case slices.ContainsFunc(unavailable, has):
		return kinded{err, ErrUnavailable}
	}
	return err
}

// kinded is an error that is also kind (errors.Is), its message its own.
type kinded struct {
	error
	kind error
}

func (k kinded) Is(target error) bool { return target == k.kind }
func (k kinded) Unwrap() error        { return k.error }
