package guard

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// These cases run real Bash and zsh shells against the real guard CLI. Each
// dangerous line removes the disposable victim through /bin/rm, so a PATH
// shim cannot be what stops it: the victim survives only if the line itself
// was judged and rejected.

// origTail is the last line of a user BASH_ENV in these tests. Its file tells a
// cut-short file (absent) from one that ran to the end (present).
const origTail = "printf tail > \"$IKESU_RUNTIME_ROOT/orig-tail\"\n"

func assertOrigTail(t *testing.T, root string, want bool, output string) {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, "orig-tail"))
	if (err == nil) != want {
		t.Fatalf("user file reached its last line: %v, want %v\n%s", err == nil, want, output)
	}
}

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
			"required":    ": \"${UNSET_IN_ORIG:?missing}\"\n" + origTail,
			"own-nounset": "set -u\n: \"$UNDEF_IN_ORIG\"\n" + origTail,
		} {
			for _, mode := range []string{"file", "c", "stdin"} {
				t.Run(name+"/"+mode, func(t *testing.T) {
					t.Parallel()
					root := t.TempDir()
					env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, orig))
					err, output := runGuardEntry(t, root, shell, env, nil, mode, dangerous)
					assertGuardOptionsStopped(t, root, err, output)
					assertOrigTail(t, root, false, output)
				})
			}
		}
		// BASH_ENV may name the generated file directly, without the loader.
		for _, mode := range []string{"c", "stdin"} {
			t.Run("direct/"+mode, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := EnvMap(prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, ": \"${UNSET_IN_ORIG:?missing}\"\n"+origTail)))
				env["BASH_ENV"] += ".current"
				err, output := runGuardEntry(t, root, shell, environment(env), nil, mode, dangerous)
				assertGuardOptionsStopped(t, root, err, output)
				assertOrigTail(t, root, false, output)
			})
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
		"required": ": \"${UNSET_IN_ORIG:?missing}\"\n" + origTail,
		"nounset":  "setopt nounset\n: \"$UNDEF_IN_ORIG\"\n" + origTail,
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
			assertOrigTail(t, root, false, output)
		})
	}
}

// 2. A working BASH_ENV keeps its ERR trap, its function and its own set -u,
// runs to its last line, and none of its lines are sent for approval. Bash
// 3.2 reading the script from standard input cannot tell where the user's
// file ends, so there its lines are judged and its trap … ERR line is asked.
func TestBashRuntimeOrdinaryBashEnv(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	orig := `false || true
false
URL="${UNSET_URL:-https://example.com/?a=1}"
TRIMMED="${URL#?}"
function git { printf 'user git\n' > "$IKESU_RUNTIME_ROOT/git-fn"; }
trap 'printf "%s\n" "${PHASE-orig}" >> "$IKESU_RUNTIME_ROOT/err-trap"' ERR
set -u
ORIGINAL_LOADER_SEEN=1
false
` + origTail
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
				if shell == "/bin/bash" && mode == "stdin" {
					if err == nil || !strings.Contains(output, "申請文") {
						t.Fatalf("bash 3.2 stdin: expected the user file to be judged: %v\n%s", err, output)
					}
					return
				}
				if err != nil || strings.Contains(output, "申請文") {
					t.Fatalf("safe script: %v\n%s", err, output)
				}
				assertOrigTail(t, root, true, output)
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

// 3. An EXIT text is judged when it is registered; the shell runs it itself.
// A text whose commands or arguments may change before it runs is asked.
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
			"literal":  "trap '/bin/rm VICTIM' EXIT\nexit 3\n",
			"shadowed": "echo() { /bin/rm VICTIM; }\ntrap 'echo ok' EXIT\n",
			"sh-c":     "/bin/sh -c \"trap '/bin/rm VICTIM' EXIT\"\n",
			"dash-c":   "/bin/dash -c \"trap '/bin/rm VICTIM' EXIT\"\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := prepareGuardOptionsFixture(t, root, binary, shell)
				script = strings.ReplaceAll(script, "VICTIM", root+"/home/../victim")
				err, output := runGuardOptionsScript(t, root, shell, env, nil, script+"printf after > \"$IKESU_RUNTIME_ROOT/after\"\n")
				assertGuardOptionsStopped(t, root, err, output)
			})
		}
	})
}

