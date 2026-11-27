package guard

import (
	"fmt"
	"strings"
)

// bashInternalCases skips the DEBUG events of the generated functions, only in
// their own frame. A frame counts as generated when its function was defined
// in this startup file. Its events are skipped when they are the call line
// that entered it, the line that sets its flag, or, with that flag set, a line
// of the function's own text as declare -f prints it.
func (a *Assets) bashInternalCases() string {
	return `  case "${FUNCNAME[_ikesu_guard_at]:-}" in
  _ikesu_guard_forward|_ikesu_guard_command|trap)
   if [[ ${BASH_SOURCE[_ikesu_guard_at]-} == "${_ikesu_guard_startup_file-}" ]]; then
    case "$1" in "${FUNCNAME[_ikesu_guard_at]}"|"${FUNCNAME[_ikesu_guard_at]} "*|'local _ikesu_guard_in_wrapper=1'|'local _ikesu_guard_in_wrapper=1 '*) return 0;; esac
    [[ ${_ikesu_guard_in_wrapper-} == 1 && ${_ikesu_guard_texts-} == *"$1"* ]] && return 0
   fi;;
  _ikesu_guard_trap_check|_ikesu_guard_trap_return)
   case "$1" in "${FUNCNAME[_ikesu_guard_at]}"|"${FUNCNAME[_ikesu_guard_at]} "*|'local _ikesu_guard_debug_busy=1') return 0;; esac;;
  esac
`
}

// bashWrapperCalls accepts the calls the generated wrappers make, only in the
// frame of a wrapper defined in this startup file, and the status helper the
// wrapped ERR/RETURN text calls (it only returns the saved number).
func (a *Assets) bashWrapperCalls() string {
	var b strings.Builder
	own := `[[ ${BASH_SOURCE[_ikesu_guard_at]-} == "${_ikesu_guard_startup_file-}" ]] && `
	for _, cmd := range a.Config.Commands() {
		fmt.Fprintf(&b, "  %s) [[ ${FUNCNAME[_ikesu_guard_at]-} == %s ]] && %sreturn 0;;\n", quote("_ikesu_guard_forward "+quote(cmd)+` "$@"`), quote(cmd), own)
	}
	if len(a.Config.Commands()) > 0 {
		fmt.Fprintf(&b, "  %s) [[ ${FUNCNAME[_ikesu_guard_at]-} == command ]] && %sreturn 0;;\n", quote(`_ikesu_guard_command "$@"`), own)
	}
	for _, line := range []string{`_ikesu_guard_trap_return "$_ikesu_guard_trap_saved_ERR"`, `_ikesu_guard_trap_return "$_ikesu_guard_trap_saved_RETURN"`} {
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
		return fmt.Sprintf(`if [[ $- != *i* ]]; then
%s_ikesu_guard_needs_check() {
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
 xargs|find|sh|bash|zsh|dash|ksh|env|command|builtin|exec|eval|source|.|sudo|doas|timeout|stdbuf|arch|chroot|setsid|ionice|_ikesu_guard_*) return 0;;
 trap|unset|unsetenv|setenv|export|typeset|declare|local|readonly|unfunction|functions|alias|unalias|set|setopt|unsetopt|shopt|chmod|chown|chgrp|sed|truncate|mkdir|install) return 0;;
 esac
 return 1
}
_ikesu_guard_debug() {
 local _ikesu_guard_debug_busy=1 _ikesu_guard_at=2
 [[ ${FUNCNAME[2]-} == _ikesu_guard_startup_debug ]] && _ikesu_guard_at=3
 if [[ ${2-} != force ]]; then
%s  case "$1" in
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
   # A trap text, or a line that may register one, is judged knowing which
   # names are shell functions. Both values only add checks.
   case "${2-}:$1" in force:*|*trap*) builtin export IKESU_GUARD_SHELL_FUNCTIONS="$(builtin compgen -A function)";; esac
   [[ ${2-} != force ]] || builtin export IKESU_GUARD_TRAP_BODY=1
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
 builtin printf -v "_ikesu_guard_trap_saved_$1" '%%s' "${2:-0}"
 # RETURN also runs when a guard function returns and when source leaves
 # a startup file. Those are not the user's returns.
 if [[ $1 == RETURN ]]; then
  case "${FUNCNAME[1]-}" in _ikesu_guard_*) return 1;; esac
  if [[ -n ${BASH_SOURCE[1]-} && ${BASH_SOURCE[1]} == "${_ikesu_guard_startup_file-}" ]]; then
   case " source trap command codex claude ${_ikesu_guard_wrapper_names-} " in *" ${FUNCNAME[1]-} "*) return 1;; esac
  fi
  [[ ${FUNCNAME[1]-} == source && -n ${_ikesu_guard_startup_loader-} && ${BASH_SOURCE[1]-} == "$_ikesu_guard_startup_loader" ]] && return 1
 fi
 _ikesu_guard_debug "${!_ikesu_guard_trap_ref-}" force
 return 0
}
_ikesu_guard_trap_return() { return "${1:-0}"; }
%s_ikesu_guard_texts=$(builtin declare -f _ikesu_guard_forward _ikesu_guard_command trap 2>/dev/null)
readonly _ikesu_guard_texts
readonly -f _ikesu_guard_debug _ikesu_guard_dispatch _ikesu_guard_needs_check _ikesu_guard_trap_check _ikesu_guard_trap_return
fi
`, bashSimpleForms, strings.Join(append(targets, quote("")), "|"), a.bashInternalCases(), a.bashWrapperCalls(), a.shellActive(), bashRequestReasonRestore, bashRequestReasonRestore, a.identity()+" builtin exec "+quote(a.RuntimePath()), bashTrapWrapper)
	}
	return fmt.Sprintf(`if [[ ! -o interactive ]]; then
if [[ %s ]] && [[ -n "${ZSH_EXECUTION_STRING:-}" ]]; then
 %s guard line --via exec-string -- "${ZSH_EXECUTION_STRING-}" || exit $?
fi
%ssetopt DEBUG_BEFORE_CMD
TRAPDEBUG() {
%s case "${ZSH_DEBUG_CMD-}" in
%s esac
 if [[ %s ]]; then
  case "${ZSH_DEBUG_CMD-}" in
  *trap*|*TRAP*|*functions*) IKESU_GUARD_SHELL_FUNCTIONS="${(k)functions}" %s guard line --via zsh-debug -- "${ZSH_DEBUG_CMD-}" || exit $?;;
  *) %s guard line --via zsh-debug -- "${ZSH_DEBUG_CMD-}" || exit $?;;
  esac
 fi
 return 0
}
fi
`, a.shellActive(), a.invocation(), a.zshStartupTraps(), a.zshInternalCases(), a.zshWrapperCalls(), a.shellActive(), a.invocation(), a.invocation())
}
