package screenprompt

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/kazuph/ikesu/internal/agyview"
	"github.com/kazuph/ikesu/internal/api"
)

var agyFinalFooter = regexp.MustCompile(`(?:^|·)\s*enter (?:Submit All|Submit)\s*(?:·|$)`)
var agyHeader = regexp.MustCompile(`^Question ([1-9][0-9]*)/([1-9][0-9]*):\s*(.+)$`)

// Parse only the observed Question form, including its navigation footer.
// Review screens are intentionally not actionable.
func parseAgy(screen []string) *Prompt {
	start := -1
	for i, line := range screen {
		if strings.TrimSpace(line) != "Question" || i+1 >= len(screen) || !solidRule(screen[i+1]) {
			continue
		}
		j := i + 2
		for j < len(screen) && strings.TrimSpace(screen[j]) == "" {
			j++
		}
		if j < len(screen) && agyHeader.MatchString(strings.TrimSpace(screen[j])) {
			start = j
		}
	}

	if start < 0 {
		return nil
	}
	m := agyHeader.FindStringSubmatch(strings.TrimSpace(screen[start]))
	index, _ := strconv.Atoi(m[1])
	total, _ := strconv.Atoi(m[2])
	if index > total {
		return nil
	}
	p := &Prompt{AgyQuestion: true, Cursor: -1, Progress: -1, Keys: map[string]string{}, Checked: map[string]bool{}, Question: index, Total: total}
	label := "Question " + m[1] + "/" + m[2]
	p.Params.QuestionLabel = &label
	p.Params.Kind = "agent_prompt"
	p.Params.Title = m[3]
	p.Params.AllowText = true
	footer := false
	selected := 0
	writeIn := false
	editor := false
	for rowIndex, line := range screen[start+1:] {
		trim := strings.TrimSpace(line)
		if editor {
			if trim == "enter Submit · esc Back" || index == total && trim == "enter Submit All · esc Back" {
				footer = agyview.Tail(screen[start+1+rowIndex+1:])
				p.TextOpen = true
				break
			}
			p.TextValue += strings.TrimSpace(line) + "\n"
			continue
		}
		if trim == "Your answer:" {
			if !writeIn || p.Cursor != len(p.Items)-1 {
				return nil
			}
			editor = true
			continue
		}
		if agyFooter(trim, index == total) {
			footer = agyview.Tail(screen[start+1+rowIndex+1:])
			break
		}
		if trim == "" {
			continue
		}
		row := numbered.FindStringSubmatch(line)
		if row == nil {
			return nil
		}
		id, value := row[2], strings.TrimSpace(row[3])
		if id != strconv.Itoa(len(p.Items)+1) || value == "" {
			return nil
		}
		p.Items = append(p.Items, id)
		if row[1] != "" {
			selected++
			p.Cursor = len(p.Items) - 1
		}
		if value == "Write-in..." {
			if writeIn {
				return nil
			}
			writeIn = true
			p.TextID = id
			continue
		}
		if writeIn || checkbox.MatchString(value) {
			return nil
		}
		p.Params.Options = append(p.Params.Options, api.DecisionOption{ID: id, Label: value, Role: "other"})
	}
	if !footer || !writeIn || selected != 1 || len(p.Params.Options) == 0 {
		return nil
	}
	presentQuestion(p)
	return p
}

// The final question submits its answer directly; no separate submit key is sent.
func agyFooter(line string, final bool) bool {
	if !strings.Contains(line, "↑/↓ Navigate") || strings.Contains(line, "Toggle") {
		return false
	}
	if final {
		return agyFinalFooter.MatchString(line) || strings.Contains(line, "enter Write-in")
	}
	return strings.Contains(line, "→ Next") && (strings.Contains(line, "enter Select") || strings.Contains(line, "enter Write-in")) && strings.Contains(line, "esc Skip")
}
