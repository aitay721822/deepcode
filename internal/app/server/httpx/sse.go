package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/session"
)

var (
	// pingEvery is how often a watched stream pings, which keeps idle
	// proxies from closing it, and checks its session again.
	pingEvery = 20 * time.Second
	// writeTimeout bounds each write: a client that stopped reading makes it
	// fail, and net/http then cancels the request, which ends the stream.
	writeTimeout = 10 * time.Second
)

// Stream is a server-sent event response. Frames are written by hand as
// "id: <n>\nevent: <name>\ndata: <json>\n\n", the format the UI parses.
type Stream struct {
	mu sync.Mutex // Watch pings beside the handler
	w  gin.ResponseWriter
	rc *http.ResponseController
}

// OpenStream starts a server-sent event response.
func OpenStream(c *gin.Context) *Stream {
	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // keeps nginx from holding events back
	c.Status(http.StatusOK)
	c.Writer.Flush()
	return &Stream{w: c.Writer, rc: http.NewResponseController(c.Writer)}
}

// Send writes one event; an id above 0 lets a reconnecting EventSource resume
// after it (Last-Event-ID).
func (s *Stream) Send(event string, id int64, data any) {
	b, _ := json.Marshal(data)
	frame := fmt.Sprintf("event: %s\ndata: %s\n\n", event, b)
	if id > 0 {
		frame = fmt.Sprintf("id: %d\n", id) + frame
	}
	s.write(frame)
}

// write sends one frame within writeTimeout.
func (s *Stream) write(frame string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rc.SetWriteDeadline(time.Now().Add(writeTimeout))
	io.WriteString(s.w, frame)
	s.w.Flush()
	s.rc.SetWriteDeadline(time.Time{}) // none while idle, so ending the response cannot time out
}

// Watch keeps a long stream open and in step with its session until stop is
// called. Every 20 seconds it pings and signs the session in again, so
// Principal sees role changes; once the session has ended, its user is
// disabled, or allowed rejects them, it cancels ctx to end the stream. Call
// stop before the handler returns.
func (s *Stream) Watch(c *gin.Context, sessions *session.Store, allowed func(*session.Principal) bool) (ctx context.Context, stop func()) {
	ctx, cancel := context.WithCancel(c.Request.Context())
	done := make(chan struct{})
	token := SessionToken(c)
	go func() {
		defer close(done)
		tick := time.NewTicker(pingEvery)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			p, err := sessions.Authenticate(ctx, token)
			if err != nil || !allowed(p) {
				cancel()
				return
			}
			c.Set(principalKey, p)
			s.write(": ping\n\n")
		}
	}()
	return ctx, func() { cancel(); <-done }
}
