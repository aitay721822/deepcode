package e2e

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"deepcode/internal/platform/jobs"
)

// checkAccess: a user sees and does only what their roles grant on which
// repositories, never another user's conversations, and the admin console
// keeps an administrator around.
func checkAccess(t *testing.T, base string, admin *client, repoID string, adminConv int64) {
	t.Helper()
	var readers struct {
		ID int64 `json:"id"`
	}
	if code := admin.post("/api/admin/roles", `{"name":"readers","description":"read only","permissions":["repo.read","jobs.view"],"allRepositories":false,"repositoryIds":[]}`, &readers); code != http.StatusCreated {
		t.Fatalf("create role: %d", code)
	}
	// repo.sync on no repository yet: dev holds the permission, but on none of the repositories.
	var syncers struct {
		ID int64 `json:"id"`
	}
	if code := admin.post("/api/admin/roles", `{"name":"syncers","permissions":["repo.sync"],"repositoryIds":[]}`, &syncers); code != http.StatusCreated {
		t.Fatalf("create role: %d", code)
	}
	var user struct {
		ID int64 `json:"id"`
	}
	body := fmt.Sprintf(`{"username":"dev","displayName":"Dev","password":"dev-password","roleIds":[%d,%d]}`, readers.ID, syncers.ID)
	if code := admin.post("/api/admin/users", body, &user); code != http.StatusCreated {
		t.Fatalf("create user: %d", code)
	}
	dev := newClient(t, base)
	if code := dev.login("DEV", "dev-password"); code != http.StatusOK {
		t.Fatalf("dev sign in: %d", code)
	}
	var me struct {
		Username    string   `json:"username"`
		Permissions []string `json:"permissions"`
	}
	if dev.get("/api/auth/me", &me); me.Username != "dev" || len(me.Permissions) != 3 {
		t.Fatalf("me: %+v", me)
	}

	// No repositories granted: none visible, and a repository's existence stays hidden.
	repo := "/api/repos/" + repoID
	var list []repoView
	if dev.get("/api/repos", &list); len(list) != 0 {
		t.Fatalf("dev sees %d repositories before any grant", len(list))
	}
	for path, want := range map[string]int{
		repo:                 http.StatusNotFound,
		repo + "/wiki":       http.StatusNotFound,
		repo + "/files":      http.StatusNotFound,
		"/api/admin/users":   http.StatusForbidden,
		"/api/admin/servers": http.StatusForbidden,
		"/api/admin/jobs/1":  http.StatusNotFound, // a job of a repository dev cannot read
	} {
		if code := dev.get(path, nil); code != want {
			t.Fatalf("dev GET %s: %d, want %d", path, code, want)
		}
	}
	if code := dev.post("/api/repos", `{"url":"acme/demo"}`, nil); code != http.StatusForbidden {
		t.Fatalf("dev import: %d", code)
	}
	var jobList []map[string]any
	if code := dev.get("/api/admin/jobs", &jobList); code != http.StatusOK || len(jobList) != 0 {
		t.Fatalf("dev sees jobs of repositories it cannot read: %d %v", code, jobList)
	}
	if code := dev.get("/api/admin/usage", &jobList); code != http.StatusOK || len(jobList) != 0 {
		t.Fatalf("dev sees the usage of repositories it cannot read: %d %v", code, jobList)
	}

	// Granted: readable, but asking and syncing need their own permissions on it,
	// so the repository says dev may only read it.
	if code := admin.patch(fmt.Sprintf("/api/admin/roles/%d", readers.ID), fmt.Sprintf(`{"repositoryIds":[%q]}`, repoID), nil); code != http.StatusOK {
		t.Fatalf("grant: %d", code)
	}
	if dev.get("/api/repos", &list); len(list) != 1 || fmt.Sprint(list[0].Permissions) != "[repo.read]" {
		t.Fatalf("dev sees %+v after the grant", list)
	}
	var full repoView
	if admin.get(repo, &full); fmt.Sprint(full.Permissions) != "[repo.read repo.chat repo.sync]" {
		t.Fatalf("the admin may %v on the repository", full.Permissions)
	}
	if dev.get("/api/admin/jobs", &jobList); len(jobList) != 2 {
		t.Fatalf("dev sees %d jobs after the grant, want 2", len(jobList))
	}
	if dev.get("/api/admin/usage", &jobList); len(jobList) != 2 || jobList[0]["repoId"] != repoID || jobList[1]["repoId"] != repoID {
		t.Fatalf("dev sees the usage %v after the grant, want the chat and embedding models' of %s", jobList, repoID)
	}
	if code := dev.get(repo+"/wiki", nil); code != http.StatusOK {
		t.Fatalf("dev wiki: %d", code)
	}
	if code, _ := dev.events(http.MethodPost, "/api/chat/stream", fmt.Sprintf(`{"repoId":%q,"question":"hi"}`, repoID)); code != http.StatusForbidden {
		t.Fatalf("dev chat without repo.chat: %d", code)
	}
	if code := dev.post(repo+"/sync", `{}`, nil); code != http.StatusForbidden {
		t.Fatalf("dev sync without repo.sync: %d", code)
	}
	if code := dev.get(fmt.Sprintf("/api/conversations/%d", adminConv), nil); code != http.StatusNotFound {
		t.Fatalf("dev read the admin's conversation: %d", code)
	}

	// The admin console keeps an administrator, and disabling signs a user out.
	var self struct {
		ID int64 `json:"id"`
	}
	admin.get("/api/auth/me", &self)
	if code := admin.delete(fmt.Sprintf("/api/admin/users/%d", self.ID)); code != http.StatusConflict {
		t.Fatalf("deleted yourself: %d", code)
	}
	if code := admin.patch(fmt.Sprintf("/api/admin/users/%d", self.ID), fmt.Sprintf(`{"roleIds":[%d]}`, readers.ID), nil); code != http.StatusConflict {
		t.Fatalf("demoted the last admin: %d", code)
	}
	if code := admin.delete("/api/admin/roles/1"); code != http.StatusConflict {
		t.Fatalf("deleted the admin role: %d", code)
	}
	if code := admin.patch(fmt.Sprintf("/api/admin/users/%d", user.ID), `{"disabled":true}`, nil); code != http.StatusOK {
		t.Fatalf("disable dev: %d", code)
	}
	if code := dev.get("/api/repos", nil); code != http.StatusUnauthorized {
		t.Fatalf("disabled user still signed in: %d", code)
	}
	if code := dev.login("dev", "dev-password"); code != http.StatusUnauthorized {
		t.Fatalf("disabled user signed in: %d", code)
	}
}

