// ABOUTME: SystemClient.AgentLog — backend-free read of a sandbox's raw agent
// ABOUTME: terminal output (logs/agent.log), full or tail-N, ANSI left intact.
package yoloai

import "github.com/kstenerud/yoloai/internal/sandbox"

// AgentLog returns the raw agent terminal output for a sandbox. tailLines <= 0
// returns the full log; otherwise the last tailLines lines. A missing log is
// not an error — it returns ("", nil). ANSI escape sequences are left intact;
// the caller decides whether to strip them. This is a host-filesystem read and
// does not require a running backend.
func (s *SystemClient) AgentLog(name string, tailLines int) (string, error) {
	return sandbox.ReadAgentLog(s.layout, name, tailLines)
}
