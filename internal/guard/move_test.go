package guard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMovePaths(t *testing.T) {
	f := newGlobFixture(t)
	f.in.Env["TMPDIR"] = filepath.Join(f.root, "temporary")
	for _, dir := range []string{f.in.Env["TMPDIR"], filepath.Join(f.wt, ".artifacts"), filepath.Join(f.root, "Library")} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{
		f.wt + "/out/plain.txt":                f.other + "/sub/f",
		f.root + "/Library/link":               f.wt + "/plain.txt",
		f.wt + "/outside-parent":               f.root + "/Library",
		f.wt + "/.artifacts/outside-parent":    f.root + "/Library",
		f.in.Env["TMPDIR"] + "/outside-parent": f.root + "/Library",
		f.root + "/Library/temp-parent":        f.in.Env["TMPDIR"],
		f.root + "/Library/artifact-parent":    f.wt + "/.artifacts",
		f.in.Env["TMPDIR"] + "/external-leaf":  f.other + "/sub/f",
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		ask  bool
		args []string
	}{
		{"inside", false, []string{"plain.txt", "renamed"}},
		{"tmp destination", false, []string{"plain.txt", t.TempDir()}},
		{"tmp source", false, []string{filepath.Join(t.TempDir(), "src"), "renamed"}},
		{"TMPDIR", false, []string{"plain.txt", f.in.Env["TMPDIR"]}},
		{"artifacts", false, []string{"plain.txt", ".artifacts"}},
		{"outside source", true, []string{f.root + "/Library/file", "renamed"}},
		{"outside destination", true, []string{"plain.txt", f.root + "/Library"}},
		{"other repo source", true, []string{f.other + "/sub/f", "renamed"}},
		{"other repo destination", true, []string{"plain.txt", f.other + "/sub"}},
		{"HOME source", true, []string{f.in.Env["HOME"], "renamed"}},
		{"HOME destination", true, []string{"plain.txt", f.in.Env["HOME"]}},
		{"worktree source", true, []string{f.wt, "renamed"}},
		{"git source", true, []string{".git", "renamed"}},
		{"git destination", true, []string{"plain.txt", ".git"}},
		{"git directory destination", true, []string{"plain.txt", f.repo + "/.git"}},
		{"current directory", false, []string{"plain.txt", "."}},
		{"destination child link stays inside", false, []string{"plain.txt", "out"}},
		{"source link stays inside", false, []string{"out/plain.txt", "renamed"}},
		{"source link stays outside", true, []string{f.root + "/Library/link", "renamed"}},
		{"destination link stays outside", true, []string{"plain.txt", f.root + "/Library/link"}},
		{"source parent resolves outside", true, []string{"outside-parent/link", "renamed"}},
		{"destination parent resolves outside", true, []string{"plain.txt", "outside-parent/new"}},
		{"artifacts parent resolves outside", true, []string{"plain.txt", ".artifacts/outside-parent/new"}},
		{"TMPDIR parent resolves outside", true, []string{"plain.txt", f.in.Env["TMPDIR"] + "/outside-parent/new"}},
		{"parent resolves into TMPDIR", false, []string{"plain.txt", f.root + "/Library/temp-parent/new"}},
		{"parent resolves into artifacts", false, []string{"plain.txt", f.root + "/Library/artifact-parent/new"}},
		{"source link stays in TMPDIR", false, []string{f.in.Env["TMPDIR"] + "/external-leaf", "renamed"}},
		{"short target directory", false, []string{"-t", "out", "out/a.jpg", "out/b.jpg"}},
		{"short attached target directory", false, []string{"-vftout", "out/*.jpg"}},
		{"long target directory", false, []string{"--target-directory=out", "out/a.jpg"}},
		{"long target directory separate", false, []string{"--target-directory", "out", "out/a.jpg"}},
		{"target directory outside", true, []string{"-t", f.other, "out/a.jpg"}},
		{"target directory missing", true, []string{"-t", "missing", "out/a.jpg"}},
		{"end of options", false, []string{"--", "-odd", "-v"}},
		{"option after first operand", true, []string{"plain.txt", "-f", "."}},
		{"unknown option", true, []string{"--unknown", "plain.txt", "renamed"}},
		{"unknown short option", true, []string{"-vfY", "plain.txt", "renamed"}},
		{"flags without values", false, []string{"-f", "-i", "-n", "-v", "-h", "plain.txt", "backup"}},
		{"short suffix value", false, []string{"-S", "--suffix-looking", "plain.txt", "old.bak"}},
		{"long suffix value", false, []string{"--suffix=~", "plain.txt", "old.bak"}},
		{"long suffix separate", false, []string{"--suffix", "~-old", "plain.txt", "old.bak"}},
		{"source glob sees .git", true, []string{".g*", "renamed"}},
		{"multiple sources include an outside path", true, []string{"plain.txt", f.other + "/sub/f", "."}},
		{"parent segments", true, []string{"plain.txt", "out/../renamed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			i := f.in
			i.Command, i.Args = "mv", tc.args
			got := Evaluate(Config{}, i)
			if tc.ask {
				if got.Decision != "ask" || got.Rule != "mv-paths" || got.Tier != TierL3 {
					t.Fatalf("expected mv-paths L3 for %q, got %+v", tc.args, got)
				}
			} else if got.Decision != "allow" {
				t.Fatalf("expected allow for %q, got %+v", tc.args, got)
			}
		})
	}
}
