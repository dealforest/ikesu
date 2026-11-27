package guard

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These cases run real Bash and zsh shells against the real guard CLI. Each
// dangerous line removes the disposable victim through /bin/rm, so a PATH
// shim cannot be what stops it: the victim survives only if the line itself
// was judged and rejected.

const trapVictim = `"$IKESU_RUNTIME_ROOT/home/../victim"`

func eachBash(t *testing.T, run func(t *testing.T, shell string)) {
	for name, shell := range map[string]string{"system": "/bin/bash", "homebrew": "/opt/homebrew/bin/bash"} {
		t.Run(name, func(t *testing.T) {
			if _, err := os.Stat(shell); err != nil {
				if name == "homebrew" && os.IsNotExist(err) {
					t.Skip("Homebrew bashがないため、この版だけ未実施")
				}
				t.Fatal(err)
			}
			run(t, shell)
		})
	}
}

// runGuardEntry runs script as a file, as a -c string, or from standard input.
func runGuardEntry(t *testing.T, root, shell string, env, options []string, mode, script string) (error, string) {
	t.Helper()
	if mode == "file" {
		return runGuardOptionsScript(t, root, shell, env, options, script)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	args := append([]string{}, options...)
	if mode == "c" {
		args = append(args, "-c", script)
	}
	cmd := exec.CommandContext(ctx, shell, args...)
	if mode == "stdin" {
		cmd.Stdin = strings.NewReader(script)
	}
	cmd.Env, cmd.Dir = env, root
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("shell timed out: %v\n%s", err, output)
	}
	return err, string(output)
}

func writeOriginal(t *testing.T, root, body string) string {
	t.Helper()
	orig := filepath.Join(root, "original")
	if err := os.WriteFile(orig, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return orig
}

// zshGuardEnv prepares a zsh whose own startup files are read from root.
func zshGuardEnv(t *testing.T, root, binary, zshenv string) []string {
	t.Helper()
	if zshenv != "" {
		if err := os.WriteFile(filepath.Join(root, ".zshenv"), []byte(zshenv), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := EnvMap(prepareGuardOptionsFixture(t, root, binary, "/bin/zsh"))
	delete(env, "IKESU_GUARD_ZDOTDIR_SET")
	delete(env, "IKESU_GUARD_ZDOTDIR_ORIG")
	delete(env, "IKESU_GUARD_BASH_ENV_ORIG")
	return environment(env)
}

func assertVictimKept(t *testing.T, root, output string) {
	t.Helper()
	if data, err := os.ReadFile(filepath.Join(root, "victim")); err != nil || string(data) != "unchanged" {
		t.Fatalf("victim changed: %q, %v\n%s", data, err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "after")); !os.IsNotExist(err) {
		t.Fatalf("post-failure line ran: %v\n%s", err, output)
	}
}

// 1. A user BASH_ENV cut short by an expansion error, or by its own set -u,
// still leaves every line of the script, -c string and standard input judged.
func TestBashRuntimeTruncatedBashEnv(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	dangerous := "/bin/rm " + trapVictim + "\nprintf after > \"$IKESU_RUNTIME_ROOT/after\"\n"
	eachBash(t, func(t *testing.T, shell string) {
		for name, orig := range map[string]string{
			"required":    ": \"${UNSET_IN_ORIG:?missing}\"\nORIG_TAIL=1\n",
			"own-nounset": "set -u\n: \"$UNDEF_IN_ORIG\"\nORIG_TAIL=1\n",
		} {
			for _, mode := range []string{"file", "c", "stdin"} {
				t.Run(name+"/"+mode, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, orig))
					err, output := runGuardEntry(t, root, shell, env, nil, mode, dangerous)
					assertGuardOptionsStopped(t, root, err, output)
				})
			}
		}
	})
}

// zsh ends the shell on a failed NAME:? expansion; an unset variable under
// nounset only ends the user's file. Neither lets the script run unjudged.
func TestZshRuntimeTruncatedStartup(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	dangerous := "/bin/rm " + trapVictim + "\nprintf after > \"$IKESU_RUNTIME_ROOT/after\"\n"
	for name, zshenv := range map[string]string{
		"required": ": \"${UNSET_IN_ORIG:?missing}\"\n",
		"nounset":  "setopt nounset\n: \"$UNDEF_IN_ORIG\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			env := zshGuardEnv(t, root, binary, zshenv)
			err, output := runGuardOptionsScript(t, root, "/bin/zsh", env, nil, dangerous)
			if err == nil {
				t.Fatalf("zsh ran the script: %s", output)
			}
			assertVictimKept(t, root, output)
		})
	}
}

