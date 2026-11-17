package client

import (
	"fmt"

	"github.com/kazuph/ikesu/internal/api"
	"github.com/kazuph/ikesu/internal/config"
	"github.com/kazuph/ikesu/internal/endpoint"
	"github.com/kazuph/ikesu/internal/input"
)

type localSettings struct {
	clientVersion string
	upgrade       *upgradeCandidate
	upgradeState  *upgradeState
	retryReader   *machineInputState
	retryHeld     []input.Event
	retryLocal    []byte
	retryQueued   map[string]queuedUpgradeInput
	session       string
	config        *config.Config
	warning       string
	preferences   *endpoint.ViewPreferences
	path          string
}

type queuedUpgradeInput struct {
	target, session string
	messages        []msg
}

func (s *localSettings) apply(edit config.Edit) (config.Config, error) {
	var current config.Config
	if s.config != nil {
		current = *s.config
	}
	return edit.ApplyTo(current)
}

func loadLocalSettings(session string) (*localSettings, error) {
	if !api.ValidSessionName(session) {
		return nil, fmt.Errorf("invalid client session name")
	}
	s := &localSettings{session: session, path: endpoint.ViewPreferencesPath(session)}
	c, err := config.Load()
	if err != nil {
		s.warning = "client configuration: " + err.Error()
	} else {
		s.config = &c
	}
	// A client inside a host app (the phone's screen) starts from its own config every time and keeps what it
	// changes (the sidebar, its width) to itself: sharing this file would carry the phone's layout to the PC's client.
	if s.config != nil && s.config.UI.NativeMenus {
		s.path = ""
		return s, nil
	}
	preferences, err := endpoint.LoadViewPreferences(s.path)
	if err != nil {
		return nil, err
	}
	s.preferences = preferences
	return s, nil
}

func (s *localSettings) save(preferences endpoint.ViewPreferences) error {
	if s.path == "" {
		s.preferences = &preferences
		return nil
	}
	if err := endpoint.SaveViewPreferences(s.path, preferences); err != nil {
		return err
	}
	s.preferences = &preferences
	return nil
}
