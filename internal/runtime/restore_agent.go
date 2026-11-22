package runtime

// AgentRunningForRestore recognizes the shell after agy exits even when an
// older report still overrides the detected agent. Other reports keep their
// existing running behavior.
func (p *Pane) AgentRunningForRestore(recordedAgent string) bool {
	snap := p.Snapshot()
	if recordedAgent == "agy" && snap.ForegroundAgent == "" && snap.AgentID != "agy" && snap.FgPID > 0 && snap.FgPID == p.ShellPID() {
		return false
	}
	return snap.AgentID != "" || snap.ForegroundAgent == "agy"
}