// 2. A working BASH_ENV keeps its ERR trap, its function and its own set -u,
// and none of its lines are sent for approval.
func TestBashRuntimeOrdinaryBashEnv(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	orig := `false || true
false
function git { printf 'user git\n' > "$IKESU_RUNTIME_ROOT/git-fn"; }
trap 'printf "%s\n" "${PHASE-orig}" >> "$IKESU_RUNTIME_ROOT/err-trap"' ERR
set -u
ORIGINAL_LOADER_SEEN=1
false
`
	safe := `[[ $ORIGINAL_LOADER_SEEN == 1 && $- == *u* ]] || exit 71
git
x=1
PHASE=body
false
printf 'safe ran\n' > "$IKESU_RUNTIME_ROOT/result"
`
	eachBash(t, func(t *testing.T, shell string) {
		for _, mode := range []string{"file", "c", "stdin"} {
			t.Run(mode, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, orig))
				err, output := runGuardEntry(t, root, shell, env, nil, mode, safe)
				if err != nil || strings.Contains(output, "申請文") {
					t.Fatalf("safe script: %v\n%s", err, output)
				}
				for file, want := range map[string]string{"result": "safe ran\n", "git-fn": "user git\n"} {
					if data, err := os.ReadFile(filepath.Join(root, file)); err != nil || string(data) != want {
						t.Fatalf("%s=%q err=%v\n%s", file, data, err, output)
					}
				}
				// The loader reads the file in a || list, where Bash 5 runs no
				// ERR trap. The body's failure runs it in every version.
				if data, err := os.ReadFile(filepath.Join(root, "err-trap")); err != nil || !strings.HasSuffix(string(data), "body\n") {
					t.Fatalf("err-trap=%q err=%v\n%s", data, err, output)
				}
			})
		}
	})
}

// 3. EXIT trap bodies run under the DEBUG trap, however they are written.
func TestBashRuntimeExitTrapJudged(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		for name, script := range map[string]string{
			"inline":   "T=" + trapVictim + "\ntrap '/bin/rm \"$T\"' EXIT\n",
			"zero":     "T=" + trapVictim + "\ntrap '/bin/rm \"$T\"' 0\n",
			"function": "T=" + trapVictim + "\ncleanup() { /bin/rm \"$T\"; }\ntrap cleanup EXIT\n",
			"nested":   "helper() { /bin/rm \"$T\"; }\ncleanup() { helper; }\nT=" + trapVictim + "\ntrap cleanup EXIT\n",
			"later":    "T=\"$IKESU_RUNTIME_ROOT/safe\"\ntrap '/bin/rm \"$T\"' EXIT\nT=" + trapVictim + "\n",
			"exit":     "T=" + trapVictim + "\ntrap '/bin/rm \"$T\"' EXIT\nexit 3\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := prepareGuardOptionsFixture(t, root, binary, shell)
				err, output := runGuardOptionsScript(t, root, shell, env, nil, script)
				assertGuardOptionsStopped(t, root, err, output)
			})
		}
	})
}

