package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/account"
	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/app/importer"
	"deepcode/internal/platform/jobs"
	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/gitserver"
)

// TestWatchEndsWithTheSession: a watched stream pings, and ends once its
// user is disabled.
func TestWatchEndsWithTheSession(t *testing.T) {
	defer func(old time.Duration) { pingEvery = old }(pingEvery)
	pingEvery = 20 * time.Millisecond
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "w.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	accounts := &account.Store{DB: db}
	sessions := session.New(db, accounts, &role.Store{DB: db}, time.Hour)
	accounts.Bootstrap(ctx, "password-0")
	name, password := "dev", "password-1"
	dev, err := accounts.Create(ctx, account.Input{Username: &name, Password: &password})
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := sessions.Login(ctx, dev.Username, password, "1.1.1.1")
	if err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stream", Session(sessions), func(c *gin.Context) {
		s := OpenStream(c)
		ctx, stop := s.Watch(c, sessions, func(*session.Principal) bool { return true })
		defer stop()
		<-ctx.Done()
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/stream", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	ping := make([]byte, len(": ping\n\n"))
	if _, err := io.ReadFull(resp.Body, ping); err != nil || string(ping) != ": ping\n\n" {
		t.Fatalf("first frame %q: %v", ping, err)
	}
	if _, err := accounts.Update(ctx, dev.ID, 1, account.Input{Disabled: new(true)}); err != nil {
		t.Fatal(err)
	}
	ended := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		ended <- err
	}()
	select {
	case err := <-ended:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream outlived its session")
	}
}

// TestStuckClientEndsTheStream: when a client stops reading, a write times
// out and the request ends instead of blocking for good.
func TestStuckClientEndsTheStream(t *testing.T) {
	defer func(old time.Duration) { writeTimeout = old }(writeTimeout)
	writeTimeout = 50 * time.Millisecond
	gin.SetMode(gin.TestMode)
	ended := make(chan struct{})
	r := gin.New()
	r.GET("/stream", func(c *gin.Context) {
		defer close(ended)
		s := OpenStream(c)
		big := strings.Repeat("x", 64<<10)
		for c.Request.Context().Err() == nil {
			s.Send("big", 0, big)
		}
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "GET /stream HTTP/1.1\r\nHost: test\r\n\r\n") // and never read the answer
	select {
	case <-ended:
	case <-time.After(20 * time.Second):
		t.Fatal("the stream to a client that stopped reading went on")
	}
}

// TestGuardAndCookie: PATCH bodies must be JSON as POST ones must, names
// under .localhost are loopback, a body over 64 KB is refused, and the session
// cookie is HttpOnly and SameSite=Lax, and Secure behind an HTTPS proxy.
func TestGuardAndCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Guard(true))
	r.Any("/x", func(c *gin.Context) {
		if Bind(c, new(map[string]any)) {
			SetSession(c, "token", time.Hour)
		}
	})
	send := func(method, host, contentType, proto, body string) *http.Response {
		req := httptest.NewRequest(method, "/x", strings.NewReader(body))
		req.Host = host
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Forwarded-Proto", proto)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Result()
	}
	for _, c := range []struct {
		method, host, contentType, body string
		want                            int
	}{
		{http.MethodPatch, "localhost", "text/plain", "{}", http.StatusUnsupportedMediaType},
		{http.MethodPost, "localhost.evil.example", "application/json", "{}", http.StatusForbidden},
		{http.MethodPost, "127.0.0.1", "application/json", `{"a":"` + strings.Repeat("a", 64<<10) + `"}`, http.StatusRequestEntityTooLarge},
	} {
		if res := send(c.method, c.host, c.contentType, "", c.body); res.StatusCode != c.want {
			t.Errorf("%s to %s as %s: %d, want %d", c.method, c.host, c.contentType, res.StatusCode, c.want)
		}
	}
	for proto, secure := range map[string]bool{"": false, "https": true} {
		res := send(http.MethodPost, "app.localhost:8080", "application/json", proto, "{}")
		if cs := res.Cookies(); res.StatusCode != http.StatusOK || len(cs) != 1 || !cs[0].HttpOnly || cs[0].SameSite != http.SameSiteLaxMode || cs[0].Secure != secure {
			t.Errorf("X-Forwarded-Proto %q: %d %q", proto, res.StatusCode, res.Header["Set-Cookie"])
		}
	}
}

// TestDomainError: each kind of domain failure answers with its status and
// message; any other error is a 500 that does not repeat it.
func TestDomainError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	answer := func(err error) (int, string) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
		DomainError(c, err)
		var body struct{ Error string }
		json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Error
	}
	for kind, want := range map[error]int{
		jobs.ErrNotFound:      http.StatusNotFound,
		gitserver.ErrInvalid:  http.StatusBadRequest,
		role.ErrConflict:      http.StatusConflict,
		gitserver.ErrSealed:   http.StatusConflict,
		account.ErrBadLogin:   http.StatusUnauthorized,
		importer.ErrForbidden: http.StatusForbidden,
		session.ErrThrottled:  http.StatusTooManyRequests,
		catalog.ErrTooLarge:   http.StatusRequestEntityTooLarge,
		catalog.ErrUpstream:   http.StatusBadGateway,
	} {
		err := fmt.Errorf("import: %w", kind)
		if status, msg := answer(err); status != want || msg != err.Error() {
			t.Errorf("%v: %d %q, want %d", kind, status, msg, want)
		}
	}
	if status, msg := answer(errors.New("SELECT token FROM git_servers")); status != http.StatusInternalServerError || strings.Contains(msg, "token") {
		t.Errorf("another error: %d %q", status, msg)
	}
}
