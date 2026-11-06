package e2e

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"deepcode/internal/platform/ai"
)

// fakeAPI answers like the GitHub and GitLab REST APIs for the demo
// repositories, and remembers the token GitLab was sent.
type fakeAPI struct {
	mu          sync.Mutex
	gitlabToken string
}

func (f *fakeAPI) token() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gitlabToken
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.RequestURI {
	case "/repos/acme/demo", "/repos/acme/old-demo": // GitHub answers for a renamed repository's old name too
		fmt.Fprint(w, `{"full_name":"acme/demo","private":false,"description":"Demo repo","stargazers_count":42,"size":12,"default_branch":"main"}`)
	case "/repos/acme/huge": // 2 GB, over the 1 MB limit
		fmt.Fprint(w, `{"full_name":"acme/huge","private":false,"description":"Huge","stargazers_count":0,"size":2097152,"default_branch":"main"}`)
	case "/repos/acme/big": // reported small; its download is not
		fmt.Fprint(w, `{"full_name":"acme/big","private":false,"description":"Big","stargazers_count":0,"size":12,"default_branch":"main"}`)
	case "/repos/acme/tools":
		fmt.Fprint(w, `{"full_name":"acme/tools","private":false,"description":"Tools","stargazers_count":1,"size":12,"default_branch":"main"}`)
	case "/projects/group%2Fsub%2Fapp?statistics=true":
		f.mu.Lock()
		f.gitlabToken = r.Header.Get("PRIVATE-TOKEN")
		f.mu.Unlock()
		fmt.Fprint(w, `{"path_with_namespace":"group/sub/app","description":"GitLab demo","default_branch":"main","star_count":7}`)
	case "/user":
		fmt.Fprint(w, `{"username":"ci-bot"}`)
	default:
		http.NotFound(w, r)
	}
}

// fakeLLM is an OpenAI-compatible server: bag-of-words embeddings, a fixed
// wiki plan, fixed search terms, and answers citing the first source in one
// group with a made-up file.
type fakeLLM struct {
	mu     sync.Mutex
	chunks int // chunk texts embedded so far
	terms  int // requests for search terms so far
	plans  int // requests for a wiki plan so far
}

func (f *fakeLLM) planRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.plans
}

func (f *fakeLLM) chunkInputs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chunks
}

func (f *fakeLLM) termRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.terms
}

var sourceRe = regexp.MustCompile(`<source path="([^"]+)" lines="(\d+)-(\d+)"`)

func (f *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input    []string     `json:"input"`
		Messages []ai.Message `json:"messages"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if r.URL.Path == "/v1/embeddings" {
		data := make([]map[string]any, len(body.Input))
		for i, s := range body.Input {
			if strings.HasPrefix(s, "File: ") {
				f.mu.Lock()
				f.chunks++
				f.mu.Unlock()
			}
			data[i] = map[string]any{"index": i, "embedding": bagOfWords(s)}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
		return
	}
	reply := `{"pages":[{"title":"Overview","query":"main server"},{"title":"Web App","query":"App start helper"}]}`
	if strings.Contains(body.Messages[0].Content, "Turn the user's question into") {
		f.mu.Lock()
		f.terms++
		f.mu.Unlock()
		reply = "Server Handle"
	} else if strings.Contains(body.Messages[0].Content, "plan a concise technical wiki") {
		f.mu.Lock()
		f.plans++
		f.mu.Unlock()
	} else {
		m := sourceRe.FindStringSubmatch(body.Messages[len(body.Messages)-1].Content)
		if m == nil {
			http.Error(w, "fakeLLM: the prompt carries no <source>", http.StatusBadRequest)
			return
		}
		reply = fmt.Sprintf("It is defined in [%s:%s-%s; bogus.go:1-2].", m[1], m[2], m[3])
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, part := range []string{reply[:len(reply)/2], reply[len(reply)/2:]} {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": part}}}})
		fmt.Fprintf(w, "data: %s\n\n", b)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func bagOfWords(s string) []float32 {
	v := make([]float32, 16)
	for _, w := range regexp.MustCompile(`[a-z]+`).FindAllString(strings.ToLower(s), -1) {
		h := fnv.New32a()
		h.Write([]byte(w))
		v[h.Sum32()%16]++
	}
	return v
}
