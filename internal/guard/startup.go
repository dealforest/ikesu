package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kazuph/ikesu/internal/childenv"
)

// ConfigureShell is called before Prepare for the interactive pane shell.
// A direct agent receives the already completed PATH without a prompt hook.
func (a *Assets) ConfigureShell(shell string) {
	switch filepath.Base(shell) {
	case "zsh":
	case "bash":
		a.prompt = true
	default:
		a.unsupported = "shell の起動後に shim の PATH を保証できません: " + shell
	}
}

const restoreZdotdir = `if [[ "${IKESU_GUARD_ZDOTDIR_SET-}" == 1 ]]; then export ZDOTDIR="${IKESU_GUARD_ZDOTDIR_ORIG-}"; else unset ZDOTDIR; fi
`

func (a *Assets) prepareStartup(m map[string]string, dir string) error {
	if err := a.publishRuntime(); err != nil {
		return err
	}
	m["IKESU_GUARD_ZDOTDIR_ORIG"], m["IKESU_GUARD_ZDOTDIR_SET"] = m["ZDOTDIR"], "0"
	if _, ok := m["ZDOTDIR"]; ok {
		m["IKESU_GUARD_ZDOTDIR_SET"] = "1"
	}
	m["IKESU_GUARD_SHELL_DIR"] = dir
	m["ZDOTDIR"] = dir
	m["IKESU_GUARD_BASH_ENV_ORIG"] = m["BASH_ENV"]
	m["BASH_ENV"] = filepath.Join(dir, "bash-env")
	if a.prompt {
		// Interactive bash ignores BASH_ENV. Its first prompt runs after both the
		// system and user login files. Without this acknowledgement no marker is
		// published and ai_guard is not suppressed in a child shell.
		m["IKESU_GUARD_PROMPT_COMMAND_ORIG"], m["IKESU_GUARD_PROMPT_COMMAND_SET"] = m["PROMPT_COMMAND"], "0"
		if _, ok := m["PROMPT_COMMAND"]; ok {
			m["IKESU_GUARD_PROMPT_COMMAND_SET"] = "1"
		}
		m["IKESU_GUARD_READY"] = filepath.Join(dir, "ready")
		_ = os.Remove(m["IKESU_GUARD_READY"])
		m["PROMPT_COMMAND"] += childenv.GuardPromptHook(a.Config.EffectiveMode() == "enforce")
		delete(m, "IKESU_GUARD_ENFORCING")
	}
	if a.unsupported != "" {
		delete(m, "IKESU_GUARD_ENFORCING")
	}
	return nil
}

// Keep the user's ZDOTDIR in their interactive shell, while external agents
// inherit the generated startup chain for every tool shell they launch.
func (a *Assets) agentFunctions(dir string) string {
	var body string
	for _, agent := range []string{"codex", "claude"} {
		invocation := `ZDOTDIR=` + quote(dir) + ` BASH_ENV=` + quote(filepath.Join(dir, "bash-env")) + ` builtin command ` + agent + ` "$@"`
		if agent == "claude" {
			invocation = `local _ikesu_claude_prefix=` + quote(filepath.Join(dir, "claude-prefix")) + `
 IKESU_GUARD_CLAUDE_PREFIX_ORIG="${CLAUDE_CODE_SHELL_PREFIX:-}" IKESU_GUARD_CLAUDE_PREFIX_SET="${CLAUDE_CODE_SHELL_PREFIX+x}" IKESU_GUARD_CLAUDE_PREFIX="$_ikesu_claude_prefix" CLAUDE_CODE_SHELL_PREFIX="$_ikesu_claude_prefix" ` + invocation
		}
		body += fmt.Sprintf(`if ! typeset -f %[1]s >/dev/null 2>&1 && ! alias %[1]s >/dev/null 2>&1; then
function %[1]s { %[2]s; }
fi
`, agent, invocation)
	}
	return body
}