// A safe EXIT trap runs and leaves the same exit status and trap -p output
// as the same script in a Bash without the guard.
func TestBashRuntimeExitTrapKeepsBehavior(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		for name, script := range map[string]string{
			"status":       "trap 'printf \"ran %s\" \"$?\" > \"$OUT/result\"' EXIT\nexit 42\n",
			"trap-exit":    "trap 'printf ran > \"$OUT/result\"; exit 7' EXIT\nexit 42\n",
			"bare-exit":    "trap 'printf ran > \"$OUT/result\"; false; exit' EXIT\ntrue\n",
			"fall-off":     "trap 'printf ran > \"$OUT/result\"; false' EXIT\n(exit 5)\n",
			"function":     "cleanup() { printf 'ran %s' \"$1\" > \"$OUT/result\"; }\ntrap 'cleanup it'\\''s' EXIT\nexit 2\n",
			"show":         "trap 'printf ran > \"$OUT/result\"' EXIT\ntrap -p EXIT > \"$OUT/shown\"\ntrap > \"$OUT/listed\"\n",
			"reset":        "trap 'printf ran > \"$OUT/result\"' EXIT\ntrap - EXIT\ntrap -p > \"$OUT/shown\"\nexit 4\n",
			"substitution": "trap 'printf ran > \"$OUT/result\"' EXIT\nX=$(trap -p EXIT)\nprintf '%s' \"$X\" > \"$OUT/shown\"\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				plainDir, guardedDir := t.TempDir(), t.TempDir()
				plain := exec.Command(shell, "-c", script)
				plain.Env = []string{"PATH=/usr/bin:/bin", "OUT=" + plainDir}
				plainOut, plainErr := plain.CombinedOutput()
				root := t.TempDir()
				env := append(prepareGuardOptionsFixture(t, root, binary, shell), "OUT="+guardedDir)
				err, output := runGuardOptionsScript(t, root, shell, env, nil, script)
				if exitCode(err) != exitCode(plainErr) {
					t.Fatalf("exit status: guarded %v, plain %v\n%s\n%s", err, plainErr, output, plainOut)
				}
				for _, file := range []string{"result", "shown", "listed"} {
					want, wantErr := os.ReadFile(filepath.Join(plainDir, file))
					got, gotErr := os.ReadFile(filepath.Join(guardedDir, file))
					if gotErr == nil {
						// The guarded shell also lists its own DEBUG trap.
						got = bytes.Join(filterLines(got, "DEBUG"), []byte("\n"))
						want = bytes.Join(filterLines(want, "DEBUG"), []byte("\n"))
					}
					if (wantErr == nil) != (gotErr == nil) || !bytes.Equal(want, got) {
						t.Fatalf("%s: guarded %q (%v), plain %q (%v)\n%s", file, got, gotErr, want, wantErr, output)
					}
				}
			})
		}
	})
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode()
	}
	return -1
}

func filterLines(data []byte, drop string) [][]byte {
	var kept [][]byte
	for _, line := range bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n")) {
		if !bytes.HasSuffix(line, []byte(" "+drop)) {
			kept = append(kept, line)
		}
	}
	return kept
}

// 4. zsh runs no TRAPDEBUG inside a trap, so an EXIT trap is judged when it is
// registered. A function call cannot be judged then and is asked.
func TestZshRuntimeExitTrapJudged(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	for name, script := range map[string]string{
		"inline":   "T=" + trapVictim + "\ntrap '/bin/rm \"$T\"' EXIT\n",
		"literal":  "trap '/bin/rm VICTIM' EXIT\n",
		"function": "TRAPEXIT() { /bin/rm \"$T\"; }\nT=" + trapVictim + "\n",
		"call":     "T=" + trapVictim + "\ncleanup() { /bin/rm \"$T\"; }\ntrap cleanup EXIT\n",
		"builtin":  "T=" + trapVictim + "\nbuiltin trap '/bin/rm \"$T\"' EXIT\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			env := zshGuardEnv(t, root, binary, "")
			script = strings.ReplaceAll(script, "VICTIM", root+"/home/../victim")
			err, output := runGuardOptionsScript(t, root, "/bin/zsh", env, nil, script+"printf after > \"$IKESU_RUNTIME_ROOT/after\"\n")
			assertGuardOptionsStopped(t, root, err, output)
		})
	}
	t.Run("safe-inline", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		env := zshGuardEnv(t, root, binary, "")
		result := filepath.Join(root, "result")
		err, output := runGuardOptionsScript(t, root, "/bin/zsh", env, nil, "trap 'printf ran > "+quote(result)+"' EXIT\nexit 3\n")
		if exitCode(err) != 3 {
			t.Fatalf("exit status %v\n%s", err, output)
		}
		if data, err := os.ReadFile(result); err != nil || string(data) != "ran" {
			t.Fatalf("result=%q err=%v\n%s", data, err, output)
		}
	})
}

