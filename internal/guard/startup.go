package guard

import (
	"fmt"
	"os"
	"path/filepath"
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
// leaves it, normally or because an expansion error cut it short, the trap
// finishes the setup once and judges from that command on.
func (a *Assets) bashStartup(dir string) string {
	// Read the user's BASH_ENV with nounset suspended so an unset variable
	// cannot interrupt startup. Restore it before DEBUG would check the set.
	suspendNounset := `builtin unset _ikesu_guard_nounset
case $- in *u*) builtin set +u; _ikesu_guard_nounset=1;; esac
`
	restoreNounset := `if [[ ${_ikesu_guard_nounset-} == 1 ]]; then builtin unset _ikesu_guard_nounset; builtin set -u; fi
`
	return a.generationHeader() + suspendNounset + bashRequestReasonCapture + restoreZdotdir + a.debugHook("bash") + a.bashFinalize(dir, restoreNounset) + `if [[ $- != *i* ]]; then
_ikesu_guard_startup_file="${BASH_SOURCE[0]-}"
_ikesu_guard_startup_loader="${BASH_SOURCE[1]-}"
_ikesu_guard_startup_orig="${IKESU_GUARD_BASH_ENV_ORIG-}"
readonly _ikesu_guard_startup_file _ikesu_guard_startup_loader _ikesu_guard_startup_orig
set -o functrace
builtin trap 'case $- in *T*) builtin set +T; _ikesu_guard_startup_debug "${BASH_COMMAND-}"; builtin set -T;; *) _ikesu_guard_startup_debug "${BASH_COMMAND-}";; esac' DEBUG
fi
if [[ -n "${IKESU_GUARD_BASH_ENV_ORIG-}" && -f "${IKESU_GUARD_BASH_ENV_ORIG-}" ]]; then source "${IKESU_GUARD_BASH_ENV_ORIG-}"; fi
if [[ $- != *i* ]]; then
_ikesu_guard_finalize
else
` + childenv.GuardPathPin + a.shellFunctions() + a.agentFunctions(dir) + restoreNounset + "fi\n"
}

// bashFinalize defines the startup DEBUG trap and the setup it finishes.
//
// Leaving the user's file is seen from the stack of the next command. Bash 5
// drops that file from BASH_SOURCE. Bash 3.2 keeps the cut frames, so a
// script file shows main above them and a -c string shows line 0. For a
// script read from standard input, 3.2 shows no difference; a line with a
// NAME? or NAME:? expansion that can fail, or one that cannot be evaluated
// here, switches to judging after it runs. A line that could change the
// DEBUG trap is judged even inside the user's file.
func (a *Assets) bashFinalize(dir, restoreNounset string) string {
	return fmt.Sprintf(`if [[ $- != *i* ]]; then
_ikesu_guard_startup_scan() {
 local _ikesu_guard_rest="$1" _ikesu_guard_name _ikesu_guard_re='^([A-Za-z_][A-Za-z0-9_]*)(:?)[?]'
 while [[ $_ikesu_guard_rest == *'${'* ]]; do
  _ikesu_guard_rest="${_ikesu_guard_rest#*'${'}"
  if [[ $_ikesu_guard_rest =~ $_ikesu_guard_re ]]; then
   _ikesu_guard_name="${BASH_REMATCH[1]}"
   if [[ -n ${BASH_REMATCH[2]} ]]; then
    [[ -n ${!_ikesu_guard_name-} ]] && continue
   else
    [[ ${!_ikesu_guard_name+x} ]] && continue
   fi
   _ikesu_guard_startup_risk=1
   return 0
  fi
  case "${_ikesu_guard_rest%%%%'}'*}" in *'?'*) _ikesu_guard_startup_risk=1; return 0;; esac
 done
 return 0
}
_ikesu_guard_startup_debug() {
 local _ikesu_guard_frame _ikesu_guard_inside=0
 # Bash 3.2 keeps this trap when a DEBUG handler replaces it.
 if [[ ${_ikesu_guard_finalized-} == 1 ]]; then
  [[ ${FUNCNAME[1]-} == _ikesu_guard_finalize ]] || _ikesu_guard_dispatch "$1"
  return 0
 fi
 # The setup's own lines, including the calls made by _ikesu_guard_finalize.
 [[ ${BASH_SOURCE[1]-} == "$_ikesu_guard_startup_file" ]] && return 0
 for _ikesu_guard_frame in "${BASH_SOURCE[@]:1}"; do
  [[ $_ikesu_guard_frame == "$_ikesu_guard_startup_orig" ]] && _ikesu_guard_inside=1
 done
 [[ ${FUNCNAME[1]-} == main && ${BASH_SOURCE[1]-} != "$_ikesu_guard_startup_orig" ]] && _ikesu_guard_inside=0
 [[ ${BASH_LINENO[0]-} == 0 ]] && _ikesu_guard_inside=0
 [[ ${_ikesu_guard_startup_risk-} == 1 ]] && _ikesu_guard_inside=0
 if [[ $_ikesu_guard_inside == 1 ]]; then
  case $- in *u*) builtin set +u; _ikesu_guard_nounset=1;; esac
  (( BASH_VERSINFO[0] >= 5 )) || [[ -n ${BASH_EXECUTION_STRING:-} ]] || _ikesu_guard_startup_scan "$1"
  case "$1" in *[Dd][Ee][Bb][Uu][Gg]*|*functrace*|*extdebug*|set*+*T*) _ikesu_guard_dispatch "$1";; esac
  return 0
 fi
 _ikesu_guard_finalize
 _ikesu_guard_dispatch "$1"
}
_ikesu_guard_finalize() {
 local _ikesu_guard_trap_name _ikesu_guard_trap_flags=$- _ikesu_guard_trap_words _ikesu_guard_trap_RETURN _ikesu_guard_trap_ERR _ikesu_guard_trap_EXIT
 [[ ${_ikesu_guard_finalized-} == 1 ]] && return 0
 # Read the user's traps with functrace on (a subshell shows RETURN only
 # then) and wrap RETURN first, so no function returns into the raw text.
 builtin set -T
 _ikesu_guard_trap_RETURN=$(builtin trap -p RETURN)
 _ikesu_guard_trap_ERR=$(builtin trap -p ERR)
 _ikesu_guard_trap_EXIT=$(builtin trap -p EXIT)
 [[ $_ikesu_guard_trap_flags == *T* ]] || builtin set +T
 # The trap wrapper existed before the user's file. Wrap only traps it set
 # with the builtin directly.
 for _ikesu_guard_trap_name in RETURN ERR EXIT; do
  _ikesu_guard_trap_words="_ikesu_guard_trap_$_ikesu_guard_trap_name"
  [[ -n ${!_ikesu_guard_trap_words} ]] || continue
  builtin eval "_ikesu_guard_trap_words=(${!_ikesu_guard_trap_words#trap })"
  case "${_ikesu_guard_trap_words[1]-}" in ''|_ikesu_guard_run_exit_trap|'if _ikesu_guard_trap_check '*) continue;; esac
  trap -- "${_ikesu_guard_trap_words[1]}" "$_ikesu_guard_trap_name"
 done
if [[ ${_ikesu_guard_reason_name+x} ]] && [[ %s ]]; then
%sfi
%s%s%s%sif [[ %s ]] && [[ -n "${BASH_EXECUTION_STRING:-}" ]]; then
 %s guard line --via exec-string -- "${BASH_EXECUTION_STRING-}" || exit $?
fi
 set -o functrace
 _ikesu_guard_finalized=1
 readonly _ikesu_guard_finalized _ikesu_guard_wrapper_names
 builtin trap 'case $- in *T*) builtin set +T; _ikesu_guard_dispatch "${BASH_COMMAND-}"; builtin set -T;; *) _ikesu_guard_dispatch "${BASH_COMMAND-}";; esac' DEBUG
}
readonly -f _ikesu_guard_startup_scan _ikesu_guard_startup_debug _ikesu_guard_finalize
fi
`, a.shellActive(), bashRequestReasonRestore, childenv.GuardPathPin, a.bashWrapperNames()+a.shellWrappers(), a.agentFunctions(dir), restoreNounset, a.shellActive(), a.invocation())
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
			body += wrappers + a.lineHook()
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

// bashWrapperNames records which user-facing names the generated wrappers
// take, so a user's RETURN trap does not run when one of them returns.
func (a *Assets) bashWrapperNames() string {
	var b strings.Builder
	names := a.Config.Commands()
	if len(names) > 0 {
		names = append(append([]string{}, names...), "command")
	}
	for _, name := range names {
		fmt.Fprintf(&b, "if ! typeset -f %s >/dev/null 2>&1 && ! alias %s >/dev/null 2>&1; then _ikesu_guard_wrapper_names+=\" %s\"; fi\n", quote(name), quote(name), name)
	}
	return b.String()
}