// Use the same generator when proving a child bash will read our DEBUG hook.
// The judging functions and a startup DEBUG trap exist before the user's
// BASH_ENV runs. The startup trap does not judge that file. When the shell
// leaves it, normally or because an error cut it short, the trap finishes the
// setup once and judges from that command on.
func (a *Assets) bashStartup(dir string) string {
	// Read the user's BASH_ENV with an inherited nounset suspended so an unset
	// variable cannot interrupt startup. A set -u of that file itself stays.
	suspendNounset := `builtin unset _ikesu_guard_nounset
case $- in *u*) builtin set +u; _ikesu_guard_nounset=1;; esac
`
	restoreNounset := `if [[ ${_ikesu_guard_nounset-} == 1 ]]; then builtin unset _ikesu_guard_nounset; builtin set -u; fi
`
	text := a.generationHeader() + suspendNounset + bashRequestReasonCapture + restoreZdotdir + a.bashCoreFunctions() + a.debugHook("bash") + a.bashSetup(dir, restoreNounset) + a.bashFinalize() + `if [[ $- != *i* ]]; then
builtin unset _ikesu_guard_finalized _ikesu_guard_startup_stage _ikesu_guard_startup_strict
_ikesu_guard_wrapper_names=
_ikesu_guard_startup_file="${BASH_SOURCE[0]-}"
_ikesu_guard_startup_loader="${BASH_SOURCE[1]-}"
_ikesu_guard_startup_orig="${IKESU_GUARD_BASH_ENV_ORIG-}"
` + bashStdinProbe + `_ikesu_guard_source_line=$((LINENO + @SOURCE_OFF@)) _ikesu_guard_done_line=$((LINENO + @DONE_OFF@))
readonly _ikesu_guard_startup_file _ikesu_guard_startup_loader _ikesu_guard_startup_orig _ikesu_guard_startup_strict _ikesu_guard_source_line _ikesu_guard_done_line
set -o functrace
builtin trap '_ikesu_guard_startup_debug "${BASH_COMMAND-}"' DEBUG
fi
if [[ -n "${IKESU_GUARD_BASH_ENV_ORIG-}" && -f "${IKESU_GUARD_BASH_ENV_ORIG-}" ]]; then
_ikesu_guard_startup_stage=orig
source "${IKESU_GUARD_BASH_ENV_ORIG-}"
fi
[[ $- == *i* ]] || _ikesu_guard_startup_done
if [[ $- != *i* ]]; then
_ikesu_guard_finalize
true
else
_ikesu_guard_setup
fi
`
	// The file may be read with lines before this text, so the two lines are
	// counted from the line that records them.
	base := lineOf(text, "_ikesu_guard_source_line=$((LINENO + @SOURCE_OFF@)) _ikesu_guard_done_line=$((LINENO + @DONE_OFF@))")
	return strings.NewReplacer("@SOURCE_OFF@", strconv.Itoa(lineOf(text, "source \"${IKESU_GUARD_BASH_ENV_ORIG-}\"")-base), "@DONE_OFF@", strconv.Itoa(lineOf(text, "[[ $- == *i* ]] || _ikesu_guard_startup_done")-base)).Replace(text)
}

// lineOf is the 1-based number of the line that is exactly line.
func lineOf(text, line string) int {
	for n, l := range strings.Split(text, "\n") {
		if l == line {
			return n + 1
		}
	}
	return 0
}