// 5. ERR and RETURN traps from the user's BASH_ENV are checked each time they
// run, with the current variables, before their text runs in place.
func TestBashRuntimeStartupErrReturnTrapsJudged(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		type trapCase struct {
			orig, option, script string
		}
		cases := map[string]trapCase{}
		for _, option := range []string{"-E", "-eE"} {
			for _, invocation := range []string{"git status", "command git status", "command false"} {
				for body, text := range map[string]string{
					"rm":     "/bin/rm " + trapVictim,
					"marker": "printf forged >> \"$IKESU_GUARD_MARKER\"",
					"second": ":; /bin/rm " + trapVictim,
				} {
					cases["ERR/"+option+"/"+invocation+"/"+body] = trapCase{"trap " + quote(text) + " ERR\n", option, invocation}
				}
			}
		}
		for body, text := range map[string]string{"rm": "/bin/rm " + trapVictim, "second": ":; /bin/rm " + trapVictim} {
			cases["RETURN/"+body] = trapCase{"trap " + quote(text) + " RETURN\n", "", "f() { :; }\nf"}
		}
		for name, tc := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, tc.orig))
				marker := EnvMap(env)["IKESU_GUARD_MARKER"]
				before, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				var options []string
				if tc.option != "" {
					options = []string{tc.option}
				}
				err, output := runGuardOptionsScript(t, root, shell, env, options, tc.script+"\nprintf after > \"$IKESU_RUNTIME_ROOT/after\"\n")
				// Bash 5 itself runs no ERR trap for a failure returned
				// through a function under -e; then the shell only stops.
				if err == nil {
					t.Fatalf("shell went on: %s", output)
				}
				assertVictimKept(t, root, output)
				if after, err := os.ReadFile(marker); err != nil || !bytes.Equal(before, after) {
					t.Fatalf("guard marker changed: %v\n%s", err, output)
				}
			})
		}
	})
}

// The guard's own functions return without running the user's RETURN trap.
func TestBashRuntimeReturnTrapSkipsGuardFunctions(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		t.Parallel()
		root := t.TempDir()
		orig := "trap 'printf \"%s\\n\" \"${FUNCNAME[0]}\" >> \"$IKESU_RUNTIME_ROOT/returns\"' RETURN\n"
		env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, orig))
		err, output := runGuardOptionsScript(t, root, shell, env, nil, "git status\ncommand git status\ntrap 'printf x' USR1\nf() { :; }\nf\n")
		if err != nil {
			t.Fatalf("%v\n%s", err, output)
		}
		if data, err := os.ReadFile(filepath.Join(root, "returns")); err != nil || string(data) != "f\n" {
			t.Fatalf("RETURN trap ran for %q, %v\n%s", data, err, output)
		}
	})
}

// zsh ZERR traps and TRAPZERR functions from .zshenv are checked before they run.
func TestZshRuntimeStartupErrTrapJudged(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	for name, zshenv := range map[string]string{
		"list":     "trap '/bin/rm " + trapVictim + "' ZERR\n",
		"function": "TRAPZERR() { /bin/rm " + trapVictim + "; }\n",
		"marker":   "trap 'printf forged >> \"$IKESU_GUARD_MARKER\"' ZERR\n",
	} {
		for _, invocation := range []string{"false", "git status", "command git status"} {
			t.Run(name+"/"+invocation, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := zshGuardEnv(t, root, binary, zshenv)
				marker := EnvMap(env)["IKESU_GUARD_MARKER"]
				before, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				err, output := runGuardOptionsScript(t, root, "/bin/zsh", env, nil, invocation+"\nprintf after > \"$IKESU_RUNTIME_ROOT/after\"\n")
				assertGuardOptionsStopped(t, root, err, output)
				if after, err := os.ReadFile(marker); err != nil || !bytes.Equal(before, after) {
					t.Fatalf("guard marker changed: %v\n%s", err, output)
				}
			})
		}
	}
}

// 6. Calling a forwarding function directly is judged like any other line.
func TestShellRuntimeDirectWrapperCall(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	script := "_ikesu_guard_command /bin/rm " + trapVictim + "\nprintf after > \"$IKESU_RUNTIME_ROOT/after\"\n"
	eachBash(t, func(t *testing.T, shell string) {
		t.Parallel()
		root := t.TempDir()
		env := prepareGuardOptionsFixture(t, root, binary, shell)
		err, output := runGuardOptionsScript(t, root, shell, env, nil, script)
		assertGuardOptionsStopped(t, root, err, output)
	})
	t.Run("zsh", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		env := zshGuardEnv(t, root, binary, "")
		err, output := runGuardOptionsScript(t, root, "/bin/zsh", env, nil, script)
		assertGuardOptionsStopped(t, root, err, output)
	})
}
