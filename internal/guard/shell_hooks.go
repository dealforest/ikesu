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
	names := append(append([]string{}, a.Config.Commands()...), "command", "codex", "claude")
	for k, name := range names {
		names[k] = quote(name)
	}
	// Bash reports the call line of a function once more as the first event
	// in it. The caller's frame judged that line; a generated wrapper's frame
	// skips only that repeat.
	wrappers := "  " + strings.Join(names, "|") + `)
   if [[ ${BASH_SOURCE[_ikesu_guard_at]-} == "${_ikesu_guard_startup_file-}" ]]; then
    case "$1" in "${FUNCNAME[_ikesu_guard_at]}"|"${FUNCNAME[_ikesu_guard_at]} "*) return 0;; esac
   fi;;
`
	return `  case "${FUNCNAME[_ikesu_guard_at]:-}" in
  _ikesu_guard_forward|_ikesu_guard_command|trap|_ikesu_guard_wrap_trap)
   if [[ ${BASH_SOURCE[_ikesu_guard_at]-} == "${_ikesu_guard_startup_file-}" ]]; then
    case "$1" in "${FUNCNAME[_ikesu_guard_at]}"|"${FUNCNAME[_ikesu_guard_at]} "*|'local _ikesu_guard_in_wrapper=1'|'local _ikesu_guard_in_wrapper=1 '*) return 0;; esac
    [[ ${_ikesu_guard_in_wrapper-} == 1 && ${_ikesu_guard_texts-} == *"$1"* ]] && return 0
   fi;;
  _ikesu_guard_trap_check|_ikesu_guard_trap_return)
   case "$1" in "${FUNCNAME[_ikesu_guard_at]}"|"${FUNCNAME[_ikesu_guard_at]} "*|'local _ikesu_guard_debug_busy=1') return 0;; esac;;
  _ikesu_guard_trap_has_err|_ikesu_guard_trap_split|_ikesu_guard_trap_defs_of|_ikesu_guard_user_functions)
   # These only test their arguments or list the functions.
   [[ ${BASH_SOURCE[_ikesu_guard_at]-} == "${_ikesu_guard_startup_file-}" ]] && return 0;;
` + wrappers + `  esac
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
	// The wrapped trap text calls the check and the status helper. Neither runs
	// user text: the check judges the recorded text, the helper returns.
	b.WriteString("  '_ikesu_guard_trap_check '*|'_ikesu_guard_trap_return \"$_ikesu_guard_trap_saved_'*) return 0;;\n")
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
   case "${2-}:$1" in force:*|*trap*) builtin export IKESU_GUARD_SHELL_FUNCTIONS="$(_ikesu_guard_user_functions)";; esac
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
_ikesu_guard_user_functions() {
 # The functions this startup file defined are the guard's; a user's function
 # of the same name was defined elsewhere and stays in the list.
 local _ikesu_guard_name _ikesu_guard_line _ikesu_guard_file IFS=$' \t\n'
 builtin shopt -s extdebug
 builtin declare -F $(builtin compgen -A function) 2>/dev/null | while builtin read -r _ikesu_guard_name _ikesu_guard_line _ikesu_guard_file; do
  [[ $_ikesu_guard_file == "${_ikesu_guard_startup_file-}" ]] || builtin printf '%%s\n' "$_ikesu_guard_name"
 done
}
_ikesu_guard_trap_check() {
 # $1 is the record of the registered text, $2 its kind, $3 the status at
 # the trap. The record and the definitions taken then are read-only.
 local _ikesu_guard_debug_busy=1
 local IFS=$' \t\n' _ikesu_guard_trap_ref="_ikesu_guard_trap_body_$1" _ikesu_guard_trap_defs="_ikesu_guard_trap_defs_$1" _ikesu_guard_trap_names="_ikesu_guard_trap_names_$1" _ikesu_guard_trap_early="_ikesu_guard_trap_early_$1"
 builtin printf -v "_ikesu_guard_trap_saved_$1" '%%s' "${3:-0}"
 # RETURN also runs when a guard function returns and when source leaves
 # a startup file. Those are not the user's returns.
 if [[ $2 == RETURN ]]; then
  case "${FUNCNAME[1]-}" in _ikesu_guard_*) return 1;; esac
  if [[ -n ${BASH_SOURCE[1]-} && ${BASH_SOURCE[1]} == "${_ikesu_guard_startup_file-}" ]]; then
   case " source trap command codex claude ${_ikesu_guard_wrapper_names-} " in *" ${FUNCNAME[1]-} "*) return 1;; esac
  fi
  [[ ${FUNCNAME[1]-} == source && -n ${_ikesu_guard_startup_loader-} && ${BASH_SOURCE[1]-} == "$_ikesu_guard_startup_loader" ]] && return 1
 fi
 # ERR and RETURN texts are judged each time, with the current variables.
 if [[ $2 == ERR || $2 == RETURN ]]; then
  _ikesu_guard_debug "${!_ikesu_guard_trap_ref-}" force
  return 0
 fi
 # Other texts were judged when registered. They are judged again when a
 # name they call is defined differently now, or when the startup registered
 # them and has not judged them yet.
 if [[ -n ${!_ikesu_guard_trap_early-} && ${_ikesu_guard_finalized-} != 1 ]] || [[ "$(_ikesu_guard_trap_defs_of ${!_ikesu_guard_trap_names-})" != "${!_ikesu_guard_trap_defs-}" ]]; then
  _ikesu_guard_debug "${!_ikesu_guard_trap_ref-}" force
 fi
 return 0
}
_ikesu_guard_trap_return() { return "${1:-0}"; }
%s_ikesu_guard_texts=$(builtin declare -f _ikesu_guard_forward _ikesu_guard_command trap _ikesu_guard_wrap_trap 2>/dev/null)
readonly _ikesu_guard_texts
readonly -f _ikesu_guard_debug _ikesu_guard_dispatch _ikesu_guard_needs_check _ikesu_guard_trap_check _ikesu_guard_trap_return _ikesu_guard_user_functions
fi
`, bashSimpleForms, strings.Join(append(targets, quote("")), "|"), a.bashInternalCases(), a.bashWrapperCalls(), a.shellActive(), bashRequestReasonRestore, bashRequestReasonRestore, a.identity()+" builtin exec "+quote(a.RuntimePath()), bashTrapWrappers())
	}
	return fmt.Sprintf(`if [[ ! -o interactive ]]; then
%sif [[ %s ]] && [[ -n "${ZSH_EXECUTION_STRING:-}" ]]; then
 %s guard line --via exec-string -- "${ZSH_EXECUTION_STRING-}" || exit $?
fi
setopt DEBUG_BEFORE_CMD
TRAPDEBUG() {
%s case "${ZSH_DEBUG_CMD-}" in
%s esac
 if [[ %s ]]; then
  case "${ZSH_DEBUG_CMD-}" in
  # A line that registers a trap, or that may define, alias or remove a name
  # a registered trap calls, is judged with the functions and the traps.
  *trap*|*TRAP*|*functions*|*' () '*|*alias*|*unset*|*unfunction*|*disable*|*enable*|*hash*) _ikesu_guard_zfuncs; _ikesu_guard_ztraps; IKESU_GUARD_SHELL_FUNCTIONS=$_ikesu_guard_zf IKESU_GUARD_TRAPS=$_ikesu_guard_trap_list %s guard line --via zsh-debug -- "${ZSH_DEBUG_CMD-}" || exit $?;;
  *) %s guard line --via zsh-debug -- "${ZSH_DEBUG_CMD-}" || exit $?;;
  esac
 fi
 return 0
}
fi
`, a.zshStartupTraps(), a.shellActive(), a.invocation(), a.zshInternalCases(), a.zshWrapperCalls(), a.shellActive(), a.invocation(), a.invocation())
}