// A safe EXIT trap leaves the same exit status, output and trap listing as
// the same script in a Bash without the guard. The paths are literal: a text
// whose values may change before it runs is asked instead.
func TestBashRuntimeExitTrapKeepsBehavior(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		for name, script := range map[string]string{
			"status":       "trap 'printf ran > OUT/result' EXIT\nexit 42\n",
			"trap-exit":    "trap 'printf ran > OUT/result; exit 7' EXIT\nexit 42\n",
			"bare-exit":    "trap 'printf ran > OUT/result; false; exit' EXIT\ntrue\n",
			"fall-off":     "trap 'printf ran > OUT/result; false' EXIT\n(exit 5)\n",
			"errexit":      "set -e\ntrap 'printf a > OUT/result; false; printf b >> OUT/result' EXIT\nexit 23\n",
			"arguments":    "set -- one two\ntrap 'printf ran > OUT/result' EXIT\nexit 0\n",
			"show":         "trap 'printf ran > OUT/result' EXIT\ntrap -p EXIT > OUT/shown\ntrap > OUT/listed\n",
			"reset":        "trap 'printf ran > OUT/result' EXIT\ntrap - EXIT\ntrap -p > OUT/shown\nexit 4\n",
			"number-reset": "trap 'printf ran > OUT/result' EXIT\ntrap 0 EXIT\ntrap -p > OUT/shown\nexit 4\n",
			"double-zero":  "trap 'printf ran > OUT/result' 00\ntrap -p EXIT > OUT/shown\n",
			"substitution": "trap 'printf ran > OUT/result' EXIT\nX=$(trap -p EXIT)\nprintf '%s' \"$X\" > OUT/shown\n",
			"print-action": "trap 'printf ran > OUT/result' EXIT\ntrap -P EXIT > OUT/shown 2>&1\nprintf '%s' \"$?\" > OUT/listed\n",
			"blank-lines":  "trap 'printf a > OUT/result\n\nprintf b >> OUT/result' EXIT\ntrap -p > OUT/shown\ntrap > OUT/listed\n",
			"unrelated":    "trap 'printf ran > OUT/result' EXIT\nhelper() { :; }\nhelper\nexit 6\n",
			"signal":       "trap 'printf usr1 > OUT/result' USR1\nkill -USR1 $$\ntrap -p USR1 > OUT/shown\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				plainDir, guardedDir := t.TempDir(), t.TempDir()
				plainScript := script
				if name == "bare-exit" {
					// With any DEBUG trap, Bash gives a bare exit in a trap the
					// status of the trap's last command. The guard keeps one.
					plainScript = "trap : DEBUG\n" + script
				}
				plain := exec.Command(shell, "-c", strings.ReplaceAll(plainScript, "OUT", plainDir))
				plain.Env = []string{"PATH=/usr/bin:/bin"}
				plainOut, plainErr := plain.CombinedOutput()
				root := t.TempDir()
				env := prepareGuardOptionsFixture(t, root, binary, shell)
				err, output := runGuardOptionsScript(t, root, shell, env, nil, strings.ReplaceAll(script, "OUT", guardedDir))
				assertSameRun(t, plainDir, guardedDir, err, plainErr, output, string(plainOut))
			})
		}
	})
}

