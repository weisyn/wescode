package engine

import "github.com/weisyn/wesgine/agent"

// DelegationRole defines an agent's position in multi-agent orchestration.
type DelegationRole string

const (
	RoleOrchestrator DelegationRole = "orchestrator"
	RoleWorker       DelegationRole = "worker"
	RoleHybrid       DelegationRole = "hybrid"
)

// BlackboardNamespace is the Memory namespace used for shared inter-agent
// communication during multi-agent tasks. All agents in a delegation tree
// can read/write to this namespace via cell.Memory() with scope=working.
const BlackboardNamespace = "blackboard"

// DelegationRoleForAgent returns the delegation role for a preset agent ID.
// Used by seedBuiltinAgentsToCell to set ToolPolicy and by chat handler for CycleDetect.
func DelegationRoleForAgent(agentID string) DelegationRole {
	role, ok := agentDelegationRoles[agentID]
	if !ok {
		return RoleHybrid
	}
	return role
}

var agentDelegationRoles = map[string]DelegationRole{
	"builtin-architect": RoleOrchestrator,
	"builtin-pm":        RoleOrchestrator,
	"builtin-coder":     RoleWorker,
	"builtin-frontend":  RoleWorker,
	"builtin-golang":    RoleWorker,
	"builtin-java":      RoleWorker,
	"builtin-dba":       RoleWorker,
	"builtin-test":      RoleWorker,
	"builtin-security":  RoleWorker,
	"builtin-devops":    RoleWorker,
	"builtin-reviewer":  RoleHybrid,
}

// WorkerToolPolicy returns the ToolPolicy for Worker agents (cannot delegate).
func WorkerToolPolicy() agent.ToolPolicy {
	return agent.ToolPolicy{Deny: []string{"delegate_task"}}
}

// OrchestratorToolPolicy returns the ToolPolicy for Orchestrator agents
// (delegate execution, don't write/edit directly).
func OrchestratorToolPolicy() agent.ToolPolicy {
	return agent.ToolPolicy{Deny: []string{"write", "edit", "apply_patch"}}
}
