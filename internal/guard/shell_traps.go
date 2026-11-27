package guard

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/syntax"
)

func trapSignal(signal string) string {
	return strings.TrimPrefix(strings.ToUpper(signal), "SIG")
}

func isZshTrapFunction(name string) bool {
	signal, ok := strings.CutPrefix(name, "TRAP")
	return ok && (has([]string{"EXIT", "ZERR", "DEBUG"}, signal) || unix.SignalNum("SIG"+signal) != 0)
}

func trapBodyText(body *syntax.Stmt, line string) string {
	return line[body.Pos().Offset():body.End().Offset()]
}

// trapRegistrationCommands judges what a trap registration will run. Every
// shell and signal is judged here, EXIT included: the shell runs the text
// itself later, where no DEBUG check may see its lines.
func trapRegistrationCommands(i Input) ([]Input, error) {
	args := i.Args
	if len(args) > 0 && has([]string{"-p", "-P", "-l"}, args[0]) {
		return nil, nil
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	// A single operand, "-", or a leading unsigned number resets the signals.
	if len(args) < 2 || args[0] == "-" || args[0] == "" || args[0] == ":" || unsignedNumber(args[0]) {
		return nil, nil
	}
	return trapActionCommands(i, args[0])
}

func unsignedNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// withShellFunctions reads the function names the generated hook passes for a
// line that may register or run a trap. The list only adds checks.
func withShellFunctions(i Input) Input {
	if i.shellFunctions != nil || i.Env == nil {
		return i
	}
	i.shellFunctions = map[string]bool{}
	// The guard's own functions forward to a judge or are this hook.
	own := append([]string{"command", "trap", "codex", "claude", "TRAPDEBUG"}, defaults(i.GuardCommands, (Config{}).Commands())...)
	for _, name := range strings.Fields(i.Env["IKESU_GUARD_SHELL_FUNCTIONS"]) {
		if !strings.HasPrefix(name, "_ikesu_guard_") && !has(own, name) {
			i.shellFunctions[name] = true
		}
	}
	i.trapBody = i.Env["IKESU_GUARD_TRAP_BODY"] == "1"
	return i
}

func trapActionCommands(i Input, action string) ([]Input, error) {
	if i.evalDepth >= scriptDepthLimit {
		return nil, fmt.Errorf("トラップの入れ子を安全に確定できません")
	}
	i.evalDepth++
	i.trapRegistration = true
	// A registration does not know the variable values at trap execution.
	i.shellVariables, i.shellArrays, i.Env, i.AuthorizationEnv = nil, nil, nil, nil
	commands, err := lineCommands(i, action)
	if err == nil {
		for _, cmd := range commands {
			if cmd.Unresolved || cmd.CwdUncertain {
				return commands, fmt.Errorf("トラップの実行時の引数・書き込み先を安全に確定できません")
			}
		}
	}
	if unresolvedFailure(err) {
		err = fmt.Errorf("トラップの実行内容を安全に確定できません: %v", err)
	}
	return commands, err
}

func knownTrapCall(i Input) bool {
	if i.shellFunctions[i.Command] {
		return false
	}
	return filepath.IsAbs(i.Command) || has([]string{
		":", "[", "cd", "echo", "exit", "false", "printf", "pwd", "return", "test", "trap", "true",
	}, i.Command)
}

// bashTrapWrapper is defined before the user's BASH_ENV. It wraps only ERR
// and RETURN, whose text runs after a check each time (see
// _ikesu_guard_trap_check), and shows that stored text for trap -p, trap and
// trap -P. Every other form goes to the builtin unchanged; an EXIT text is
// judged when the line registering it is judged.
const bashTrapWrapper = `trap() {
 local _ikesu_guard_in_wrapper=1 _ikesu_guard_trap_out= _ikesu_guard_trap_sig= _ikesu_guard_trap_name= _ikesu_guard_trap_text= _ikesu_guard_trap_view= _ikesu_guard_trap_rc=0 _ikesu_guard_trap_hide=
 # Without errtrace a function sees no ERR trap; the listing shows none there.
 [[ $- == *E* || ${FUNCNAME[1]-main} == main || ${FUNCNAME[1]} == source ]] || _ikesu_guard_trap_hide=ERR
 if [[ $# == 0 || $1 == -p ]]; then
  [[ $# == 0 ]] || builtin shift
  if [[ $# == 0 ]]; then
   _ikesu_guard_trap_out=$(builtin trap -p) || _ikesu_guard_trap_rc=$?
   for _ikesu_guard_trap_name in ERR RETURN; do
    _ikesu_guard_trap_text="_ikesu_guard_trap_shown_$_ikesu_guard_trap_name"
    [[ -n ${!_ikesu_guard_trap_text-} ]] || continue
    _ikesu_guard_trap_out=${_ikesu_guard_trap_out//"${!_ikesu_guard_trap_text}"/}
   done
   _ikesu_guard_trap_out=${_ikesu_guard_trap_out//$'\n\n'/$'\n'}
   _ikesu_guard_trap_out=${_ikesu_guard_trap_out#$'\n'}
   _ikesu_guard_trap_out=${_ikesu_guard_trap_out%$'\n'}
   [[ -z $_ikesu_guard_trap_out ]] || builtin printf '%s\n' "$_ikesu_guard_trap_out"
   for _ikesu_guard_trap_name in ERR RETURN; do
    [[ $_ikesu_guard_trap_name != "$_ikesu_guard_trap_hide" ]] || continue
    _ikesu_guard_trap_view="_ikesu_guard_trap_view_$_ikesu_guard_trap_name"
    [[ -z ${!_ikesu_guard_trap_view-} ]] || builtin printf '%s\n' "${!_ikesu_guard_trap_view}"
   done
   return "$_ikesu_guard_trap_rc"
  fi
  for _ikesu_guard_trap_sig in "$@"; do
   case "$_ikesu_guard_trap_sig" in [Ee][Rr][Rr]) _ikesu_guard_trap_name=ERR;; [Rr][Ee][Tt][Uu][Rr][Nn]) _ikesu_guard_trap_name=RETURN;; *) _ikesu_guard_trap_name=;; esac
   [[ $_ikesu_guard_trap_name != "$_ikesu_guard_trap_hide" ]] || _ikesu_guard_trap_name=
   _ikesu_guard_trap_view="_ikesu_guard_trap_view_$_ikesu_guard_trap_name"
   if [[ -n $_ikesu_guard_trap_name && -n ${!_ikesu_guard_trap_view-} ]]; then
    builtin printf '%s\n' "${!_ikesu_guard_trap_view}"
   else
    builtin trap -p "$_ikesu_guard_trap_sig" || _ikesu_guard_trap_rc=$?
   fi
  done
  return "$_ikesu_guard_trap_rc"
 fi
 if [[ $1 == -P ]] && (( BASH_VERSINFO[0] > 5 || BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 1 )); then
  builtin shift
  for _ikesu_guard_trap_sig in "$@"; do
   case "$_ikesu_guard_trap_sig" in [Ee][Rr][Rr]) _ikesu_guard_trap_name=ERR;; [Rr][Ee][Tt][Uu][Rr][Nn]) _ikesu_guard_trap_name=RETURN;; *) _ikesu_guard_trap_name=;; esac
   [[ $_ikesu_guard_trap_name != "$_ikesu_guard_trap_hide" ]] || _ikesu_guard_trap_name=
   _ikesu_guard_trap_text="_ikesu_guard_trap_body_$_ikesu_guard_trap_name"
   if [[ -n $_ikesu_guard_trap_name && -n ${!_ikesu_guard_trap_text+x} ]]; then
    builtin printf '%s\n' "${!_ikesu_guard_trap_text}"
   else
    builtin trap -P "$_ikesu_guard_trap_sig" || _ikesu_guard_trap_rc=$?
   fi
  done
  (( $# )) || builtin trap -P || _ikesu_guard_trap_rc=$?
  return "$_ikesu_guard_trap_rc"
 fi
 # Any other option, such as -P before Bash 5.1, is the builtin's to answer.
 case $1 in -[!-]*) builtin trap "$@"; return $?;; esac
 _ikesu_guard_trap_out=$1
 [[ $1 != -- ]] || _ikesu_guard_trap_out=${2-}
 case "$*" in *[Ee][Rr][Rr]*|*[Rr][Ee][Tt][Uu][Rr][Nn]*) ;; *) builtin trap "$@" || return $?; return 0;; esac
 [[ $1 != -- ]] || builtin shift
 # One operand, "-", or a leading unsigned number resets the signals.
 case "${1-}" in ''|*[!0-9]*) _ikesu_guard_trap_rc=1;; *) _ikesu_guard_trap_rc=0;; esac
 if [[ $# -le 1 || $1 == - || $_ikesu_guard_trap_rc == 0 ]]; then
  builtin trap -- "$@" || return $?
  for _ikesu_guard_trap_sig in "$@"; do
   case "$_ikesu_guard_trap_sig" in [Ee][Rr][Rr]) _ikesu_guard_trap_name=ERR;; [Rr][Ee][Tt][Uu][Rr][Nn]) _ikesu_guard_trap_name=RETURN;; *) continue;; esac
   builtin unset "_ikesu_guard_trap_body_$_ikesu_guard_trap_name" "_ikesu_guard_trap_view_$_ikesu_guard_trap_name" "_ikesu_guard_trap_shown_$_ikesu_guard_trap_name"
  done
  return 0
 fi
 _ikesu_guard_trap_rc=0
 builtin shift
 for _ikesu_guard_trap_sig in "$@"; do
  case "$_ikesu_guard_trap_sig" in [Ee][Rr][Rr]) _ikesu_guard_trap_name=ERR;; [Rr][Ee][Tt][Uu][Rr][Nn]) _ikesu_guard_trap_name=RETURN;; *) _ikesu_guard_trap_name=;; esac
  if [[ -z $_ikesu_guard_trap_name || -z $_ikesu_guard_trap_out ]]; then
   if builtin trap -- "$_ikesu_guard_trap_out" "$_ikesu_guard_trap_sig"; then
    [[ -z $_ikesu_guard_trap_name ]] || builtin unset "_ikesu_guard_trap_body_$_ikesu_guard_trap_name" "_ikesu_guard_trap_view_$_ikesu_guard_trap_name" "_ikesu_guard_trap_shown_$_ikesu_guard_trap_name"
   else
    _ikesu_guard_trap_rc=$?
   fi
   continue
  fi
  _ikesu_guard_trap_text="if _ikesu_guard_trap_check $_ikesu_guard_trap_name \"\$?\"; then _ikesu_guard_trap_return \"\$_ikesu_guard_trap_saved_$_ikesu_guard_trap_name\" && true"$'\n'"$_ikesu_guard_trap_out"$'\n'fi
  builtin printf -v "_ikesu_guard_trap_body_$_ikesu_guard_trap_name" '%s' "$_ikesu_guard_trap_out"
  _ikesu_guard_trap_view=${_ikesu_guard_trap_out//\'/\'\\\'\'}
  builtin printf -v "_ikesu_guard_trap_view_$_ikesu_guard_trap_name" "trap -- '%s' %s" "$_ikesu_guard_trap_view" "$_ikesu_guard_trap_name"
  _ikesu_guard_trap_view=${_ikesu_guard_trap_text//\'/\'\\\'\'}
  builtin printf -v "_ikesu_guard_trap_shown_$_ikesu_guard_trap_name" "trap -- '%s' %s" "$_ikesu_guard_trap_view" "$_ikesu_guard_trap_name"
  builtin trap -- "$_ikesu_guard_trap_text" "$_ikesu_guard_trap_name" || _ikesu_guard_trap_rc=$?
 done
 return "$_ikesu_guard_trap_rc"
}
`

// zshStartupTraps judges, once, every trap the user's startup files left:
// trap … EXIT/ZERR/ERR and other signals, and TRAP<SIG> functions. The same
// judgment as a registration in the shell applies. zsh runs no TRAPDEBUG
// inside a trap, so this is the only check these texts get. The listing goes
// through a private file: a subshell would not list the EXIT trap.
func (a *Assets) zshStartupTraps() string {
	return fmt.Sprintf(`if [[ %s ]] && [[ -n ${IKESU_GUARD_SHELL_DIR-} ]]; then
 _ikesu_guard_trap_file="${IKESU_GUARD_SHELL_DIR}/.traps.$$"
 if builtin trap >| "$_ikesu_guard_trap_file"; then
  _ikesu_guard_trap_list="$(<"$_ikesu_guard_trap_file")"
 else
  _ikesu_guard_trap_list="trap -- unreadable EXIT"
 fi
 zmodload -F zsh/files b:zf_rm 2>/dev/null && zf_rm -f -- "$_ikesu_guard_trap_file" 2>/dev/null
 if [[ -n $_ikesu_guard_trap_list ]]; then
  # A rejected EXIT text must not run when the shell exits here.
  IKESU_GUARD_SHELL_FUNCTIONS="${(k)functions}" %s guard line --via zsh-debug -- "$_ikesu_guard_trap_list" || {
   _ikesu_guard_trap_rc=$?
   builtin trap - EXIT
   builtin unfunction TRAPEXIT 2>/dev/null
   builtin exit "$_ikesu_guard_trap_rc"
  }
 fi
 unset _ikesu_guard_trap_file _ikesu_guard_trap_list
fi
`, a.shellActive(), a.invocation())
}

// zshInternalCases narrows the frame skip of TRAPDEBUG to the forwarding
// functions' own lines. zsh reports a whole compound command per event and
// indents it with tabs, so an event counts as the function's own line when,
// without tabs, it is part of the function's text without tabs.
func (a *Assets) zshInternalCases() string {
	if len(a.Config.Commands()) == 0 {
		return ""
	}
	return ` case "${funcstack[2]:-}" in _ikesu_guard_forward|_ikesu_guard_command)
  case "${ZSH_DEBUG_CMD-}" in 'local _ikesu_guard_in_wrapper=1'|'local _ikesu_guard_in_wrapper=1 ') return 0;; esac
  [[ ${_ikesu_guard_in_wrapper-} == 1 && ${_ikesu_guard_ztexts-} == *"${ZSH_DEBUG_CMD//$'\t'/}"* ]] && return 0;;
 esac
`
}

// zshWrapperCalls accepts the calls the generated wrappers make, only in the
// frame of that wrapper. zsh reports the whole "call || return $?" list.
func (a *Assets) zshWrapperCalls() string {
	var b strings.Builder
	for _, cmd := range a.Config.Commands() {
		fmt.Fprintf(&b, " %s) [[ ${funcstack[2]-} == %s ]] && return 0;;\n", quote("_ikesu_guard_forward "+quote(cmd)+` "$@" || return $?`), quote(cmd))
	}
	if len(a.Config.Commands()) > 0 {
		fmt.Fprintf(&b, " %s) [[ ${funcstack[2]-} == command ]] && return 0;;\n", quote(`_ikesu_guard_command "$@" || return $?`))
	}
	return b.String()
}