// checkSignUp: sign-up stays closed until an administrator opens it; a
// visitor then gets a member account and a session, and cannot close it.
func checkSignUp(t *testing.T, base string, admin *client) {
	t.Helper()
	anon := newClient(t, base)
	var state struct {
		Open bool `json:"open"`
	}
	if code := anon.get("/api/auth/signup", &state); code != http.StatusOK || state.Open {
		t.Fatalf("sign-up at first: %d %+v", code, state)
	}
	visitor := `{"username":"visitor","password":"visitor-password"}`
	if code := anon.post("/api/auth/signup", visitor, nil); code != http.StatusForbidden {
		t.Fatalf("sign-up while closed: %d", code)
	}
	if code := anon.patch("/api/admin/signup", `{"open":true}`, nil); code != http.StatusUnauthorized {
		t.Fatalf("opened without a session: %d", code)
	}
	if code := admin.patch("/api/admin/signup", `{}`, nil); code != http.StatusBadRequest {
		t.Fatalf("toggle without open: %d", code)
	}
	if code := admin.patch("/api/admin/signup", `{"open":true}`, &state); code != http.StatusOK || !state.Open {
		t.Fatalf("open sign-up: %d %+v", code, state)
	}
	var me struct {
		Username    string   `json:"username"`
		Permissions []string `json:"permissions"`
	}
	if code := anon.post("/api/auth/signup", visitor, &me); code != http.StatusCreated || me.Username != "visitor" {
		t.Fatalf("sign-up: %d %+v", code, me)
	}
	if code := anon.get("/api/auth/me", &me); code != http.StatusOK || fmt.Sprint(me.Permissions) != "[repo.read repo.chat]" {
		t.Fatalf("signed up as: %d %+v", code, me)
	}
	if code := anon.post("/api/auth/signup", visitor, nil); code != http.StatusConflict {
		t.Fatalf("the same name again: %d", code)
	}
	if code := anon.patch("/api/admin/signup", `{"open":false}`, nil); code != http.StatusForbidden {
		t.Fatalf("a member closed sign-up: %d", code)
	}
	if code := admin.patch("/api/admin/signup", `{"open":false}`, nil); code != http.StatusOK {
		t.Fatalf("close sign-up: %d", code)
	}
	if code := newClient(t, base).post("/api/auth/signup", `{"username":"late","password":"late-password"}`, nil); code != http.StatusForbidden {
		t.Fatalf("sign-up after closing: %d", code)
	}
}