// bashStdinProbe tells, in bash 3.2 without -c, a script file from a script
// read from standard input. Bash 3.2 shows no difference inside the shell, so
// it reads the process arguments once. If ps fails or is unclear, the user's
// BASH_ENV is judged line by line (strict).
const bashStdinProbe = `if (( BASH_VERSINFO[0] < 4 )) && [[ -z ${BASH_EXECUTION_STRING:-} ]]; then
_ikesu_guard_startup_strict=1
if _ikesu_guard_args=$(/bin/ps -ww -o args= -p "$$" 2>/dev/null) && [[ -n $_ikesu_guard_args ]]; then
builtin read -r -a _ikesu_guard_argv <<< "$_ikesu_guard_args"
_ikesu_guard_i=1
while (( _ikesu_guard_i < ${#_ikesu_guard_argv[@]} )); do
case "${_ikesu_guard_argv[_ikesu_guard_i]}" in
-o|+o|-O|+O|--rcfile|--init-file) (( _ikesu_guard_i += 2 )); continue;;
--) (( _ikesu_guard_i + 1 < ${#_ikesu_guard_argv[@]} )) && _ikesu_guard_startup_strict=; break;;
--*) ;;
-*s*|+*s*) break;;
-*|+*) ;;
*) _ikesu_guard_startup_strict=; break;;
esac
(( _ikesu_guard_i += 1 ))
done
fi
builtin unset _ikesu_guard_args _ikesu_guard_argv _ikesu_guard_i
fi
`

// bashCoreFunctions defines the forwarding functions before the user's file,
// read-only, for both kinds of shell.
func (a *Assets) bashCoreFunctions() string {
	if len(a.Config.Commands()) == 0 {
		return ""
	}
	return a.shellCore() + "readonly -f _ikesu_guard_forward _ikesu_guard_command\n"
}

// bashSetup is the part of the setup that also ran for an interactive shell:
// the PATH pin, the user-facing wrappers, the agent functions and nounset.
// It records which wrappers it defines, so their frames and returns are
// known to be the guard's own.
func (a *Assets) bashSetup(dir, restoreNounset string) string {
	var b strings.Builder
	b.WriteString("_ikesu_guard_setup() {\n" + childenv.GuardPathPin)
	for _, cmd := range a.Config.Commands() {
		fmt.Fprintf(&b, `if ! typeset -f %s >/dev/null 2>&1 && ! alias %s >/dev/null 2>&1; then
_ikesu_guard_wrapper_names+=" %s"
function %s { _ikesu_guard_forward %s "$@" || return $?; }
fi
`, quote(cmd), quote(cmd), cmd, cmd, quote(cmd))
	}
	if len(a.Config.Commands()) > 0 {
		b.WriteString(`if ! typeset -f command >/dev/null 2>&1 && ! alias command >/dev/null 2>&1; then
_ikesu_guard_wrapper_names+=" command"
command() { _ikesu_guard_command "$@" || return $?; }
fi
`)
	}
	b.WriteString(a.agentFunctions(dir) + restoreNounset + "}\nreadonly -f _ikesu_guard_setup\n")
	return b.String()
}

