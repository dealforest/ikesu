package guard

import (
	"fmt"
	"strings"
)

// Lines that bash's DEBUG trap reports from inside the generated functions.
// Only these exact lines are skipped, and only in their own frame. The first
// line of each list sets the frame's flag; every later line needs that flag.
// trap is the generated wrapper: it runs no user text, so with its flag set
// every line of that frame is skipped.
var bashInternalLines = map[string][]string{
	"_ikesu_guard_forward":       {"local _ikesu_guard_in_wrapper=1"},
	"_ikesu_guard_command":       {"local _ikesu_guard_in_wrapper=1"},
	"_ikesu_guard_run_exit_trap": {"local _ikesu_guard_in_wrapper=1 _ikesu_guard_exit_saved=$? _ikesu_guard_exit_mark= _ikesu_guard_exit_rc=0"},
	"trap":                       {bashTrapWrapperFirstLine},
}

const bashTrapWrapperFirstLine = "local _ikesu_guard_in_wrapper=1 _ikesu_guard_trap_out= _ikesu_guard_trap_name= _ikesu_guard_trap_sig= _ikesu_guard_trap_action= _ikesu_guard_trap_text= _ikesu_guard_trap_q= _ikesu_guard_trap_rc=0"

func (a *Assets) bashInternalCases() string {
	var b strings.Builder
	for _, frame := range []string{"_ikesu_guard_forward", "_ikesu_guard_command", "_ikesu_guard_run_exit_trap", "trap"} {
		for n, line := range append(append([]string{}, bashInternalLines[frame]...), a.bashFrameLines(frame)...) {
			if n == 0 {
				fmt.Fprintf(&b, "  %s) return 0;;\n", quote(frame+":"+line))
			} else {
				fmt.Fprintf(&b, "  %s) [[ ${_ikesu_guard_in_wrapper-} == 1 ]] && return 0;;\n", quote(frame+":"+line))
			}
		}
	}
	// Bash reports a function's call line once more as the first event inside
	// it. The caller's frame already decided that line.
	for _, frame := range []string{"_ikesu_guard_forward", "_ikesu_guard_command", "_ikesu_guard_run_exit_trap", "_ikesu_guard_trap_check", "_ikesu_guard_trap_return", "trap"} {
		fmt.Fprintf(&b, "  %s|%s*) return 0;;\n", quote(frame+":"+frame), quote(frame+":"+frame+" "))
	}
	// These frames run no user text of their own: the check and the status
	// helper are pure, and the trap wrapper only stores and installs text.
	b.WriteString("  _ikesu_guard_trap_check:*|_ikesu_guard_trap_return:*|_ikesu_guard_finalize:*) return 0;;\n")
	b.WriteString("  trap:*) [[ ${_ikesu_guard_in_wrapper-} == 1 ]] && return 0;;\n")
	return b.String()
}

// bashWrapperCalls lists the calls written by the generated wrappers. Each is
// accepted only in the frame of the wrapper that contains it. The trap helpers
// only check a stored trap text, restore $?, or run the stored EXIT text under
// the DEBUG trap, so calling them directly does not skip a check.
func (a *Assets) bashWrapperCalls() string {
	var b strings.Builder
	for _, cmd := range a.Config.Commands() {
		fmt.Fprintf(&b, "  %s) [[ ${FUNCNAME[_ikesu_guard_at]-} == %s ]] && return 0;;\n", quote("_ikesu_guard_forward "+quote(cmd)+` "$@"`), quote(cmd))
	}
	if len(a.Config.Commands()) > 0 {
		fmt.Fprintf(&b, "  %s) [[ ${FUNCNAME[_ikesu_guard_at]-} == command ]] && return 0;;\n", quote(`_ikesu_guard_command "$@"`))
	}
	for _, line := range []string{":", "_ikesu_guard_run_exit_trap", `_ikesu_guard_trap_check ERR "$?"`, `_ikesu_guard_trap_check RETURN "$?"`, `_ikesu_guard_trap_return "$_ikesu_guard_trap_saved_ERR"`, `_ikesu_guard_trap_return "$_ikesu_guard_trap_saved_RETURN"`} {
		fmt.Fprintf(&b, "  %s) return 0;;\n", quote(line))
	}
	return b.String()
}