// checkImportPolicy: a new repository joins its importer's scoped import
// role; one imported before, under any of its names, is the importer's only
// if they could already read it. It returns the importer, signed in, and
// their repository.
func checkImportPolicy(t *testing.T, base string, admin *client, demoID string) (*client, string) {
	t.Helper()
	var importers struct {
		ID int64 `json:"id"`
	}
	if code := admin.post("/api/admin/roles", `{"name":"importers","permissions":["repo.read","repo.import"],"repositoryIds":[]}`, &importers); code != http.StatusCreated {
		t.Fatalf("create role: %d", code)
	}
	if code := admin.post("/api/admin/users", fmt.Sprintf(`{"username":"ivy","password":"ivy-password","roleIds":[%d]}`, importers.ID), nil); code != http.StatusCreated {
		t.Fatalf("create user: %d", code)
	}
	ivy := newClient(t, base)
	if code := ivy.login("ivy", "ivy-password"); code != http.StatusOK {
		t.Fatalf("ivy sign in: %d", code)
	}
	var tools repoView
	if code := ivy.post("/api/repos", `{"url":"acme/tools"}`, &tools); code != http.StatusAccepted {
		t.Fatalf("ivy imports a new repository: %d", code)
	}
	var list []repoView
	if ivy.get("/api/repos", &list); len(list) != 1 || list[0].ID != tools.ID {
		t.Fatalf("ivy sees %+v, want only acme/tools", list)
	}
	for url, want := range map[string]int{
		"acme/tools":    http.StatusOK,        // hers already
		"acme/demo":     http.StatusForbidden, // imported before, hidden from her
		"acme/old-demo": http.StatusForbidden, // the same repository under its old name
	} {
		if code := ivy.post("/api/repos", fmt.Sprintf(`{"url":%q}`, url), nil); code != want {
			t.Fatalf("ivy imports %s: %d, want %d", url, code, want)
		}
	}
	var roles []struct {
		ID            int64    `json:"id"`
		RepositoryIDs []string `json:"repositoryIds"`
	}
	admin.get("/api/admin/roles", &roles)
	for _, r := range roles {
		if r.ID == importers.ID && (len(r.RepositoryIDs) != 1 || r.RepositoryIDs[0] != tools.ID) {
			t.Fatalf("the importers role holds %v, want only %s", r.RepositoryIDs, tools.ID)
		}
	}
	if code := ivy.get("/api/repos/"+demoID, nil); code != http.StatusNotFound {
		t.Fatalf("ivy reads acme/demo: %d", code)
	}
	waitFor(t, "index and wiki of acme/tools", func() bool {
		var r repoView
		admin.get("/api/repos/"+tools.ID, &r)
		failIfJobFailed(t, r.Job)
		return r.Snapshot != nil && r.Snapshot.WikiStatus == "ready" && r.Job.Finished()
	})
	return ivy, tools.ID
}

