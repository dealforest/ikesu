package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/dispatch"
	"github.com/kazuph/ikesu/internal/runtime"
)

// When Claude Code resumes a conversation whose background shell commands
// had no completion record (the Mac or ikesu restarted while they ran), it
// writes one notice per command into the transcript. The notice only lands
// in the transcript; nothing asks the agent to act, so the commands stay
// stopped until someone talks to it. After ikesu restores a Claude pane, it
// reads those notices and sends the agent one message listing the original
// commands. The agent restarts what must keep running (servers, watchers) and
// reports one-shot commands (push, deploy, delete) instead of running them
// twice. ikesu itself never re-runs a command.
const interruptedBackgroundNotice = "Background shell command didn't finish before the previous session ended"

const (
	interruptedWatchTimeout = 10 * time.Minute
	interruptedPollInterval = 2 * time.Second
	// The resume command is typed a moment before Claude Code starts and
	// writes its notices; accept notices from shortly before the launch.
	interruptedNoticeSlack = 5 * time.Second
	// Long commands are cut so the message stays readable; the agent can
	// read the full command from its own transcript.
	interruptedCommandMax = 300
)

// Transcripts can be hundreds of MB; restores of many panes at once read
// them one or two at a time.
var interruptedReadSlots = make(chan struct{}, 2)

var interruptedToolUseID = regexp.MustCompile(`<tool-use-id>(toolu_[A-Za-z0-9_-]+)</tool-use-id>`)

type interruptedCommand struct {
	ToolUseID   string
	Description string
	Command     string
}

// readInterruptedBackground returns, in notice order, the background Bash
// commands whose interruption notice Claude Code wrote at or after since.
func readInterruptedBackground(r io.Reader, since time.Time) ([]interruptedCommand, error) {
	reader := bufio.NewReaderSize(r, 1<<20)
	tools := map[string]interruptedCommand{}
	var order []string
	seen := map[string]bool{}
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			notice := bytes.Contains(line, []byte(interruptedBackgroundNotice))
			if bytes.Contains(line, []byte(`"tool_use"`)) {
				collectBackgroundToolUses(line, tools)
			}
			if notice {
				if id, ok := interruptedNoticeID(line, since); ok && !seen[id] {
					seen[id] = true
					order = append(order, id)
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	var out []interruptedCommand
	for _, id := range order {
		if cmd, ok := tools[id]; ok && strings.TrimSpace(cmd.Command) != "" {
			out = append(out, cmd)
		}
	}
	return out, nil
}

func collectBackgroundToolUses(line []byte, tools map[string]interruptedCommand) {
	var rec struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	var parts []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Name  string `json:"name"`
		Input struct {
			Command         string `json:"command"`
			Description     string `json:"description"`
			RunInBackground bool   `json:"run_in_background"`
		} `json:"input"`
	}
	if json.Unmarshal(rec.Message.Content, &parts) != nil {
		return
	}
	for _, part := range parts {
		if part.Type == "tool_use" && part.Name == "Bash" && part.Input.RunInBackground && part.ID != "" {
			tools[part.ID] = interruptedCommand{ToolUseID: part.ID, Description: part.Input.Description, Command: part.Input.Command}
		}
	}
}

// interruptedNoticeID accepts only the queue record Claude Code writes on
// resume, so the same text quoted elsewhere (for example in this message or a
// tool result) is not mistaken for a new interruption.
func interruptedNoticeID(line []byte, since time.Time) (string, bool) {
	var rec struct {
		Type      string `json:"type"`
		Operation string `json:"operation"`
		Timestamp string `json:"timestamp"`
		Content   string `json:"content"`
	}
	if json.Unmarshal(line, &rec) != nil || rec.Type != "queue-operation" || rec.Operation != "enqueue" {
		return "", false
	}
	if !strings.Contains(rec.Content, "<task-notification>") || !strings.Contains(rec.Content, interruptedBackgroundNotice) {
		return "", false
	}
	at, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
	if err != nil || at.Before(since) {
		return "", false
	}
	m := interruptedToolUseID.FindStringSubmatch(rec.Content)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// interruptedBackgroundMessage is the one message sent to the restored agent.
func interruptedBackgroundMessage(cmds []interruptedCommand) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[ikesu] Mac か ikesu の再起動で、この会話のバックグラウンドのコマンドが %d 件、終わる前に止まりました。", len(cmds))
	b.WriteString("サーバーや監視のように動かし続けるものは、起動し直してください。")
	b.WriteString("push・デプロイ・削除のように1回だけ流すものは流し直さず、止まったことと出力ファイルに残った結果を報告してください。\n")
	for _, cmd := range cmds {
		command := strings.Join(strings.Fields(cmd.Command), " ")
		if r := []rune(command); len(r) > interruptedCommandMax {
			command = string(r[:interruptedCommandMax]) + "…"
		}
		description := strings.Join(strings.Fields(cmd.Description), " ")
		if description == "" {
			description = "説明なし"
		}
		fmt.Fprintf(&b, "- %s（%s）: %s\n", description, cmd.ToolUseID, command)
	}
	return strings.TrimRight(b.String(), "\n")
}

// watchInterruptedBackground waits until the restored Claude Code in pid is
// idle, reads its transcript once, and sends the list of interrupted
// background commands. It gives up quietly if the pane is replaced, the
// agent never becomes idle, or the conversation differs from sessionID.
func (s *Server) watchInterruptedBackground(pid int, owner *runtime.Pane, sessionID string, launched time.Time) {
	if owner == nil || sessionID == "" {
		return
	}
	since := launched.Add(-interruptedNoticeSlack)
	go func() {
		deadline := time.Now().Add(interruptedWatchTimeout)
		idle := 0
		for time.Now().Before(deadline) {
			time.Sleep(interruptedPollInterval)
			if s.closing.Load() != 0 {
				return
			}
			alive, status := false, ""
			s.app.Do(func() {
				alive = s.sup.Get(pid) == owner
				status = s.app.PaneStatusKey(pid)
			})
			if !alive {
				return
			}
			// Two idle polls in a row: Claude Code finished starting and is
			// not showing a question that typed text would answer.
			if status != "idle" {
				idle = 0
				continue
			}
			if idle++; idle < 2 {
				continue
			}
			s.sendInterruptedBackground(pid, owner, sessionID, since)
			return
		}
		fmt.Fprintf(os.Stderr, "ikesu restore: pane p%d did not become idle; interrupted background commands were not listed\n", pid)
	}()
}

func (s *Server) sendInterruptedBackground(pid int, owner *runtime.Pane, sessionID string, since time.Time) {
	path, _, err := claudePermissionPath(owner)
	if err != nil || !strings.HasSuffix(path, string(os.PathSeparator)+sessionID+".jsonl") {
		return
	}
	interruptedReadSlots <- struct{}{}
	cmds, err := func() ([]interruptedCommand, error) {
		defer func() { <-interruptedReadSlots }()
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return readInterruptedBackground(f, since)
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ikesu restore: pane p%d: cannot read interrupted background commands: %v\n", pid, err)
		return
	}
	if len(cmds) == 0 {
		return
	}
	params, _ := json.Marshal(api.MsgSendParams{Room: dispatch.DefaultRoom, FromAgent: "ikesu", To: fmtPaneID(pid), Body: interruptedBackgroundMessage(cmds)})
	response := s.dispatchContext(context.Background(), &api.Request{ID: "restore-interrupted", Method: api.MethodMsgSend, Params: params})
	if response.Err != nil {
		fmt.Fprintf(os.Stderr, "ikesu restore: pane p%d: cannot send interrupted background commands: %s\n", pid, response.Err.Message)
	}
}
