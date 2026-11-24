package cli

import (
	"encoding/json"
	"fmt"

	"github.com/kazuph/ikesu/internal/config"
)

const configUsage = "usage: ikesu config check | ikesu config notify --json | ikesu config migrate [--write] [--preserve-v1]"

func (r *runner) configCLI(args []string) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(r.out(), configUsage)
		return 0
	}
	if len(args) > 0 && args[0] == "migrate" {
		return r.configMigrate(args[1:])
	}
	if len(args) == 2 && args[0] == "notify" && args[1] == "--json" {
		return r.configNotify()
	}
	if len(args) != 1 || args[0] != "check" {
		return usageErr(r, configUsage)
	}
	c, err := config.Load()
	fmt.Fprintf(r.out(), "config_version: v%d\n", c.EffectiveConfigVersion())
	if err == nil && len(c.Warnings) == 0 {
		fmt.Fprintln(r.out(), "config: ok")
		return 0
	}
	fmt.Fprintln(r.out(), "config: issues found")
	if err != nil {
		fmt.Fprintf(r.out(), "config parse error: %v; using defaults\n", err)
	}
	for _, warning := range c.Warnings {
		fmt.Fprintln(r.out(), warning)
	}
	return 1
}

func (r *runner) configMigrate(args []string) int {
	write, preserveV1 := false, false
	for _, arg := range args {
		switch arg {
		case "--write":
			write = true
		case "--preserve-v1":
			preserveV1 = true
		case "--help", "-h":
			fmt.Fprintln(r.out(), configUsage)
			return 0
		default:
			return usageErr(r, configUsage)
		}
	}

	var plan config.MigratePlan
	var err error
	if write {
		plan, err = config.ApplyMigrate(config.Path(), preserveV1)
	} else {
		plan, err = config.PlanMigrateOptions(config.Path(), preserveV1)
	}
	if err != nil {
		fmt.Fprintln(r.errOut(), err)
		return 1
	}
	if plan.AlreadyCurrent {
		fmt.Fprintf(r.out(), "%s: already uses v%d defaults\n", plan.Path, plan.TargetVersion)
		return 0
	}
	fmt.Fprintf(r.out(), "--- %s (v%d)\n+++ %s (v%d)\n", plan.Path, plan.CurrentVersion, plan.Path, plan.TargetVersion)
	fmt.Fprintf(r.out(), "+ config_version = %d\n", plan.TargetVersion)
	for _, change := range plan.Changes {
		if preserveV1 {
			fmt.Fprintf(r.out(), "+ %s = %s (preserves v1 default)\n", change.Key, change.V1Value)
			continue
		}
		fmt.Fprintf(r.out(), "- %s = %s\n+ %s = %s\n", change.Key, change.V1Value, change.Key, change.V2Value)
	}
	if !write {
		fmt.Fprintln(r.out(), "Dry run. Use --write to save, or --write --preserve-v1 to keep v1 behavior.")
	}
	return 0
}

// configNotify は config.toml の [notify] を JSON で出す（Slack のプラグインが読む）。
func (r *runner) configNotify() int {
	c, err := config.Load()
	if err != nil {
		fmt.Fprintf(r.errOut(), "config parse error: %v\n", err)
		return 1
	}
	raw, err := json.Marshal(c.NotifyJSON())
	if err != nil {
		fmt.Fprintln(r.errOut(), err)
		return 1
	}
	fmt.Fprintln(r.out(), string(raw))
	return 0
}
