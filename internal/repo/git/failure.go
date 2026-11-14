package git

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ErrUnavailable is a remote git could not reach, or one that failed for now:
// no connection, a dropped or stalled transfer, a rate limit, a 408, 429 or
// 5xx. A later try may get through.
var ErrUnavailable = errors.New("the git server is unavailable")

// ErrRefused is a remote that refused the request for good: the credentials,
// a repository it does not have, or a commit it no longer has (force-pushed
// away). No retry fixes it.
var ErrRefused = errors.New("the git server refused the request")

// What git, in the C locale, and curl say for each when no HTTP status says
// it; seen from git 2.4x against a closed port, an unknown host, a dropped
// connection and a commit the remote lacks.
var (
	refused = []string{"terminal prompts disabled", "authentication failed", "' not found", "repository not found",
		"not our ref", "unadvertised object", "couldn't find remote ref"}
	unavailable = []string{"could not resolve host", "could not resolve proxy", "failed to connect", "could not connect to server",
		"couldn't connect to server", "connection refused", "connection reset", "connection timed out", "operation timed out",
		"operation too slow", "empty reply from server", "early eof", "the remote end hung up unexpectedly", "rpc failed",
		"transfer closed", "unexpected disconnect", "recv failure", "send failure"}
)

// status is the HTTP status curl names: "The requested URL returned error: 413".
var status = regexp.MustCompile(`returned error: (\d{3})`)

// classify is err, git's failure to talk to a remote that said msg, as an
// ErrRefused or ErrUnavailable when msg says which. A rate limit is
// unavailable, whatever its status (GitHub's is a 403). Else an HTTP status
// decides, as the words after it (a hang-up once a 413 ended the request)
// say nothing more; a status that is neither, such as 413, is neither. Else
// a refusal wins, as a dropped connection may follow it. The remote's own
// words count too: a server can only make its own job stop or wait, as it
// could by refusing or dropping the connection.
func classify(err error, msg string) error {
	m := strings.ToLower(msg)
	has := func(s string) bool { return strings.Contains(m, s) }
	if has("rate limit") {
		return kinded{err, ErrUnavailable}
	}
	if s := status.FindStringSubmatch(m); s != nil {
		switch code, _ := strconv.Atoi(s[1]); {
		case code == 401 || code == 403 || code == 404:
			return kinded{err, ErrRefused}
		case code == 408 || code == 429 || code >= 500:
			return kinded{err, ErrUnavailable}
		}
		return err
	}
	switch {
	case slices.ContainsFunc(refused, has):
		return kinded{err, ErrRefused}
	case slices.ContainsFunc(unavailable, has):
		return kinded{err, ErrUnavailable}
	}
	return err
}

// kinded is an error that is also kind (errors.Is), its message its own;
// package ai has its twin, as neither imports the other.
type kinded struct {
	error
	kind error
}

func (k kinded) Is(target error) bool { return target == k.kind }
func (k kinded) Unwrap() error        { return k.error }
