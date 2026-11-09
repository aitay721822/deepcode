package server_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"deepcode/internal/access/role"
	"deepcode/internal/app"
	"deepcode/internal/platform/storage"
)

// TestMCP: an agent without a token is pointed to OAuth; one with a personal
// token, or with one it got through OAuth (registration, the signed-in
// user's approval, PKCE, a refresh), reads the user's repositories through a
// real MCP client, and only those; a revoked token stops working.
func TestMCP(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := storage.Open(ctx, filepath.Join(dir, "deepcode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO repositories(id, public_id, url, owner, name) VALUES (1, 'r1', 'https://github.com/acme/demo', 'acme', 'demo'),
			(2, 'r2', 'https://github.com/acme/wip', 'acme', 'wip');
		INSERT INTO snapshots(id, repository_id, commit_sha, status) VALUES (1, 1, 'abc', 'ready');
		UPDATE repositories SET ready_snapshot_id = 1 WHERE id = 1;
		INSERT INTO file_contents(hash, content) VALUES ('h', 'package demo' || char(13, 10) || 'func A() {}' || char(10));
		INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count) VALUES (1, 'demo.go', 'go', 'h', 26, 2);
		INSERT INTO chunks(snapshot_id, path, start_line, end_line, content, content_hash) VALUES (1, 'demo.go', 2, 2, 'func A() {}', 'c');
		INSERT INTO wiki_pages(id, snapshot_id, slug, title, position, content) VALUES (1, 1, 'overview', 'Overview', 0, 'All of it.'),
			(2, 1, 'router', 'Router', 1, 'Routes.');
		INSERT INTO wiki_pages(snapshot_id, slug, title, position, parent, content) VALUES (1, 'mount', 'Mount', 2, 1, hex(zeroblob(49994)))`); err != nil { // with "# 2.1 Mount\n\n", 100001 bytes: one past the bound
		t.Fatal(err)
	}
	big, wide := strings.Repeat("x\n", 1001), strings.Repeat(strings.Repeat("y", 3000)+"\n", 60) // past the line bound; past the size bound
	if _, err := db.Exec(`INSERT INTO file_contents(hash, content) VALUES ('big', ?), ('wide', ?)`, big, wide); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO source_files(snapshot_id, path, language, content_hash, size, line_count)
		VALUES (1, 'big.go', 'go', 'big', ?, 1001), (1, 'wide.go', 'go', 'wide', ?, 60)`, len(big), len(wide)); err != nil {
		t.Fatal(err)
	}
	// The chat model draws a map of demo.go when asked to, and has nothing to add otherwise.
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []struct{ Content string } }
		json.NewDecoder(r.Body).Decode(&body)
		reply := "DONE"
		if len(body.Messages) > 0 && strings.Contains(body.Messages[0].Content, "drawing a code map") {
			reply = `{"title":"A","description":"d","traces":[{"title":"Define","description":"d","guide":"g","locations":[{"title":"A","description":"d","path":"demo.go","line":2,"code":"func A() {}"}]}]}`
		}
		chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": reply}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer model.Close()
	log.SetOutput(io.Discard)
	defer log.SetOutput(os.Stderr)
	a, err := app.New(ctx, app.Config{DataDir: dir, AdminPassword: "password-0", SessionTTL: time.Hour, ChatURL: model.URL, ChatModel: "m"},
		db, http.NotFoundHandler(), true)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler)
	defer srv.Close()

	res, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized || !strings.Contains(res.Header.Get("WWW-Authenticate"), srv.URL+"/.well-known/oauth-protected-resource/mcp") {
		t.Fatalf("no token: %d %q", res.StatusCode, res.Header.Get("WWW-Authenticate"))
	}
	var resource struct {
		Servers []string `json:"authorization_servers"`
	}
	var server struct {
		Token    string `json:"token_endpoint"`
		Register string `json:"registration_endpoint"`
	}
	getJSON(t, srv.URL+"/.well-known/oauth-protected-resource/mcp", &resource)
	getJSON(t, srv.URL+"/.well-known/oauth-authorization-server", &server)
	if len(resource.Servers) != 1 || resource.Servers[0] != srv.URL || server.Token != srv.URL+"/oauth/token" {
		t.Fatalf("metadata: %+v %+v", resource, server)
	}

	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar}
	send(t, browser, http.MethodPost, srv.URL+"/api/auth/login", map[string]string{"username": "admin", "password": "password-0"}, nil)
	var personal struct {
		Secret string `json:"secret"`
		Token  struct{ ID int64 }
	}
	send(t, browser, http.MethodPost, srv.URL+"/api/auth/tokens", map[string]any{"name": "cli", "days": 30}, &personal)
	if got, err := repositories(srv.URL, personal.Secret); err != nil || !strings.Contains(got, "acme/demo") {
		t.Fatalf("with a personal token: %q %v", got, err)
	}
	read := map[string]any{"repo": "acme/demo", "path": "./demo.go", "end_line": 1}
	if got, err := call(srv.URL, personal.Secret, "read_file", read); err != nil || !strings.Contains(got, "lines 1-1 of 2") ||
		!strings.Contains(got, "1  package demo\n") || strings.Contains(got, "\r") || strings.Contains(got, "func A") {
		t.Errorf("read_file: %q %v", got, err)
	}
	if got, err := call(srv.URL, personal.Secret, "read_file", map[string]any{"repo": "acme/demo", "path": `\big.go`}); err != nil ||
		!strings.Contains(got, "lines 1-1000 of 1001") || !strings.HasSuffix(got, "(read on from start_line 1001)\n") {
		t.Errorf("past the line bound: %.120q %v", got, err)
	}
	if got, err := call(srv.URL, personal.Secret, "read_file", map[string]any{"repo": "acme/demo", "path": "wide.go"}); err != nil ||
		len(got) > 105_000 || !strings.Contains(got, strings.Repeat("y", 2000)+"…\n") || strings.Contains(got, strings.Repeat("y", 2001)) ||
		!strings.HasSuffix(got, "(read on from start_line 51)\n") {
		t.Errorf("past the size bound: %d bytes, %v", len(got), err)
	}
	if _, err := call(srv.URL, personal.Secret, "read_file", map[string]any{"repo": "acme/demo", "path": "demo.go", "start_line": 5}); err == nil ||
		!strings.Contains(err.Error(), "outside") {
		t.Errorf("past the end of a file: %v", err)
	}
	if _, err := call(srv.URL, personal.Secret, "read_file", map[string]any{"repo": "acme/wip", "path": "a.go"}); err == nil ||
		!strings.Contains(err.Error(), "still being indexed") {
		t.Errorf("a repository without a snapshot: %v", err)
	}
	if got, err := call(srv.URL, personal.Secret, "read_wiki_structure", map[string]any{"repo": "acme/demo"}); err != nil ||
		!strings.HasSuffix(got, "- 1 Overview\n- 2 Router\n  - 2.1 Mount\n") {
		t.Errorf("read_wiki_structure: %q %v", got, err)
	}
	// The page under Router, one byte past 100 KB, does not fit beside the others, and is cut when read alone.
	if got, err := call(srv.URL, personal.Secret, "read_wiki_contents", map[string]any{"repo": "acme/demo"}); err != nil ||
		!strings.Contains(got, "# 2 Router\n\nRoutes.") || strings.Contains(got, "# 2.1") || !strings.HasSuffix(got, "by number: 2.1") {
		t.Errorf("read_wiki_contents: %.300q %v", got, err)
	}
	if got, err := call(srv.URL, personal.Secret, "read_wiki_contents", map[string]any{"repo": "acme/demo", "page": "2.1"}); err != nil ||
		len(got) > 101_000 || !strings.HasPrefix(got, "# 2.1 Mount") || !strings.Contains(got, "cut at 100 KB") {
		t.Errorf("a page past the bound: %d bytes, %v", len(got), err)
	}
	if got, err := call(srv.URL, personal.Secret, "search_code", map[string]any{"repo": "acme/demo", "query": "func A"}); err != nil ||
		!strings.Contains(got, "## demo.go:2-2") {
		t.Errorf("search_code: %q %v", got, err)
	}
	if _, err := call(srv.URL, personal.Secret, "ask_question", map[string]any{"repo": "acme/demo", "question": "q", "mode": "slow"}); err == nil ||
		!strings.Contains(err.Error(), "mode must be") {
		t.Errorf("an unknown mode: %v", err)
	}
	if got, err := call(srv.URL, personal.Secret, "ask_question", map[string]any{"repo": "acme/demo", "question": "Where is func A?", "mode": "Codemap"}); err != nil ||
		!strings.Contains(got, "**1a**") || !strings.Contains(got, "demo.go:2-2") {
		t.Errorf("a code map: %q %v", got, err)
	}
	if _, err := call(srv.URL, personal.Secret, "ask_question", map[string]any{"repo": "acme/demo", "question": "Where is func A?", "mode": "deep"}); err != nil &&
		strings.Contains(err.Error(), "mode must be") {
		t.Errorf("deep: %v", err)
	}

	// A member given no repository reads none of them.
	send(t, browser, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{"username": "dev", "password": "password-1",
		"roleIds": []int64{role.Member}}, nil)
	jar2, _ := cookiejar.New(nil)
	member := &http.Client{Jar: jar2}
	send(t, member, http.MethodPost, srv.URL+"/api/auth/login", map[string]string{"username": "dev", "password": "password-1"}, nil)
	var theirs struct{ Secret string }
	send(t, member, http.MethodPost, srv.URL+"/api/auth/tokens", map[string]any{"name": "cli"}, &theirs)
	if got, err := repositories(srv.URL, theirs.Secret); err != nil || strings.Contains(got, "acme") {
		t.Errorf("a member's list: %q %v", got, err)
	}
	if _, err := call(srv.URL, theirs.Secret, "read_file", read); err == nil || !strings.Contains(err.Error(), "no repository") {
		t.Errorf("a member read a repository not theirs: %v", err)
	}
	// A reader of the repository may not ask about it.
	var readers struct{ ID int64 }
	send(t, browser, http.MethodPost, srv.URL+"/api/admin/roles", map[string]any{"name": "readers", "permissions": []string{"repo.read"},
		"repositoryIds": []string{"r1"}}, &readers)
	send(t, browser, http.MethodPost, srv.URL+"/api/admin/users", map[string]any{"username": "reader", "password": "password-2",
		"roleIds": []int64{readers.ID}}, nil)
	jar3, _ := cookiejar.New(nil)
	reader := &http.Client{Jar: jar3}
	send(t, reader, http.MethodPost, srv.URL+"/api/auth/login", map[string]string{"username": "reader", "password": "password-2"}, nil)
	var readerToken struct{ Secret string }
	send(t, reader, http.MethodPost, srv.URL+"/api/auth/tokens", map[string]any{"name": "cli"}, &readerToken)
	if _, err := call(srv.URL, readerToken.Secret, "read_file", read); err != nil {
		t.Errorf("a reader's read_file: %v", err)
	}
	if _, err := call(srv.URL, readerToken.Secret, "ask_question", map[string]any{"repo": "acme/demo", "question": "q"}); err == nil ||
		!strings.Contains(err.Error(), "do not allow repo.chat") {
		t.Errorf("a reader asked: %v", err)
	}

	var client struct {
		ID string `json:"client_id"`
	}
	redirect := "http://127.0.0.1:9/cb"
	send(t, http.DefaultClient, http.MethodPost, server.Register, map[string]any{"client_name": "Agent", "redirect_uris": []string{redirect}}, &client)
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	ask := map[string]any{"responseType": "code", "clientId": client.ID, "redirectUri": redirect,
		"codeChallenge": base64.RawURLEncoding.EncodeToString(sum[:]), "codeChallengeMethod": "S256", "state": "s", "approve": true}
	if status, _ := do(t, http.DefaultClient, http.MethodPost, srv.URL+"/api/oauth/authorize", ask); status != http.StatusUnauthorized {
		t.Errorf("an approval without a session: %d", status)
	}
	evil := maps.Clone(ask)
	evil["redirectUri"] = "https://evil.example/cb"
	if status, b := do(t, browser, http.MethodPost, srv.URL+"/api/oauth/authorize", evil); status != http.StatusBadRequest ||
		bytes.Contains(b, []byte("evil.example")) {
		t.Errorf("an unregistered redirect: %d %s", status, b)
	}
	var approved struct{ Redirect string }
	send(t, browser, http.MethodPost, srv.URL+"/api/oauth/authorize", ask, &approved)
	back, _ := url.Parse(approved.Redirect)
	exchange := url.Values{"grant_type": {"authorization_code"}, "code": {back.Query().Get("code")}, "redirect_uri": {redirect},
		"client_id": {client.ID}, "code_verifier": {verifier}}
	tokens := grant(t, server.Token, exchange)
	for want, form := range map[string]url.Values{"invalid_grant": exchange, "unsupported_grant_type": {"grant_type": {"password"}}} {
		if status, out := post(t, server.Token, form); status != http.StatusBadRequest || out.Error != want {
			t.Errorf("%s: %d %+v", want, status, out)
		}
	}
	tokens = grant(t, server.Token, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.Refresh}, "client_id": {client.ID}})
	if got, err := repositories(srv.URL, tokens.Access); err != nil || !strings.Contains(got, "acme/demo") {
		t.Fatalf("with an OAuth token: %q %v", got, err)
	}

	// Only a user's own personal tokens are theirs to revoke, not another's or an app's; revoking an app is for its own grants.
	var appToken int64
	if err := db.QueryRow(`SELECT id FROM api_tokens WHERE client_id = ?`, client.ID).Scan(&appToken); err != nil {
		t.Fatal(err)
	}
	for c, id := range map[*http.Client]int64{member: personal.Token.ID, browser: appToken} {
		if status, _ := do(t, c, http.MethodDelete, fmt.Sprintf("%s/api/auth/tokens/%d", srv.URL, id), nil); status != http.StatusNotFound {
			t.Errorf("revoked token %d: %d", id, status)
		}
	}
	send(t, member, http.MethodDelete, srv.URL+"/api/auth/apps/"+client.ID, nil, nil)
	for _, secret := range []string{personal.Secret, tokens.Access} {
		if _, err := repositories(srv.URL, secret); err != nil {
			t.Errorf("another revoked a token: %v", err)
		}
	}
	for _, days := range []int{-1, 367} {
		if status, _ := do(t, browser, http.MethodPost, srv.URL+"/api/auth/tokens", map[string]any{"name": "x", "days": days}); status != http.StatusBadRequest {
			t.Errorf("a token for %d days: %d", days, status)
		}
	}

	send(t, browser, http.MethodDelete, fmt.Sprintf("%s/api/auth/tokens/%d", srv.URL, personal.Token.ID), nil, nil)
	send(t, browser, http.MethodDelete, srv.URL+"/api/auth/apps/"+client.ID, nil, nil)
	for _, secret := range []string{personal.Secret, tokens.Access} {
		if _, err := repositories(srv.URL, secret); err == nil {
			t.Error("a revoked token still works")
		}
	}
}

