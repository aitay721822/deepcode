package resume

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestIndexStopsThenRetries: a model server that refuses the key fails the
// job at once, saying so; once synced again, one that is down for a while
// keeps the job queued without spending its attempts, and the job finishes
// when the server is back.
func TestIndexStopsThenRetries(t *testing.T) {
	remote(t)
	m := newModel()
	ms := httptest.NewServer(m)
	defer ms.Close()
	api := httptest.NewServer(githubAPI)
	defer api.Close()
	base, db, stop := start(t, t.TempDir(), ms.URL, 1)
	defer stop()

	m.fail(http.StatusUnauthorized)
	id := importDemo(t, base, api.URL)
	j := waitJob(t, db, "the refused attempt's outcome", func(j indexJob) bool { return j.Status != "running" && j.Error != "" })
	if j.Status != "failed" || j.Attempts != 1 || !strings.Contains(j.Error, "401") {
		t.Fatalf("a refused key should fail the job at once: %+v", j)
	}

	m.fail(http.StatusServiceUnavailable)
	if code := call(t, client(t, base), http.MethodPost, base+"/api/repos/"+id+"/sync", ""); code != http.StatusAccepted {
		t.Fatalf("sync: %d", code)
	}
	j = waitJob(t, db, "the outage's outcome", func(j indexJob) bool { return j.Status != "running" && j.Error != "" })
	if j.Status != "queued" || j.Attempts != 0 || j.Retries != 1 || !strings.Contains(j.Error, "503") || j.RunsAgainSeconds < 20 || j.RunsAgainSeconds > 30 {
		t.Fatalf("an outage should queue the job again in 30s without spending an attempt: %+v", j)
	}

	m.fail(0)
	db.Exec(`UPDATE jobs SET run_after = CURRENT_TIMESTAMP`) // the backoff has passed
	j = waitJob(t, db, "the job after the outage", func(j indexJob) bool { return j.Status == "succeeded" || j.Status == "failed" })
	if j.Status != "succeeded" || j.Attempts != 1 {
		t.Fatalf("the job should finish once the server is back: %+v", j)
	}
}

// TestIndexResumesAfterKill kills the process four times while it embeds,
// each time after it stored two batches, then starts it again: the job
// finishes, and no text is embedded twice, so each start went on where the
// last stopped. A killed attempt that stored nothing would count, failing the
// job at the third; these did not, though only the first parsed files: the
// later ones stored vectors alone.
func TestIndexResumesAfterKill(t *testing.T) {
	if data := os.Getenv("RESUME_CHILD_DATA"); data != "" {
		start(t, data, os.Getenv("RESUME_CHILD_MODEL"), 1)
		select {} // until killed
	}
	remote(t)
	m := newModel()
	ms := httptest.NewServer(m)
	defer ms.Close()
	api := httptest.NewServer(githubAPI)
	defer api.Close()
	data := t.TempDir()
	base, _, stop := start(t, data, ms.URL, 0) // no workers: the job waits for the first child
	importDemo(t, base, api.URL)
	stop()

	const kills = 4
	for kill := 1; kill <= kills; kill++ {
		held, release := m.holdAfter(2)
		child := exec.Command(os.Args[0], "-test.run=^TestIndexResumesAfterKill$")
		child.Env = append(os.Environ(), "RESUME_CHILD_DATA="+data, "RESUME_CHILD_MODEL="+ms.URL)
		var out strings.Builder
		child.Stdout, child.Stderr = &out, &out
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan error, 1)
		go func() { exited <- child.Wait() }()
		select {
		case <-held:
		case err := <-exited:
			t.Fatalf("run %d ended before it embedded two batches: %v\n%s", kill, err, out.String())
		case <-time.After(90 * time.Second):
			child.Process.Kill()
			t.Fatalf("run %d never embedded two batches:\n%s", kill, out.String())
		}
		child.Process.Kill()
		<-exited
		close(release)
	}

	before, _, _ := m.counts()
	_, db, stop := start(t, data, ms.URL, 1)
	defer stop()
	j := waitJob(t, db, "the job after the kills", func(j indexJob) bool { return j.Status == "succeeded" || j.Status == "failed" })
	if j.Status != "succeeded" {
		t.Fatalf("killed %d times while making progress, the job should still finish: %+v", kills, j)
	}
	texts, _, twice := m.counts()
	if len(twice) > 0 {
		t.Fatalf("%d texts were embedded twice, so a start did not go on where the last stopped: %.80q", len(twice), twice[0])
	}
	if before != kills*2*32 || texts <= before {
		t.Fatalf("the killed runs embedded %d texts, all runs %d: want %d, then more", before, texts, kills*2*32)
	}
}
