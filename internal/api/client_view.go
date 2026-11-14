package api

const MethodClientViewList = "client.view.list"

// ClientView describes a terminal's last painted workspace, never session focus.
// PaneIDs contains only displayed panes in the selected tab, excluding covered panes.
// WorkspacePaneIDs contains every pane of the displayed workspace, in every tab,
// including covered panes, so a decision of that space can be held while the
// user is in the space (moving to a neighbouring pane, tab, or popup).
// Focused is nil until the terminal explicitly reports focus or blur.
type ClientView struct {
	ClientID    int      `json:"client_id"`
	MachineID   string   `json:"machine_id"`
	Session     string   `json:"session"`
	WorkspaceID string   `json:"workspace_id"`
	PaneIDs     []string `json:"pane_ids"`
	// WorkspacePaneIDs is nil from clients older than 0.4.147.
	WorkspacePaneIDs []string `json:"workspace_pane_ids,omitempty"`
	Focused          *bool    `json:"focused"`
}
type ClientViewListResult struct {
	Type  string       `json:"type"`
	Views []ClientView `json:"views"`
}