// bashFinalize defines the startup DEBUG trap and the setup it finishes.
//
// Leaving the user's file is seen from the stack of the next command. Bash 5
// drops that file from BASH_SOURCE. Bash 3.2 keeps the cut frames, so a
// script file shows main above them and a -c string shows line 0. A script
// read from standard input shows no difference in 3.2; there the user's file
// is judged line by line (strict, see bashStdinProbe). This file's own lines
// are known by their line numbers while the user's file may still be running,
// since after a cut a -c string can report this file as its source.
func (a *Assets) bashFinalize() string {
	return fmt.Sprintf(`if [[ $- != *i* ]]; then
_ikesu_guard_startup_debug() {
 local _ikesu_guard_frame _ikesu_guard_inside=0
 # Bash 3.2 keeps this trap when a DEBUG handler replaces it.
 if [[ ${_ikesu_guard_finalized-} == 1 ]]; then
  [[ ${FUNCNAME[1]-} == _ikesu_guard_finalize ]] || _ikesu_guard_dispatch "$1"
  return 0
 fi
 if [[ ${BASH_SOURCE[1]-} == "$_ikesu_guard_startup_file" ]]; then
  [[ ${_ikesu_guard_startup_stage-} != orig ]] && return 0
  # The guard's own functions, such as the check of the RETURN trap that
  # runs when source leaves the user's file, do not end the startup.
  [[ ${FUNCNAME[1]-} == _ikesu_guard_* ]] && return 0
  case "${BASH_LINENO[0]-}" in "$_ikesu_guard_source_line"|"$_ikesu_guard_done_line") return 0;; esac
 fi
 for _ikesu_guard_frame in "${BASH_SOURCE[@]:1}"; do
  [[ $_ikesu_guard_frame == "$_ikesu_guard_startup_orig" ]] && _ikesu_guard_inside=1
 done
 [[ ${FUNCNAME[1]-} == main && ${BASH_SOURCE[1]-} != "$_ikesu_guard_startup_orig" ]] && _ikesu_guard_inside=0
 [[ ${BASH_LINENO[0]-} == 0 ]] && _ikesu_guard_inside=0
 if [[ $_ikesu_guard_inside == 1 ]]; then
  if [[ -n ${_ikesu_guard_startup_strict-} ]]; then
   _ikesu_guard_dispatch "$1"
  else
   # A line of the user's file that names the guard's state is judged too.
   case "$1" in *trap*[Dd][Ee][Bb][Uu][Gg]*|*set*functrace*|*set*+*T*|*shopt*extdebug*) _ikesu_guard_dispatch "$1";;
   *_ikesu_guard_*) [[ ${BASH_SOURCE[1]-} == "$_ikesu_guard_startup_file" ]] || _ikesu_guard_dispatch "$1";; esac
  fi
  return 0
 fi
 _ikesu_guard_finalize
 _ikesu_guard_dispatch "$1"
}
_ikesu_guard_startup_done() {
 _ikesu_guard_startup_stage=done
}
_ikesu_guard_finalize() {
 local _ikesu_guard_trap_name _ikesu_guard_trap_words _ikesu_guard_trap_RETURN _ikesu_guard_trap_ERR _ikesu_guard_trap_EXIT
 [[ ${_ikesu_guard_finalized-} == 1 ]] && return 0
 # A set -u of the user's file is restored after the setup.
 case $- in *u*) builtin set +u; _ikesu_guard_nounset=1;; esac
 # Read the traps with functrace on: a subshell shows RETURN only then. It
 # stays on: the startup ends by setting it.
 builtin set -T
 _ikesu_guard_trap_RETURN=$(builtin trap -p RETURN)
 _ikesu_guard_trap_ERR=$(builtin trap -p ERR)
 _ikesu_guard_trap_EXIT=$(builtin trap -p EXIT)
 # ERR and RETURN set with the builtin directly are wrapped like the others.
 for _ikesu_guard_trap_name in RETURN ERR; do
  _ikesu_guard_trap_words="_ikesu_guard_trap_$_ikesu_guard_trap_name"
  [[ -n ${!_ikesu_guard_trap_words} ]] || continue
  builtin eval "_ikesu_guard_trap_words=(${!_ikesu_guard_trap_words#trap })"
  case "${_ikesu_guard_trap_words[1]-}" in ''|'if _ikesu_guard_trap_check '*) continue;; esac
  trap -- "${_ikesu_guard_trap_words[1]}" "$_ikesu_guard_trap_name"
 done
if [[ ${_ikesu_guard_reason_name+x} ]] && [[ %s ]]; then
%sfi
 _ikesu_guard_setup
if [[ %s ]] && [[ -n "${BASH_EXECUTION_STRING:-}" ]]; then
 %s guard line --via exec-string -- "${BASH_EXECUTION_STRING-}" || exit $?
fi
 # An EXIT text the user's file left gets the judgment of a registration.
 [[ -z $_ikesu_guard_trap_EXIT ]] || _ikesu_guard_debug "$_ikesu_guard_trap_EXIT" force
 set -o functrace
 _ikesu_guard_finalized=1
 readonly _ikesu_guard_finalized _ikesu_guard_wrapper_names
 builtin trap '_ikesu_guard_dispatch "${BASH_COMMAND-}"' DEBUG
 true
}
readonly -f _ikesu_guard_startup_debug _ikesu_guard_startup_done _ikesu_guard_finalize
fi
`, a.shellActive(), bashRequestReasonRestore, a.shellActive(), a.invocation())
}