// assertSameRun compares the exit status and the files of a guarded run with
// a run without the guard. The guard's own DEBUG trap and its messages are left
// out of the comparison.
func assertSameRun(t *testing.T, plainDir, guardedDir string, err, plainErr error, output, plainOut string) {
	t.Helper()
	if exitCode(err) != exitCode(plainErr) || strings.Contains(output, "申請文") {
		t.Fatalf("exit status: guarded %v, plain %v\n%s\n%s", err, plainErr, output, plainOut)
	}
	// The whole output matches too, but for where a builtin's error points.
	got := errorPlace.ReplaceAllString(strings.ReplaceAll(output, guardedDir, plainDir), "trap:")
	if want := errorPlace.ReplaceAllString(plainOut, "trap:"); got != want {
		t.Fatalf("output: guarded %q, plain %q", got, want)
	}
	for _, file := range []string{"result", "shown", "listed"} {
		want, wantErr := os.ReadFile(filepath.Join(plainDir, file))
		got, gotErr := os.ReadFile(filepath.Join(guardedDir, file))
		got = bytes.ReplaceAll(got, []byte(guardedDir), []byte(plainDir))
		// An error of the trap builtin names the line that called it. In the
		// guarded shell that is the line of the trap wrapper.
		got, want = errorPlace.ReplaceAll(got, []byte("trap:")), errorPlace.ReplaceAll(want, []byte("trap:"))
		if gotErr == nil {
			got = bytes.Join(filterLines(got, "DEBUG"), []byte("\n"))
			want = bytes.Join(filterLines(want, "DEBUG"), []byte("\n"))
		}
		if (wantErr == nil) != (gotErr == nil) || !bytes.Equal(want, got) {
			t.Fatalf("%s: guarded %q (%v), plain %q (%v)\n%s", file, got, gotErr, want, wantErr, output)
		}
	}
}

var errorPlace = regexp.MustCompile(`(?m)^.*: line [0-9]+: trap:`)

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

// In Bash a TRAP<SIG> name is an ordinary function, not a trap.
func TestBashRuntimeTrapNamedFunction(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		t.Parallel()
		root := t.TempDir()
		env := prepareGuardOptionsFixture(t, root, binary, shell)
		err, output := runGuardOptionsScript(t, root, shell, env, nil, "TRAPEXIT() { /bin/rm \"$IKESU_RUNTIME_ROOT/home/../victim\"; }\nprintf 'safe ran\\n' > \"$IKESU_RUNTIME_ROOT/result\"\n")
		if err != nil || strings.Contains(output, "申請文") {
			t.Fatalf("%v\n%s", err, output)
		}
		assertVictimKept(t, root, output)
	})
}

// 4. zsh runs no TRAPDEBUG inside a trap, so an EXIT trap is judged when it is
// registered, here or in a startup file. A function call cannot be judged then
// and is asked.
func TestZshRuntimeExitTrapJudged(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	for name, script := range map[string]string{
		"inline":   "T=" + trapVictim + "\ntrap '/bin/rm \"$T\"' EXIT\n",
		"literal":  "trap '/bin/rm VICTIM' EXIT\n",
		"function": "TRAPEXIT() { /bin/rm \"$T\"; }\nT=" + trapVictim + "\n",
		"call":     "T=" + trapVictim + "\ncleanup() { /bin/rm \"$T\"; }\ntrap cleanup EXIT\n",
		"builtin":  "T=" + trapVictim + "\nbuiltin trap '/bin/rm \"$T\"' EXIT\n",
		"table":    "functions[TRAPEXIT]='/bin/rm VICTIM'\n",
		"typeset":  "typeset 'functions[TRAPEXIT]=/bin/rm VICTIM'\n",
		"shadowed": "echo() { /bin/rm VICTIM; }\ntrap 'echo ok' EXIT\n",
		"sh-c":     "/bin/sh -c \"trap '/bin/rm VICTIM' EXIT\"\n",
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
	// Traps a startup file left are judged before the script runs, and
	// their text does not run unjudged while the startup reads them.
	for name, zshenv := range map[string]string{
		"startup-exit":     "trap 'printf ran >> \"$IKESU_RUNTIME_ROOT/ran\"; /bin/rm VICTIM' EXIT\n",
		"startup-function": "TRAPEXIT() { printf ran >> \"$IKESU_RUNTIME_ROOT/ran\"; /bin/rm VICTIM; }\n",
		"startup-err":      "trap 'printf ran >> \"$IKESU_RUNTIME_ROOT/ran\"; /bin/rm VICTIM' ERR\n",
		"startup-zerr":     "trap 'printf ran >> \"$IKESU_RUNTIME_ROOT/ran\"; /bin/rm VICTIM' ZERR\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			env := zshGuardEnv(t, root, binary, strings.ReplaceAll(zshenv, "VICTIM", root+"/home/../victim"))
			err, output := runGuardOptionsScript(t, root, "/bin/zsh", env, nil, "false\nprintf after > \"$IKESU_RUNTIME_ROOT/after\"\n")
			assertGuardOptionsStopped(t, root, err, output)
			if _, err := os.Stat(filepath.Join(root, "ran")); !os.IsNotExist(err) {
				t.Fatalf("startup trap text ran: %v\n%s", err, output)
			}
		})
	}
	for name, script := range map[string]string{
		"safe-inline": "trap 'printf ran > OUT/result' EXIT\nexit 3\n",
		"safe-show":   "trap 'printf ran > OUT/result' EXIT\ntrap > OUT/listed\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plainDir, guardedDir := t.TempDir(), t.TempDir()
			plain := exec.Command("/bin/zsh", "-f", "-c", strings.ReplaceAll(script, "OUT", plainDir))
			plain.Env = []string{"PATH=/usr/bin:/bin"}
			plainOut, plainErr := plain.CombinedOutput()
			root := t.TempDir()
			env := zshGuardEnv(t, root, binary, "")
			err, output := runGuardOptionsScript(t, root, "/bin/zsh", env, nil, strings.ReplaceAll(script, "OUT", guardedDir))
			// The guarded zsh also lists its own TRAPDEBUG function.
			if data, readErr := os.ReadFile(filepath.Join(guardedDir, "listed")); readErr == nil {
				if i := bytes.Index(data, []byte("TRAPDEBUG () {")); i >= 0 {
					j := bytes.Index(data[i:], []byte("\n}\n"))
					data = append(data[:i:i], data[i+j+3:]...)
				}
				_ = os.WriteFile(filepath.Join(guardedDir, "listed"), data, 0600)
			}
			assertSameRun(t, plainDir, guardedDir, err, plainErr, output, string(plainOut))
		})
	}
}

