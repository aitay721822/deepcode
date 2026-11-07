package e2e

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"deepcode/internal/knowledge/wiki"
	"deepcode/internal/platform/jobs"
)

// checkJobConsole: the console lists finished jobs, and a job's log holds its
// work, the model's text streamed while each wiki page was written included.
// The output stream of a finished job sends the lines after ?after= and ends.
func checkJobConsole(t *testing.T, admin *client, repoID string) {
	t.Helper()
	var list []jobs.Listed
	admin.get("/api/admin/jobs?status=succeeded", &list)
	var wikiJob *jobs.Listed
	for i := range list {
		if list[i].RepoPublicID == repoID && list[i].Kind == jobs.KindWiki {
			wikiJob = &list[i]
		}
	}
	if len(list) != 2 || wikiJob == nil || wikiJob.RepoOwner != "acme" || wikiJob.RepoName != "demo" {
		t.Fatalf("jobs: %+v", list)
	}
	var detail struct {
		Job  jobs.Listed    `json:"job"`
		Logs []jobs.LogLine `json:"logs"`
	}
	admin.get(fmt.Sprintf("/api/admin/jobs/%d", wikiJob.ID), &detail)
	var text strings.Builder
	for _, l := range detail.Logs {
		text.WriteString(l.Line + "\n")
	}
	for _, want := range []string{"planned 2 pages: Overview | Web App", "== page 1 of 2: Overview", "It is defined in [", "succeeded"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("wiki job log lacks %q:\n%s", want, text.String())
		}
	}
	code, events := admin.events(http.MethodGet, fmt.Sprintf("/api/admin/jobs/%d/output?after=%d", wikiJob.ID, detail.Logs[1].ID), "")
	if code != http.StatusOK || len(events["log"]) != len(detail.Logs)-2 || len(events["end"]) != 1 {
		t.Fatalf("finished job's stream: %d, %d of %d lines, %d ends", code, len(events["log"]), len(detail.Logs)-2, len(events["end"]))
	}
}

// checkLiveOutput follows a running job's output stream to its end.
func checkLiveOutput(t *testing.T, admin *client, jobID int64) {
	t.Helper()
	_, events := admin.events(http.MethodGet, fmt.Sprintf("/api/admin/jobs/%d/output", jobID), "")
	var lines []string
	for _, raw := range events["log"] {
		var l jobs.LogLine
		json.Unmarshal(raw, &l)
		lines = append(lines, l.Line)
	}
	var end jobs.Listed
	if len(events["end"]) == 1 {
		json.Unmarshal(events["end"][0], &end)
	}
	log := strings.Join(lines, "\n")
	if end.Status != "succeeded" || !strings.Contains(log, "parsed 2 files, copied 2 unchanged") || !strings.Contains(log, "published snapshot") {
		t.Fatalf("live output ended with %+v:\n%s", end, log)
	}
}

// checkGitLab imports a project from a self-hosted GitLab that needs a token:
// the API receives the token, git still reaches its remote with the token
// header added, and citations link the GitLab way.
func checkGitLab(t *testing.T, admin *client, api *fakeAPI, apiURL, sha string) {
	t.Helper()
	var srv struct {
		ID       int64  `json:"id"`
		HasToken bool   `json:"hasToken"`
		APIURL   string `json:"apiUrl"`
	}
	body := fmt.Sprintf(`{"name":"Corp GitLab","kind":"gitlab","baseUrl":"https://gitlab.example.test/","apiUrl":%q,"token":"glpat-test"}`, apiURL)
	if code := admin.post("/api/admin/servers", body, &srv); code != http.StatusCreated || !srv.HasToken || srv.APIURL != apiURL {
		t.Fatalf("add GitLab: %d %+v", code, srv)
	}
	var check struct {
		OK     bool   `json:"ok"`
		Detail string `json:"detail"`
	}
	if admin.post(fmt.Sprintf("/api/admin/servers/%d/test", srv.ID), `{}`, &check); !check.OK || !strings.Contains(check.Detail, "ci-bot") {
		t.Fatalf("test connection: %+v", check)
	}
	var servers []map[string]any
	if admin.get("/api/servers", &servers); len(servers) != 2 || servers[1]["token"] != nil {
		t.Fatalf("servers for importers: %v", servers)
	}

	var repo repoView
	if code := admin.post("/api/repos", `{"url":"https://gitlab.example.test/group/sub/app/-/tree/main"}`, &repo); code != http.StatusAccepted {
		t.Fatalf("import from GitLab: %d", code)
	}
	if repo.Owner != "group/sub" || repo.Name != "app" || repo.Provider != "gitlab" || api.token() != "glpat-test" {
		t.Fatalf("GitLab project: %+v (token %q)", repo, api.token())
	}
	path := "/api/repos/" + repo.ID
	waitFor(t, "index and wiki of the GitLab project", func() bool {
		admin.get(path, &repo)
		failIfJobFailed(t, repo.Job)
		return repo.Snapshot != nil && repo.Snapshot.WikiStatus == "ready" && repo.Job.Finished()
	})
	var wk wiki.Wiki
	admin.get(path+"/wiki", &wk)
	if link := "https://gitlab.example.test/group/sub/app/-/blob/" + sha + "/"; !strings.Contains(wk.Pages[0].Content, link) {
		t.Fatalf("GitLab wiki links: %s", wk.Pages[0].Content)
	}
	if code := admin.delete(fmt.Sprintf("/api/admin/servers/%d", srv.ID)); code != http.StatusConflict {
		t.Fatalf("deleted a server in use: %d", code)
	}
}

// checkDownloadLimit: a repository its git server reports small, whose
// download passes DEEPCODE_MAX_REPO_MB (1 MB here), fails its index job once,
// with no retry, naming the limit.
func checkDownloadLimit(t *testing.T, admin *client) {
	t.Helper()
	remote := t.TempDir()
	git(t, remote, "init", "-q", "-b", "main")
	noise := make([]byte, 2<<20)
	rand.Read(noise)
	commit(t, remote, map[string]string{"noise.txt": base64.StdEncoding.EncodeToString(noise)}) // random: it does not compress
	// The fifth git configuration entry, after the four TestEndToEnd sets.
	t.Setenv("GIT_CONFIG_COUNT", "5")
	t.Setenv("GIT_CONFIG_KEY_4", "url."+fileURL(remote)+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_4", "https://github.com/acme/big.git")
	var big repoView
	if code := admin.post("/api/repos", `{"url":"acme/big"}`, &big); code != http.StatusAccepted {
		t.Fatalf("import acme/big: %d", code)
	}
	waitFor(t, "the index of acme/big to end", func() bool {
		admin.get("/api/repos/"+big.ID, &big)
		return big.Job != nil && big.Job.Finished()
	})
	if j := big.Job; j.Status != jobs.StatusFailed || j.Attempts != 1 || !strings.Contains(j.Error, "size limit of 1 MB (DEEPCODE_MAX_REPO_MB)") {
		t.Fatalf("acme/big: %+v", j)
	}
}