func (a *Assets) debugHook(shell string) string {
	if shell == "bash" {
		targets := make([]string, 0, len(a.Config.Commands()))
		for _, name := range a.Config.Commands() {
			targets = append(targets, quote(name))
		}
		protectForward := ""
		if len(targets) > 0 {
			protectForward = "readonly -f _ikesu_guard_forward _ikesu_guard_command\n"
		}
		return fmt.Sprintf(`if [[ $- != *i* ]]; then
%s%s%s_ikesu_guard_needs_check() {
 local _ikesu_guard_line="$1" _ikesu_guard_word
 _ikesu_guard_line="${_ikesu_guard_line#"${_ikesu_guard_line%%%%[!$' \t\r\n']*}"}"
 _ikesu_guard_line="${_ikesu_guard_line%%%%"${_ikesu_guard_line##*[!$' \t\r\n']}"}"
 case "$_ikesu_guard_line" in *AI_GUARD_REQUEST_REASON*|*AI_GUARD_REQUEST_ENV_NAME*|*IKESU_GUARD_PARENT_REASON*) return 0;; esac
 [[ $_ikesu_guard_line == *"${_ikesu_guard_reason_name:-AI_GUARD_REQUEST_REASON}"* ]] && return 0
 _ikesu_guard_simple_form "$_ikesu_guard_line" && return 1
 case "$_ikesu_guard_line" in *'>'*) return 0;; esac
 _ikesu_guard_word="${_ikesu_guard_line%%%%[$' \t\r\n']*}"
 case "$_ikesu_guard_word" in ''|*[!a-zA-Z0-9_./-]*) return 0;; esac
 _ikesu_guard_word="${_ikesu_guard_word##*/}"
 case "$_ikesu_guard_word" in printf|read|readarray|mapfile|let)
  case "$_ikesu_guard_line" in *'$'*|*'`+"`"+`'*) return 0;; esac;; esac
 case "$_ikesu_guard_word" in
 %s) return 0;;
 xargs|find|sh|bash|zsh|env|command|builtin|exec|eval|source|.|sudo|doas|timeout|stdbuf|arch|chroot|setsid|ionice|_ikesu_guard_*) return 0;;
 trap|unset|unsetenv|setenv|export|typeset|declare|local|readonly|unfunction|functions|alias|unalias|set|setopt|unsetopt|shopt|chmod|chown|chgrp|sed|truncate|mkdir|install) return 0;;
 esac
 return 1
}
_ikesu_guard_debug() {
 local _ikesu_guard_debug_busy=1 _ikesu_guard_at=2
 [[ ${FUNCNAME[2]-} == _ikesu_guard_startup_debug ]] && _ikesu_guard_at=3
 if [[ ${2-} != force ]]; then
  case "${FUNCNAME[_ikesu_guard_at]:-}:$1" in
%s  esac
  # A generated wrapper (git, command, ...) only forwards. Its frame runs no
  # user text except an ERR/RETURN trap, which is checked before it runs.
  case " ${_ikesu_guard_wrapper_names-} " in *" ${FUNCNAME[_ikesu_guard_at]:-} "*) [[ -n ${FUNCNAME[_ikesu_guard_at]:-} ]] && return 0;; esac
  case "$1" in
%s  esac
 fi
 if [[ %s ]]; then
%s  [[ ${2-} == force ]] || _ikesu_guard_needs_check "$1" || return 0
  (
   builtin trap - ERR
   builtin trap '' PIPE
   set +o functrace
   builtin compgen -v | while IFS= builtin read -r _ikesu_guard_snapshot_name; do
    case "$_ikesu_guard_snapshot_name" in _ikesu_guard_*) continue;; esac
    builtin declare -p "$_ikesu_guard_snapshot_name"
   done
   builtin :
  ) 2>/dev/null | (
   builtin trap - ERR
   set +o functrace
%s   %s guard line --via bash-debug --shell-pid "$$" -- "$1"
  ) || { builtin kill -s KILL "$$"; exit 1; }
 fi
 return 0
}
_ikesu_guard_dispatch() {
 local _ikesu_guard_frame
 if [[ ${_ikesu_guard_debug_busy:-} == 1 ]]; then
  for _ikesu_guard_frame in "${FUNCNAME[@]}"; do
   case "$_ikesu_guard_frame" in _ikesu_guard_debug|_ikesu_guard_trap_check) return 0;; esac
  done
 fi
 _ikesu_guard_debug "$1"
}
_ikesu_guard_trap_check() {
 local _ikesu_guard_debug_busy=1
 local _ikesu_guard_trap_ref="_ikesu_guard_trap_body_$1"
 if [[ $1 == RETURN ]]; then
  case "${FUNCNAME[1]-}" in _ikesu_guard_*|trap) return 1;; esac
  case " ${_ikesu_guard_wrapper_names-} " in *" ${FUNCNAME[1]-} "*) return 1;; esac
 fi
 builtin printf -v "_ikesu_guard_trap_saved_$1" '%%s' "${2:-0}"
 _ikesu_guard_debug "${!_ikesu_guard_trap_ref-}" force
 return 0
}
_ikesu_guard_trap_return() { return "${1:-0}"; }
_ikesu_guard_run_exit_trap() {
 local _ikesu_guard_in_wrapper=1 _ikesu_guard_exit_saved=$? _ikesu_guard_exit_mark= _ikesu_guard_exit_rc=0
 if { _ikesu_guard_exit_mark=$( { ( :; _ikesu_guard_trap_return "$_ikesu_guard_exit_saved" && :; builtin eval "$_ikesu_guard_trap_body_EXIT"; builtin printf done >&223; ) 223>&1 >&222 222>&-; } ); } 222>&1; then :; else _ikesu_guard_exit_rc=$?; fi
 [[ $_ikesu_guard_exit_mark == done ]] && return "$_ikesu_guard_exit_saved"
 builtin exit "$_ikesu_guard_exit_rc"
}
readonly -f _ikesu_guard_debug _ikesu_guard_dispatch _ikesu_guard_needs_check _ikesu_guard_trap_check _ikesu_guard_trap_return _ikesu_guard_run_exit_trap
fi
`, a.shellCore(), protectForward, bashSimpleForms, strings.Join(append(targets, quote("")), "|"), a.bashInternalCases(), a.bashWrapperCalls(), a.shellActive(), bashRequestReasonRestore, bashRequestReasonRestore, a.identity()+" builtin exec "+quote(a.RuntimePath()))
	}
	return fmt.Sprintf(`if [[ ! -o interactive ]]; then
if [[ %s ]] && [[ -n "${ZSH_EXECUTION_STRING:-}" ]]; then
 %s guard line --via exec-string -- "${ZSH_EXECUTION_STRING-}" || exit $?
fi
%ssetopt DEBUG_BEFORE_CMD
TRAPDEBUG() {
 case "${funcstack[2]:-}:${ZSH_DEBUG_CMD-}" in
%s esac
 case "${ZSH_DEBUG_CMD-}" in
%s esac
 if [[ %s ]]; then
  %s guard line --via zsh-debug -- "${ZSH_DEBUG_CMD-}" || exit $?
 fi
 return 0
}
fi
`, a.shellActive(), a.invocation(), a.zshTrapRewrap(), a.zshInternalCases(), a.zshWrapperCalls(), a.shellActive(), a.invocation())
}