// 5. ERR and RETURN traps from the user's BASH_ENV are checked each time they
// run, with the current variables, before their text runs in place. A text
// that calls a user function is asked: the function's lines would run
// unchecked. Calling a function that removes the victim and removing it
// directly get the same decision.
//
// This takes longer than 5 seconds: a rejection ends the shell, so each of the
// 27 cases per Bash needs its own shell. They run in parallel.
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
				for body, orig := range map[string]string{
					"rm":       "trap " + quote("/bin/rm "+trapVictim) + " ERR\n",
					"marker":   "trap " + quote("printf forged >> \"$IKESU_GUARD_MARKER\"") + " ERR\n",
					"second":   "trap " + quote(":; /bin/rm "+trapVictim) + " ERR\n",
					"function": "on_err() { /bin/rm " + trapVictim + "; }\ntrap on_err ERR\n",
				} {
					cases["ERR/"+option+"/"+invocation+"/"+body] = trapCase{orig, option, invocation}
				}
			}
		}
		for body, orig := range map[string]string{
			"rm":       "trap " + quote("/bin/rm "+trapVictim) + " RETURN\n",
			"second":   "trap " + quote(":; /bin/rm "+trapVictim) + " RETURN\n",
			"function": "on_return() { /bin/rm " + trapVictim + "; }\ntrap on_return RETURN\n",
		} {
			cases["RETURN/"+body] = trapCase{orig, "", "f() { :; }\nf"}
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

// assertTrapStopped is for a script that ran to its end: the trap it left was
// rejected when it ran, and the victim is still there.
func assertTrapStopped(t *testing.T, root string, err error, output string) {
	t.Helper()
	if err == nil || !strings.Contains(output, "申請文") {
		t.Fatalf("expected guard rejection: %v\n%s", err, output)
	}
	if data, err := os.ReadFile(filepath.Join(root, "victim")); err != nil || string(data) != "unchanged" {
		t.Fatalf("victim changed: %q, %v\n%s", data, err, output)
	}
}

// Review 2, decision. Bash runs no DEBUG trap for a function definition, so a
// trap whose names are defined differently when it runs is judged again then:
// a builtin shadowed by a function, or the same for another signal.
func TestBashRuntimeTrapNameRedefined(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		for name, script := range map[string]string{
			"echo":     "trap 'echo ok' EXIT\necho() { /bin/rm VICTIM; }\n",
			"printf":   "trap 'printf ok' EXIT\nprintf() { /bin/rm VICTIM; }\n",
			"keyword":  "trap 'echo ok' EXIT\nfunction echo { /bin/rm VICTIM; }\n",
			"signal":   "trap 'printf hi' USR1\nprintf() { /bin/rm VICTIM; }\nkill -USR1 $$\n",
			"in-func":  "trap 'echo ok' EXIT\nsetup() { echo() { /bin/rm VICTIM; }; }\nsetup\n",
			"via-eval": "trap 'echo ok' EXIT\neval 'echo() { /bin/rm VICTIM; }'\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := prepareGuardOptionsFixture(t, root, binary, shell)
				err, output := runGuardOptionsScript(t, root, shell, env, nil, strings.ReplaceAll(script, "VICTIM", root+"/home/../victim"))
				assertTrapStopped(t, root, err, output)
			})
		}
	})
}