// A stable loader asks the currently published native binary to refresh its
// payload before sourcing it. Neither eval nor PATH participates in lookup.
func (a *Assets) startupLoader(payload string) string {
	name := filepath.Base(payload)
	return "if [[ " + a.shellActive() + " ]]; then\n_ikesu_guard_generation=$(builtin trap - ERR; " + a.invocation() + " guard refresh) || exit $?\nsource \"$_ikesu_guard_generation/" + name + "\" || exit $?\nelse\nsource " + quote(filepath.Join(filepath.Dir(a.RuntimePath()), "current", name)) + " || exit $?\nfi\n"
}

func (a *Assets) generationHeader() string {
	info, err := os.Stat(a.Executable)
	if err != nil {
		return "# guard generator unavailable\n"
	}
	return fmt.Sprintf("# guard generator %q size=%d mtime=%d\n", a.Executable, info.Size(), info.ModTime().UnixNano())
}

func (a *Assets) startupPayloads(dir string) map[string][]byte {
	files := map[string][]byte{}
	wrappers := a.shellFunctions() + a.agentFunctions(dir)
	suspendNounset := `builtin unset _ikesu_guard_nounset
if [[ -o nounset ]]; then unsetopt nounset; _ikesu_guard_nounset=1; fi
`
	restoreNounset := `if [[ ${_ikesu_guard_nounset-} == 1 ]]; then unset _ikesu_guard_nounset; setopt nounset; fi
`
	capture := `IKESU_GUARD_ZDOTDIR_ORIG="${ZDOTDIR-}"; IKESU_GUARD_ZDOTDIR_SET=${+ZDOTDIR}
export IKESU_GUARD_ZDOTDIR_ORIG IKESU_GUARD_ZDOTDIR_SET
`
	// Each original file sees the user's ZDOTDIR. Only the intervals between
	// startup files use our directory; the final phase restores it permanently.
	phases := []struct{ name, next string }{
		{".zshenv", "[[ -o login || -o interactive ]]"},
		{".zprofile", "true"},
		{".zshrc", "[[ -o login ]]"},
		{".zlogin", "false"},
	}
	for _, phase := range phases {
		body := a.generationHeader() + suspendNounset + restoreZdotdir + fmt.Sprintf("if [[ -f \"${ZDOTDIR:-${HOME-}}/%s\" ]]; then source \"${ZDOTDIR:-${HOME-}}/%s\"; fi\n", phase.name, phase.name) + capture + childenv.GuardPathPin
		if phase.name == ".zshenv" {
			body += wrappers + zshWrapperTexts + a.lineHook()
		}
		body += "if " + phase.next + "; then export ZDOTDIR=\"${IKESU_GUARD_SHELL_DIR-}\"; fi\n" + restoreNounset
		if phase.name == ".zshenv" {
			body += "if [[ ! -o login && ! -o interactive ]]; then\n" + a.debugHook("zsh") + "fi\n"
		}
		if phase.name == ".zlogin" {
			body += a.debugHook("zsh")
		}

		files[phase.name+".current"] = []byte(body)
	}
	files["bash-env.current"] = []byte(a.bashStartup(dir))
	return files
}

// zshWrapperTexts keeps the forwarding functions' text without tabs, for the
// TRAPDEBUG skip of their own lines (see zshInternalCases).
const zshWrapperTexts = `_ikesu_guard_ztexts="${${functions[_ikesu_guard_forward]-}//$'\t'/}${${functions[_ikesu_guard_command]-}//$'\t'/}"
`
