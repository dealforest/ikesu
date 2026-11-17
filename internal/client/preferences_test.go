package client

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/endpoint"
)

func TestClientPreferencesPersistLocallyAndReportWriteFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IKESU_STATE_DIR", filepath.Join(root, "state"))
	t.Setenv(config.EnvVar, filepath.Join(root, "config.toml"))
	s, err := loadLocalSettings("endpoint-preferences")
	if err != nil {
		t.Fatal(err)
	}
	split := .7
	want := endpoint.ViewPreferences{SidebarWidth: 42, SidebarManual: true, SidebarCollapsed: true, SidebarSectionSplit: &split}
	if err := s.save(want); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadLocalSettings("endpoint-preferences")
	if err != nil || !reflect.DeepEqual(loaded.preferences, &want) {
		t.Fatalf("reload: %v, %v", loaded, err)
	}
	stat, err := os.Stat(s.path)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("preferences permissions: %v, %v", stat, err)
	}
	blocked := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocked, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	s.path = filepath.Join(blocked, "preferences.json")
	if err := s.save(endpoint.ViewPreferences{SidebarWidth: 26}); err == nil {
		t.Fatal("write failure was concealed")
	}
	if !reflect.DeepEqual(s.preferences, &want) {
		t.Fatal("failed write replaced committed preferences")
	}
	bytes, err := os.ReadFile(blocked)
	if err != nil || string(bytes) != "preserve" {
		t.Fatal("write failure damaged blocking file")
	}
}

func TestClientPreferencesRejectCorruptState(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IKESU_STATE_DIR", root)
	t.Setenv(config.EnvVar, filepath.Join(root, "config.toml"))
	s, err := loadLocalSettings("endpoint-preferences")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"{", `{"sidebar_width":0}`, `{"sidebar_width":26,"sidebar_section_split":0.95}`} {
		if err := os.WriteFile(s.path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadLocalSettings("endpoint-preferences"); err == nil {
			t.Fatalf("accepted invalid state: %s", data)
		}
	}
}

// The phone's screen (a client with native_menus) starts hidden from its config every time and never writes the
// shared file, so hiding the sidebar there does not hide it on the PC's next client.
func TestHostAppClientKeepsViewPreferencesToItself(t *testing.T) {
	root := t.TempDir()
	t.Setenv("IKESU_STATE_DIR", filepath.Join(root, "state"))
	cfg := filepath.Join(root, "config.toml")
	t.Setenv(config.EnvVar, cfg)
	pc, err := loadLocalSettings("shared")
	if err != nil {
		t.Fatal(err)
	}
	shared := endpoint.ViewPreferences{SidebarWidth: 30, SidebarManual: true}
	if err := pc.save(shared); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, []byte("[ui]\nnative_menus = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	phone, err := loadLocalSettings("shared")
	if err != nil {
		t.Fatal(err)
	}
	if phone.preferences != nil {
		t.Fatalf("the phone's client loaded the shared preferences: %+v", phone.preferences)
	}
	hidden := endpoint.ViewPreferences{SidebarWidth: 30, SidebarCollapsed: true}
	if err := phone.save(hidden); err != nil || !reflect.DeepEqual(phone.preferences, &hidden) {
		t.Fatalf("in-memory save: %v %+v", err, phone.preferences)
	}
	stored, err := endpoint.LoadViewPreferences(pc.path)
	if err != nil || !reflect.DeepEqual(stored, &shared) {
		t.Fatalf("the shared file changed: %+v, %v", stored, err)
	}
}