// Review 2, item 4. An EXIT text the user's BASH_ENV left is judged before
// the startup can end the shell: by an exit in that file, by a cut with no
// script after it, or by a rejected -c string.
func TestBashRuntimeStartupTrapsBeforeExit(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		for name, tc := range map[string]struct{ orig, mode, script string }{
			"exit":        {"trap '/bin/rm VICTIM' EXIT\nexit 0\n", "file", "printf after > \"$IKESU_RUNTIME_ROOT/after\"\n"},
			"cut-empty":   {"trap '/bin/rm VICTIM' EXIT\n: \"${UNSET_IN_ORIG:?missing}\"\n", "file", ""},
			"exec-string": {"trap '/bin/rm VICTIM' EXIT\n", "c", "/bin/rm VICTIM\n"},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				victim := root + "/home/../victim"
				env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, strings.ReplaceAll(tc.orig, "VICTIM", victim)))
				err, output := runGuardEntry(t, root, shell, env, nil, tc.mode, strings.ReplaceAll(tc.script, "VICTIM", victim))
				assertTrapStopped(t, root, err, output)
			})
		}
	})
}

// Review 2, items 3 and 6. An ERR text is judged with the user's functions
// even when one has a wrapper's name, its substitutions are not left to later
// events, and the startup wraps it again even when the user's BASH_ENV
// removed or replaced the trap wrapper.
func TestBashRuntimeErrTrapUserFunctions(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		for name, orig := range map[string]string{
			"git":           "git() { /bin/rm VICTIM; }\ntrap git ERR\n",
			"codex":         "codex() { /bin/rm VICTIM; }\ntrap codex ERR\n",
			"substitution":  "f() { /bin/rm VICTIM; }\ntrap 'X=$(f)' ERR\n",
			"unset-wrapper": "unset -f trap\ntrap '/bin/rm VICTIM' ERR\n",
			"own-wrapper":   "trap() { builtin trap \"$@\"; }\ntrap '/bin/rm VICTIM' ERR\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, strings.ReplaceAll(orig, "VICTIM", root+"/home/../victim")))
				err, output := runGuardOptionsScript(t, root, shell, env, nil, "false\nprintf after > \"$IKESU_RUNTIME_ROOT/after\"\n")
				assertGuardOptionsStopped(t, root, err, output)
			})
		}
	})
}

// Review 2, decision for zsh: TRAPDEBUG sees a definition, so the line that
// defines, aliases or removes a name a registered trap calls is asked.
func TestZshRuntimeTrapNameRedefined(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	for name, script := range map[string]string{
		"function": "trap 'echo ok' EXIT\necho() { /bin/rm VICTIM }\n",
		"keyword":  "trap 'echo ok' EXIT\nfunction echo { /bin/rm VICTIM }\n",
		"alias":    "trap 'echo ok' EXIT\nalias echo='/bin/rm VICTIM'\n",
		"disable":  "trap 'echo ok' EXIT\ndisable echo\n",
		"trapexit": "TRAPEXIT() { echo ok }\necho() { /bin/rm VICTIM }\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			env := zshGuardEnv(t, root, binary, "")
			err, output := runGuardOptionsScript(t, root, "/bin/zsh", env, nil, strings.ReplaceAll(script, "VICTIM", root+"/home/../victim")+"printf after > \"$IKESU_RUNTIME_ROOT/after\"\n")
			assertGuardOptionsStopped(t, root, err, output)
		})
	}
}