func getJSON(t *testing.T, u string, v any) {
	t.Helper()
	send(t, http.DefaultClient, http.MethodGet, u, nil, v)
}

// do makes a request with a JSON body, when body is set: the status and the answer.
func do(t *testing.T, c *http.Client, method, u string, body any) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, u, r)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, b
}

// send makes a request as do does, and decodes the answer into v, when set;
// any status but 2xx fails the test.
func send(t *testing.T, c *http.Client, method, u string, body, v any) {
	t.Helper()
	status, b := do(t, c, method, u, body)
	if status/100 != 2 {
		t.Fatalf("%s %s: %d %s", method, u, status, b)
	}
	if v != nil {
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("%s %s: %v: %s", method, u, err, b)
		}
	}
}

type issued struct {
	Access  string `json:"access_token"`
	Refresh string `json:"refresh_token"`
	Error   string `json:"error"`
}

// post posts form to the token endpoint, as OAuth has it: the status and the answer.
func post(t *testing.T, endpoint string, form url.Values) (int, issued) {
	t.Helper()
	res, err := http.PostForm(endpoint, form)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out issued
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatalf("token endpoint: %d %v", res.StatusCode, err)
	}
	return res.StatusCode, out
}

// grant posts form as post does, and fails the test unless tokens come back.
func grant(t *testing.T, endpoint string, form url.Values) issued {
	t.Helper()
	status, out := post(t, endpoint, form)
	if status != http.StatusOK || out.Access == "" {
		t.Fatalf("token endpoint: %d %+v", status, out)
	}
	return out
}

// repositories calls list_repositories as an MCP client sending secret.
func repositories(base, secret string) (string, error) {
	return call(base, secret, "list_repositories", map[string]any{})
}

// call calls tool with args as an MCP client sending secret: its text, or the
// error it answered with.
func call(base, secret, tool string, args map[string]any) (string, error) {
	ctx := context.Background()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + "/mcp", HTTPClient: &http.Client{Transport: bearer(secret)}}, nil)
	if err != nil {
		return "", err
	}
	defer session.Close()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		return "", err
	}
	if len(res.Content) == 0 {
		return "", fmt.Errorf("%s answered nothing", tool)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		return "", fmt.Errorf("%s", text)
	}
	return text, nil
}

type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}
