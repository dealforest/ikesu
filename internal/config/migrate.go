package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

// MigrateChange describes a default affected by the version upgrade.
type MigrateChange struct {
	Key     string
	V1Value string
	V2Value string
	Action  string
}

// MigratePlan describes the migration of one config file.
type MigratePlan struct {
	Path           string
	CurrentVersion int
	TargetVersion  int
	AlreadyCurrent bool
	Changes        []MigrateChange
}

type migrateDefault struct {
	key string
	v1  any
	v2  any
}

var migrateDefaults = []migrateDefault{
	{"guard.enabled", true, false},
	{"guard.mode", "enforce", "record"},
	{"decisions.screen_prompts", true, false},
	{"session.resume_agents_on_restore", true, false},
	{"ui.koi", true, false},
	{"ui.sound.enabled", true, false},
	{"ui.sidebar_plugins.enabled", true, false},
	{"integrations.claude_teams", true, false},
}

// PlanMigrate describes the defaults that would change when upgrading to v2.
func PlanMigrate(path string) (MigratePlan, error) {
	return PlanMigrateOptions(path, false)
}

// PlanMigrateOptions validates and describes a migration without writing the file.
func PlanMigrateOptions(path string, preserveV1 bool) (MigratePlan, error) {
	plan, _, err := prepareMigrate(path, preserveV1)
	return plan, err
}

// ApplyMigrate upgrades the config atomically, optionally pinning v1 defaults.
func ApplyMigrate(path string, preserveV1 bool) (MigratePlan, error) {
	plan, values, err := prepareMigrate(path, preserveV1)
	if err != nil || plan.AlreadyCurrent {
		return plan, err
	}
	return plan, writeConfigValues(path, values)
}

func prepareMigrate(path string, preserveV1 bool) (MigratePlan, map[string]any, error) {
	plan := MigratePlan{Path: path, CurrentVersion: CurrentConfigVersion, TargetVersion: CurrentConfigVersion}
	target, err := resolveWritePath(path)
	if err != nil {
		return plan, nil, err
	}
	data, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		plan.AlreadyCurrent = true
		return plan, nil, nil
	}
	if err != nil {
		return plan, nil, err
	}
	// Decode into an untyped table so settings outside Config survive migration.
	var values map[string]any
	if _, err := toml.Decode(string(data), &values); err != nil {
		return plan, nil, err
	}
	plan.CurrentVersion = ConfigVersionV1
	if raw, exists := values["config_version"]; exists {
		version, ok := raw.(int64)
		if !ok || version < ConfigVersionV1 || version > CurrentConfigVersion {
			return plan, nil, fmt.Errorf("unsupported config_version: %v", raw)
		}
		plan.CurrentVersion = int(version)
	}
	plan.AlreadyCurrent = plan.CurrentVersion == plan.TargetVersion
	if plan.AlreadyCurrent {
		return plan, nil, nil
	}
	if values == nil {
		values = make(map[string]any)
	}
	values["config_version"] = plan.TargetVersion
	for _, change := range migrateDefaults {
		table, key, err := migrationTable(values, change.key, preserveV1)
		if err != nil {
			return plan, nil, err
		}
		if value, explicit := table[key]; explicit {
			// GuardConfig treats an empty mode as an instruction to use the default.
			emptyMode := change.key == "guard.mode" && value == ""
			if !emptyMode {
				if validMigrationValue(change, value) {
					continue
				}
				if preserveV1 {
					return plan, nil, fmt.Errorf("cannot preserve the v1 default for %s until its invalid value is corrected", change.key)
				}
			}
		}
		action := "adopt_v2_default"
		if preserveV1 {
			table[key] = change.v1
			action = "preserve_explicit"
		}
		plan.Changes = append(plan.Changes, MigrateChange{
			Key: change.key, V1Value: fmt.Sprint(change.v1), V2Value: fmt.Sprint(change.v2), Action: action,
		})
	}
	return plan, values, nil
}

func validMigrationValue(change migrateDefault, value any) bool {
	if change.key == "guard.mode" {
		_, ok := value.(string)
		return ok
	}
	_, ok := value.(bool)
	return ok
}

func migrationTable(root map[string]any, path string, create bool) (map[string]any, string, error) {
	keys := strings.Split(path, ".")
	table := root
	for _, key := range keys[:len(keys)-1] {
		value, exists := table[key]
		if !exists {
			if !create {
				return nil, keys[len(keys)-1], nil
			}
			value = make(map[string]any)
			table[key] = value
		}
		next, ok := value.(map[string]any)
		if !ok {
			if !create {
				return nil, keys[len(keys)-1], nil
			}
			return nil, "", fmt.Errorf("cannot preserve %s because %s is not a table", path, key)
		}
		table = next
	}
	return table, keys[len(keys)-1], nil
}
