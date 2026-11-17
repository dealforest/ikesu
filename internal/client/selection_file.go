package client

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"

	"github.com/kazuph/ikesu/internal/endpoint"
)

// selectionFileEnv names a file where this client keeps the space, tab and
// pane it shows on its local session. The phone's bridge sets it per phone:
// the phone's screen client ends when the app goes to the background and a
// new one starts on return, which then opens where the phone left off
// instead of on the server's own selection.
const selectionFileEnv = "IKESU_SELECTION_FILE"

type selectionFile struct {
	path    string
	session string
	written *endpoint.ViewSelection
}

type savedSelection struct {
	Session   string                  `json:"session"`
	Selection *endpoint.ViewSelection `json:"selection"`
}

func newSelectionFile(session string) *selectionFile {
	path := os.Getenv(selectionFileEnv)
	if path == "" {
		return nil
	}
	return &selectionFile{path: path, session: session}
}

// load returns the saved selection of this session, or nil.
func (f *selectionFile) load() *endpoint.ViewSelection {
	if f == nil {
		return nil
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil
	}
	var saved savedSelection
	if json.Unmarshal(data, &saved) != nil || saved.Session != f.session || saved.Selection == nil {
		return nil
	}
	f.written = saved.Selection
	return saved.Selection
}

// save writes the selection when it changed since the last write. Errors are
// ignored: the file only helps the next screen start where this one was.
func (f *selectionFile) save(selection *endpoint.ViewSelection) {
	if f == nil || selection == nil || reflect.DeepEqual(selection, f.written) {
		return
	}
	data, err := json.Marshal(savedSelection{Session: f.session, Selection: selection})
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.path), ".selection-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), f.path) != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	f.written = selection
}