// Review 2, items 4 and 5. Traps a zsh startup file registers are judged when
// they are registered, so an EXIT text does not run unjudged when the startup
// ends the shell, and the check does not depend on IKESU_GUARD_SHELL_DIR.
func TestZshRuntimeStartupTrapsBeforeExit(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	for name, tc := range map[string]struct{ zshenv, mode, script string }{
		"cut":         {"trap '/bin/rm VICTIM' EXIT\n: \"${UNSET_IN_ORIG:?missing}\"\n", "file", "true\n"},
		"exit":        {"trap '/bin/rm VICTIM' EXIT\nexit 0\n", "file", "true\n"},
		"zerr":        {"trap '/bin/rm VICTIM' ZERR\nfalse\n", "file", "true\n"},
		"shell-dir":   {"unset IKESU_GUARD_SHELL_DIR\ntrap '/bin/rm VICTIM' EXIT\n", "file", "true\n"},
		"exec-string": {"TRAPEXIT() { /bin/rm VICTIM }\n", "c", "/bin/rm VICTIM\n"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			victim := root + "/home/../victim"
			env := zshGuardEnv(t, root, binary, strings.ReplaceAll(tc.zshenv, "VICTIM", victim))
			err, output := runGuardEntry(t, root, "/bin/zsh", env, nil, tc.mode, strings.ReplaceAll(tc.script, "VICTIM", victim))
			assertTrapStopped(t, root, err, output)
		})
	}
}

// A user BASH_ENV that sets the guard's own startup flag is stopped there,
// so its EXIT trap cannot skip the judgment the end of the startup gives it.
func TestBashRuntimeStartupFlagForged(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	eachBash(t, func(t *testing.T, shell string) {
		for name, orig := range map[string]string{
			"finalized": "_ikesu_guard_finalized=1\ntrap '/bin/rm VICTIM' EXIT\n",
			"stage":     "_ikesu_guard_startup_stage=done\ntrap '/bin/rm VICTIM' EXIT\n",
			"declare":   "declare _ikesu_guard_finalized=1\ntrap '/bin/rm VICTIM' EXIT\n",
			"export":    "export _ikesu_guard_finalized=1\ntrap '/bin/rm VICTIM' EXIT\n",
			"printf-v":  "printf -v_ikesu_guard_finalized 1\ntrap '/bin/rm VICTIM' EXIT\n",
			"let":       "let _ikesu_guard_trap_count=5\ntrap '/bin/rm VICTIM' EXIT\n",
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, strings.ReplaceAll(orig, "VICTIM", root+"/home/../victim")))
				err, output := runGuardOptionsScript(t, root, shell, env, nil, "printf after > \"$IKESU_RUNTIME_ROOT/after\"\n")
				assertGuardOptionsStopped(t, root, err, output)
			})
		}
	})
}