// checkJobErrors: a failed job's error may quote git or paths on the server,
// so only those who can open the job console read it; the repository's other
// readers get a fixed text.
func checkJobErrors(t *testing.T, db *sql.DB, admin, reader *client, repoID string) {
	t.Helper()
	const detail = "git fetch: exit status 128: /srv/deepcode/data/repos/7: not a git repository"
	if _, err := db.Exec(`UPDATE jobs SET status = 'failed', error = ? WHERE id = (SELECT max(id) FROM jobs WHERE repository_id = (SELECT id FROM repositories WHERE public_id = ?))`, detail, repoID); err != nil {
		t.Fatal(err)
	}
	var seen, full repoView
	reader.get("/api/repos/"+repoID, &seen)
	admin.get("/api/repos/"+repoID, &full)
	if seen.Job == nil || full.Job == nil || seen.Job.Error != "管理員可在工作主控台查看詳細資訊" || full.Job.Error != detail {
		t.Fatalf("job error: the reader sees %+v, the admin %+v", seen.Job, full.Job)
	}
	var list []repoView
	reader.get("/api/repos", &list)
	if len(list) != 1 || list[0].Job == nil || list[0].Job.Error != seen.Job.Error {
		t.Fatalf("the reader's list: %+v", list)
	}
	var job jobs.Job
	if reader.get(fmt.Sprintf("/api/jobs/%d", seen.Job.ID), &job); job.Error != seen.Job.Error {
		t.Fatalf("the job itself shows the reader %q", job.Error)
	}
}

// checkJobEvents: the live job list sends a jobs.view holder the changes of
// jobs on the repositories they may read, and none of the others. during runs
// a job on hidden, then one on visible.
func checkJobEvents(t *testing.T, base string, admin *client, hidden, visible string, during func()) {
	t.Helper()
	var watchers struct {
		ID int64 `json:"id"`
	}
	body := fmt.Sprintf(`{"name":"watchers","permissions":["repo.read","jobs.view"],"repositoryIds":[%q]}`, visible)
	if code := admin.post("/api/admin/roles", body, &watchers); code != http.StatusCreated {
		t.Fatalf("create role: %d", code)
	}
	if code := admin.post("/api/admin/users", fmt.Sprintf(`{"username":"wes","password":"wes-password","roleIds":[%d]}`, watchers.ID), nil); code != http.StatusCreated {
		t.Fatalf("create user: %d", code)
	}
	wes := newClient(t, base)
	if code := wes.login("wes", "wes-password"); code != http.StatusOK {
		t.Fatalf("wes signs in: %d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/admin/jobs/events", nil)
	resp, err := wes.http.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("wes opens the job events: %v %v", resp, err)
	}
	defer resp.Body.Close()
	type change struct {
		RepoID string `json:"repoId"`
		Status string `json:"status"`
	}
	changes := make(chan change, 1024) // read on while during runs
	go func() {
		defer close(changes)
		lines := bufio.NewScanner(resp.Body)
		for lines.Scan() {
			var c change
			if data, ok := strings.CutPrefix(lines.Text(), "data: "); ok && json.Unmarshal([]byte(data), &c) == nil {
				changes <- c
			}
		}
	}()
	during()
	// Delivery is not ordered, but the hidden job had ended before the visible one was queued:
	// by the visible job's end, any change of the hidden one the stream let through has come.
	deadline := time.After(60 * time.Second)
	for {
		select {
		case c, ok := <-changes:
			if !ok {
				t.Fatal("the job events ended")
			}
			if c.RepoID == hidden {
				t.Fatalf("wes was sent a job of a repository wes cannot read: %+v", c)
			}
			if c.RepoID == visible && c.Status == jobs.StatusSucceeded {
				return
			}
		case <-deadline:
			t.Fatal("no change of the visible repository's job arrived")
		}
	}
}

// checkPublicID: a repository is known by its CUID2 alone. Its row id, which
// counts up from 1, finds nothing, even for an administrator, in a URL or in
// a question's body.
func checkPublicID(t *testing.T, admin *client, id string) {
	t.Helper()
	if !regexp.MustCompile(`^[a-z][0-9a-z]{23}$`).MatchString(id) {
		t.Fatalf("repository id %q is not a CUID2", id)
	}
	for _, path := range []string{"/api/repos/" + id, "/api/repos/1", "/api/repos/1/wiki", "/api/repos/1/files"} {
		want := http.StatusNotFound
		if strings.HasSuffix(path, id) {
			want = http.StatusOK
		}
		if code := admin.get(path, nil); code != want {
			t.Fatalf("GET %s: %d, want %d", path, code, want)
		}
	}
	if code, _ := admin.events(http.MethodPost, "/api/chat/stream", `{"repoId":"1","question":"hi"}`); code != http.StatusNotFound {
		t.Fatalf("a question about row id 1: %d, want 404", code)
	}
}
