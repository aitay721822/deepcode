package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"deepcode/internal/platform/storage"
	"deepcode/internal/repo/gitserver"
)

// TestAPIs drives both providers against a fake API through servers stored
// with tokens, as an import does.
func TestAPIs(t *testing.T) {
	var seen []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.RequestURI+" "+r.Header.Get("PRIVATE-TOKEN")+r.Header.Get("Authorization"))
		switch r.RequestURI {
		case "/projects/group%2Fsub%2Fapp?statistics=true":
			fmt.Fprintf(w, `{"path_with_namespace":"group/sub/App","description":%q,"default_branch":"main","star_count":3,"statistics":{"repository_size":2097152}}`, strings.Repeat("長", 3000))
		case "/user":
			fmt.Fprint(w, `{"username":"bot"}`)
		case "/repos/o/r":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	box, _ := gitserver.OpenBox(t.TempDir(), "test key")
	st := &gitserver.Store{DB: db, Box: box}
	str := func(s string) *string { return &s }
	created, err := st.Create(ctx, gitserver.Input{Name: str("Corp"), Kind: str(gitserver.GitLab), BaseURL: str("https://code.example.com"), APIURL: str(api.URL), Token: str("tok")})
	if err != nil {
		t.Fatal(err)
	}
	gl, err := st.Get(ctx, created.ID) // with the token unsealed
	if err != nil {
		t.Fatal(err)
	}
	m, err := Fetch(ctx, gl, "group/sub/app")
	if err != nil || m.Path != "group/sub/App" || m.SizeKB != 2048 || m.DefaultBranch != "main" || m.Stars != 3 || len([]rune(m.Description)) != maxDescription || !strings.HasSuffix(seen[0], " tok") {
		t.Fatalf("GitLab meta: %+v %v (requests %q)", m, err, seen)
	}
	if detail, err := Check(ctx, gl); err != nil || detail != "Signed in as bot." {
		t.Fatalf("check: %q %v", detail, err)
	}
	if _, err := Fetch(ctx, gl, "group/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing project: %v", err)
	}
	gh, _ := st.Create(ctx, gitserver.Input{Name: str("GHE"), Kind: str(gitserver.GitHub), BaseURL: str("https://ghe.example.com"), APIURL: str(api.URL)})
	if _, err := Fetch(ctx, gh, "o/r"); !errors.Is(err, ErrUpstream) || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("rate limit: %v", err)
	}
}
