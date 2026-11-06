package webui

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestSPA(t *testing.T) {
	h := spa(fstest.MapFS{
		"index.html":  {Data: []byte("app")},
		"assets/a.js": {Data: []byte("js")},
	})
	const page = "text/html,application/xhtml+xml"
	cases := []struct {
		path, accept string
		code         int
		body         string
	}{
		{"/", page, 200, "app"},
		{"/vercel/next.js", page, 200, "app"}, // a repository, not a file
		{"/o/r/search/5", page, 200, "app"},
		{"/assets/a.js", "*/*", 200, "js"},
		{"/assets/gone.js", page, 404, ""},
		{"/favicon.ico", "image/avif,image/*", 404, ""},
	}
	for _, tc := range cases {
		req := httptest.NewRequest("GET", tc.path, nil)
		req.Header.Set("Accept", tc.accept)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != tc.code || (tc.body != "" && rec.Body.String() != tc.body) {
			t.Errorf("%s: %d %q, want %d %q", tc.path, rec.Code, rec.Body.String(), tc.code, tc.body)
		}
	}
}
