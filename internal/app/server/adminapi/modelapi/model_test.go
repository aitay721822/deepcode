package modelapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"deepcode/internal/platform/ai/acp"
	"deepcode/internal/platform/ai/compat"
)

// TestDescribe: the console sees no chat model, a server's by its model and
// context, and an agent by its name and what it said it is: none before it
// starts, then its name, version, model option and last failure, whose time
// is in UTC, as SQLite writes one.
func TestDescribe(t *testing.T) {
	if m := describe(nil); m != (model{}) {
		t.Errorf("no chat model: %+v", m)
	}
	server := describe(&compat.Client{ModelName: "qwen", Context: 32000})
	if server != (model{Provider: "openai", Name: "qwen", Context: 32000, Scale: 1}) {
		t.Errorf("a server: %+v", server)
	}
	agentModel := describe(&acp.Agent{Command: "npx -y @agentclientprotocol/claude-agent-acp"})
	if agentModel.Provider != "acp" || agentModel.Name != "claude-agent-acp/default" || agentModel.Scale != 1 || agentModel.Agent == nil || *agentModel.Agent != (agent{}) {
		t.Errorf("an agent not started: %+v, %+v", agentModel, agentModel.Agent)
	}
	at := time.Date(2026, 11, 17, 9, 30, 0, 0, time.FixedZone("CST", 8*3600))
	got := agentOf(acp.Status{Name: "claude-agent-acp", Version: "0.107.3", Model: "opus", Trouble: "not signed in", At: at})
	if *got != (agent{Name: "claude-agent-acp", Version: "0.107.3", Model: "opus", Trouble: "not signed in", At: "2026-11-17 01:30:00"}) {
		t.Errorf("a started agent: %+v", got)
	}
}

// TestMount: the chat model is for those who may view jobs.
func TestMount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Mount(r, nil, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/model", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("GET /model with no one signed in: %d", w.Code)
	}
}
