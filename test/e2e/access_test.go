package e2e

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"deepcode/internal/platform/jobs"
)

// checkAccess: a user sees and does only what their roles grant on which
// repositories, never another user's conversations, and the admin console
// keeps an administrator around.
func checkAccess(t *testing.T, base string, admin *client, repoID string, adminConv string) {
	t.Helper()
	var readers struct {
		ID string `json:"id"`
	}
	if code := admin.post("/api/admin/roles", `{"name":"readers","description":"read only","permissions":["repo.read","jobs.view"],"allRepositories":false,"repositoryIds":[]}`, &readers); code != http.StatusCreated {
		t.Fatalf("create role: %d", code)
	}
	// repo.sync on no repository yet: dev holds the permission, but on none of the repositories.
	var syncers struct {
		ID string `json:"id"`
	}
	if code := admin.post("/api/admin/roles", `{"name":"syncers","permissions":["repo.sync"],"repositoryIds":[]}`, &syncers); code != http.StatusCreated {
		t.Fatalf("create role: %d", code)
	}
	var user struct {
		ID string `json:"id"`
	}
	body := fmt.Sprintf(`{"username":"dev","displayName":"Dev","password":"dev-password","roleIds":[%q,%q]}`, readers.ID, syncers.ID)
	if code := admin.post("/api/admin/users", body, &user); code != http.StatusCreated {
		t.Fatalf("create user: %d", code)
	}
	dev := newClient(t, base)
	if code := dev.login("DEV", "dev-password"); code != http.StatusOK {
		t.Fatalf("dev sign in: %d", code)
	}
	// Ids are public ids: one naming no role is refused, and a user revokes only their own tokens.
	for _, roles := range []string{`["zzzzzzzzzzzzzzzzzzzzzzzz"]`, `[1]`} {
		if code := admin.post("/api/admin/users", `{"username":"nobody","password":"nobody-password","roleIds":`+roles+`}`, nil); code != http.StatusBadRequest {
			t.Fatalf("a user with roles %s: %d, want 400", roles, code)
		}
	}
	var adminToken struct {
		Token struct {
			ID string `json:"id"`
		} `json:"token"`
	}
	if code := admin.post("/api/auth/tokens", `{"name":"admin's"}`, &adminToken); code != http.StatusCreated {
		t.Fatalf("admin token: %d", code)
	}
	if code := dev.delete("/api/auth/tokens/" + adminToken.Token.ID); code != http.StatusNotFound {
		t.Fatalf("dev revoked the admin's token: %d, want 404", code)
	}
	if code := admin.delete("/api/auth/tokens/" + adminToken.Token.ID); code != http.StatusNoContent {
		t.Fatalf("the admin's token after dev's try: %d, want 204", code)
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
	var allJobs []jobs.Listed
	if admin.get("/api/admin/jobs", &allJobs); len(allJobs) == 0 {
		t.Fatal("the admin sees no jobs")
	}
	for path, want := range map[string]int{
		repo:                                     http.StatusNotFound,
		repo + "/wiki":                           http.StatusNotFound,
		repo + "/files":                          http.StatusNotFound,
		"/api/admin/users":                       http.StatusForbidden,
		"/api/admin/servers":                     http.StatusForbidden,
		"/api/admin/jobs/" + allJobs[0].PublicID: http.StatusNotFound, // a job of a repository dev cannot read
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
	if code := admin.patch("/api/admin/roles/"+readers.ID, fmt.Sprintf(`{"repositoryIds":[%q]}`, repoID), nil); code != http.StatusOK {
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
	if code := dev.get("/api/conversations/"+adminConv, nil); code != http.StatusNotFound {
		t.Fatalf("dev read the admin's conversation: %d", code)
	}

	// The admin console keeps an administrator, and disabling signs a user out.
	var self struct {
		ID string `json:"id"`
	}
	admin.get("/api/auth/me", &self)
	if code := admin.delete("/api/admin/users/" + self.ID); code != http.StatusConflict {
		t.Fatalf("deleted yourself: %d", code)
	}
	if code := admin.patch("/api/admin/users/"+self.ID, fmt.Sprintf(`{"roleIds":[%q]}`, readers.ID), nil); code != http.StatusConflict {
		t.Fatalf("demoted the last admin: %d", code)
	}
	var roles []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	admin.get("/api/admin/roles", &roles)
	adminRole := ""
	for _, r := range roles {
		if r.Name == "admin" {
			adminRole = r.ID
		}
	}
	if code := admin.delete("/api/admin/roles/" + adminRole); adminRole == "" || code != http.StatusConflict {
		t.Fatalf("deleted the admin role: %d", code)
	}
	if code := admin.patch("/api/admin/users/"+user.ID, `{"disabled":true}`, nil); code != http.StatusOK {
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
		ID string `json:"id"`
	}
	if code := admin.post("/api/admin/roles", `{"name":"importers","permissions":["repo.read","repo.import"],"repositoryIds":[]}`, &importers); code != http.StatusCreated {
		t.Fatalf("create role: %d", code)
	}
	if code := admin.post("/api/admin/users", fmt.Sprintf(`{"username":"ivy","password":"ivy-password","roleIds":[%q]}`, importers.ID), nil); code != http.StatusCreated {
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
		ID            string   `json:"id"`
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
	if reader.get("/api/jobs/"+seen.Job.PublicID, &job); job.Error != seen.Job.Error {
		t.Fatalf("the job itself shows the reader %q", job.Error)
	}
}

// checkStop: only a user who may sync a repository stops its jobs (one who
// may not read it is told there is no such job); a queued job fails at once,
// saying who stopped it, and a finished one has nothing to stop.
func checkStop(t *testing.T, db *sql.DB, admin, reader *client, repoID, hiddenID string) {
	t.Helper()
	latest := func(repo string) (id string) {
		db.QueryRow(`SELECT public_id FROM jobs WHERE id = (SELECT max(id) FROM jobs WHERE repository_id = (SELECT id FROM repositories WHERE public_id = ?))`, repo).Scan(&id)
		return id
	}
	id := latest(repoID)
	if _, err := db.Exec(`UPDATE jobs SET status = 'queued', run_after = datetime('now', '+1 hour') WHERE public_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	stop := "/api/jobs/" + id + "/stop"
	if code := reader.post(stop, `{}`, nil); code != http.StatusForbidden {
		t.Fatalf("a reader stops a job: %d", code)
	}
	if code := reader.post("/api/jobs/"+latest(hiddenID)+"/stop", `{}`, nil); code != http.StatusNotFound {
		t.Fatalf("a job of a hidden repository: %d", code)
	}
	var job jobs.Job
	if code := admin.post(stop, `{}`, &job); code != http.StatusOK || job.Status != "failed" || job.Error != "已由 admin 停止" {
		t.Fatalf("stop a queued job: %d %+v", code, job)
	}
	if code := admin.post(stop, `{}`, nil); code != http.StatusConflict {
		t.Fatalf("stop a stopped job: %d", code)
	}
}

// checkJobEvents: the live job list sends a jobs.view holder the changes of
// jobs on the repositories they may read, and none of the others. during runs
// a job on hidden, then one on visible.
func checkJobEvents(t *testing.T, base string, admin *client, hidden, visible string, during func()) {
	t.Helper()
	var watchers struct {
		ID string `json:"id"`
	}
	body := fmt.Sprintf(`{"name":"watchers","permissions":["repo.read","jobs.view"],"repositoryIds":[%q]}`, visible)
	if code := admin.post("/api/admin/roles", body, &watchers); code != http.StatusCreated {
		t.Fatalf("create role: %d", code)
	}
	if code := admin.post("/api/admin/users", fmt.Sprintf(`{"username":"wes","password":"wes-password","roleIds":[%q]}`, watchers.ID), nil); code != http.StatusCreated {
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
	// Every other id is a public id too: row id 1, which each of these tables holds, names nothing.
	for _, try := range []struct{ method, path string }{
		{http.MethodGet, "/api/jobs/1"}, {http.MethodGet, "/api/admin/jobs/1"},
		{http.MethodPatch, "/api/admin/users/1"}, {http.MethodDelete, "/api/admin/roles/1"}, {http.MethodPatch, "/api/admin/servers/1"},
	} {
		if code := admin.call(try.method, try.path, "{}", nil); code != http.StatusNotFound {
			t.Fatalf("%s %s: %d, want 404", try.method, try.path, code)
		}
	}
	if code := admin.post("/api/repos", `{"url":"https://github.com/acme/other","serverId":"zzzzzzzzzzzzzzzzzzzzzzzz"}`, nil); code != http.StatusNotFound {
		t.Fatalf("an import from no such server: %d, want 404", code)
	}
}

// checkIsolation: two users, each allowed to read and ask about one repository
// (design 5.9: projects kept apart by deepcode's accounts), ask through the
// same model, an agent's under acp in each repository's folder under work (""
// for a server), and each sees only their repository, conversation and usage; a
// conversation is gone with its repository's grant.
func checkIsolation(t *testing.T, base string, db *sql.DB, admin *client, demoID, toolsID, work string) {
	t.Helper()
	repos, users, convs, roles := map[string]string{"alice": demoID, "bob": toolsID}, map[string]*client{}, map[string]string{}, map[string]string{}
	for name, repo := range repos {
		var row int64
		if err := db.QueryRow(`SELECT id FROM repositories WHERE public_id = ?`, repo).Scan(&row); err != nil {
			t.Fatal(err)
		}
		folder := filepath.Join(work, fmt.Sprint(row))
		if work != "" {
			os.RemoveAll(folder)
		}
		var role, conv struct{ ID string } // json takes "id" too
		body := fmt.Sprintf(`{"name":"%s's project","permissions":["repo.read","repo.chat","jobs.view"],"repositoryIds":[%q]}`, name, repo)
		if code := admin.post("/api/admin/roles", body, &role); code != http.StatusCreated {
			t.Fatalf("%s's role: %d", name, code)
		}
		if code := admin.post("/api/admin/users", fmt.Sprintf(`{"username":%q,"password":"%s-password","roleIds":[%q]}`, name, name, role.ID), nil); code != http.StatusCreated {
			t.Fatalf("create %s: %d", name, code)
		}
		users[name], roles[name] = newClient(t, base), role.ID
		if code := users[name].login(name, name+"-password"); code != http.StatusOK {
			t.Fatalf("%s signs in: %d", name, code)
		}
		_, events := users[name].events(http.MethodPost, "/api/chat/stream", fmt.Sprintf(`{"repoId":%q,"question":"What does %s's project do?"}`, repo, name))
		if len(events["error"]) > 0 || len(events["done"]) != 1 || len(events["conversation"]) != 1 || json.Unmarshal(events["conversation"][0], &conv) != nil {
			t.Fatalf("%s asks: %v", name, events)
		}
		convs[name] = conv.ID
		if fi, err := os.Stat(folder); work != "" && (err != nil || !fi.IsDir()) {
			t.Errorf("%s's question, not in the agent's folder %s: %v", name, folder, err)
		}
	}
	for name, other := range map[string]string{"alice": "bob", "bob": "alice"} {
		c, theirs := users[name], repos[other]
		var list []repoView
		if c.get("/api/repos", &list); len(list) != 1 || list[0].ID != repos[name] {
			t.Errorf("%s sees %+v, want only their repository", name, list)
		}
		for path, want := range map[string]int{"/api/conversations/" + convs[name]: http.StatusOK, "/api/conversations/" + convs[other]: http.StatusNotFound,
			"/api/repos/" + theirs: http.StatusNotFound, "/api/repos/" + theirs + "/wiki": http.StatusNotFound} {
			if code := c.get(path, nil); code != want {
				t.Errorf("%s GET %s: %d, want %d", name, path, code, want)
			}
		}
		if code, _ := c.events(http.MethodPost, "/api/chat/stream", fmt.Sprintf(`{"repoId":%q,"question":"hi"}`, theirs)); code == http.StatusOK {
			t.Errorf("%s asked about %s's repository", name, other)
		}
		var usage []map[string]any
		if c.get("/api/admin/usage", &usage); len(usage) == 0 || slices.ContainsFunc(usage, func(u map[string]any) bool { return u["repoId"] != repos[name] }) {
			t.Errorf("%s sees the usage %v, want only their repository's", name, usage)
		}
	}
	if code := admin.patch("/api/admin/roles/"+roles["alice"], fmt.Sprintf(`{"repositoryIds":[%q]}`, toolsID), nil); code != http.StatusOK {
		t.Fatalf("alice's role moved: %d", code)
	} else if code := users["alice"].get("/api/conversations/"+convs["alice"], nil); code != http.StatusNotFound {
		t.Errorf("alice's conversation once the role no longer grants its repository: %d, want 404", code)
	}
}
