package modelapi

import (
	"testing"

	"deepcode/internal/platform/ai/acp"
	"deepcode/internal/platform/ai/compat"
)

// TestDescribe: the console sees no chat model, a server's by its model and
// context, and an agent by its name and what it said it is (none before it
// starts; acp's tests pin what it says once started).
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
}