// The guard's own functions return without running the user's RETURN trap;
// the user's functions do, also after a cut-short BASH_ENV.
func TestBashRuntimeReturnTrapSkipsGuardFunctions(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	record := "trap 'printf \"%s\\n\" \"${FUNCNAME[0]}\" >> \"$IKESU_RUNTIME_ROOT/returns\"' RETURN\n"
	eachBash(t, func(t *testing.T, shell string) {
		t.Run("ordinary", func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, record))
			err, output := runGuardOptionsScript(t, root, shell, env, nil, "git status\ncommand git status\ntrap 'printf x' USR1\nf() { :; }\nf\n")
			if err != nil {
				t.Fatalf("%v\n%s", err, output)
			}
			if data, err := os.ReadFile(filepath.Join(root, "returns")); err != nil || string(data) != "f\n" {
				t.Fatalf("RETURN trap ran for %q, %v\n%s", data, err, output)
			}
		})
		for _, mode := range []string{"file", "c", "stdin"} {
			if shell == "/bin/bash" && mode == "stdin" {
				continue // the user's file is judged line by line there
			}
			t.Run("cut/"+mode, func(t *testing.T) {
				t.Parallel()
				root := t.TempDir()
				env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, record+": \"${UNSET_IN_ORIG:?missing}\"\n"))
				err, output := runGuardEntry(t, root, shell, env, nil, mode, "f() { :; }\nf\n")
				if err != nil {
					t.Fatalf("%v\n%s", err, output)
				}
				if data, err := os.ReadFile(filepath.Join(root, "returns")); err != nil || !strings.HasSuffix(string(data), "f\n") {
					t.Fatalf("RETURN trap for f: %q, %v\n%s", data, err, output)
				}
			})
		}
	})
}

// trap -p, trap and trap -P show the ERR text the user registered, as a Bash
// without the guard does, also inside a function without errtrace.
func TestBashRuntimeErrTrapListing(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	orig := "trap 'printf err > /dev/null' ERR\n"
	script := "f() { trap -p ERR > OUT/shown; }\nf\ntrap > OUT/listed\ntrap -P ERR > OUT/result 2>&1\n"
	eachBash(t, func(t *testing.T, shell string) {
		t.Parallel()
		plainDir, guardedDir := t.TempDir(), t.TempDir()
		root := t.TempDir()
		origPath := writeOriginal(t, root, orig)
		plain := exec.Command(shell, "-c", strings.ReplaceAll(script, "OUT", plainDir))
		plain.Env = []string{"PATH=/usr/bin:/bin", "BASH_ENV=" + origPath}
		plainOut, plainErr := plain.CombinedOutput()
		env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+origPath)
		err, output := runGuardOptionsScript(t, root, shell, env, nil, strings.ReplaceAll(script, "OUT", guardedDir))
		assertSameRun(t, plainDir, guardedDir, err, plainErr, output, string(plainOut))
	})
}

// zsh traps from .zshenv are judged once when the startup ends, as a
// registration in the shell would be: ZERR, ERR, TRAPZERR and a function call.
func TestZshRuntimeStartupErrTrapJudged(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	for name, zshenv := range map[string]string{
		"list":     "trap '/bin/rm " + trapVictim + "' ZERR\n",
		"posix":    "trap '/bin/rm " + trapVictim + "' ERR\n",
		"function": "TRAPZERR() { /bin/rm " + trapVictim + "; }\n",
		"call":     "on_err() { /bin/rm " + trapVictim + "; }\ntrap on_err ZERR\n",
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

// 6. Calling a forwarding function directly is judged like any other line, and
// a user's own function with a wrapper's name gets no skip.
func TestShellRuntimeDirectWrapperCall(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ikesu")
	guardBinary(t, binary)
	script := "_ikesu_guard_command /bin/rm " + trapVictim + "\nprintf after > \"$IKESU_RUNTIME_ROOT/after\"\n"
	eachBash(t, func(t *testing.T, shell string) {
		t.Run("direct", func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			env := prepareGuardOptionsFixture(t, root, binary, shell)
			err, output := runGuardOptionsScript(t, root, shell, env, nil, script)
			assertGuardOptionsStopped(t, root, err, output)
		})
		t.Run("user-git", func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			orig := "function git { /bin/rm \"$IKESU_RUNTIME_ROOT/home/../victim\"; }\n"
			env := prepareGuardOptionsFixture(t, root, binary, shell, "BASH_ENV="+writeOriginal(t, root, orig))
			err, output := runGuardOptionsScript(t, root, shell, env, nil, "git status\nprintf after > \"$IKESU_RUNTIME_ROOT/after\"\n")
			assertGuardOptionsStopped(t, root, err, output)
		})
	})
	t.Run("zsh", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		env := zshGuardEnv(t, root, binary, "")
		err, output := runGuardOptionsScript(t, root, "/bin/zsh", env, nil, script)
		assertGuardOptionsStopped(t, root, err, output)
	})
}
