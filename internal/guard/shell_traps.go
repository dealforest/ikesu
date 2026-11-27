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

func trapRegistrationCommands(i Input) ([]Input, error) {
	args := i.Args
	if len(args) < 2 || args[0] == "-p" || args[0] == "-l" {
		return nil, nil
	}
	if args[0] == "--" {
		args = args[1:]
	}
	if len(args) < 2 || args[0] == "-" || args[0] == "" || args[0] == ":" {
		return nil, nil
	}
	zsh := i.LineVia == "zsh-debug" || i.LineVia == "accept-line"
	for _, signal := range args[1:] {
		if zsh || !has([]string{"EXIT", "0"}, trapSignal(signal)) {
			return trapActionCommands(i, args[0])
		}
	}
	return nil, nil
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
	return filepath.IsAbs(i.Command) || has([]string{
		":", "[", "cd", "echo", "exit", "false", "printf", "pwd", "return", "test", "trap", "true",
	}, i.Command)
}

// bashFrameLines are the later lines of the generated functions as bash's
// DEBUG trap reports them (see bashInternalLines). Bash rebuilds the text it
// reports, so a few lines appear in the forms bash 3.2 and 5 print.
func (a *Assets) bashFrameLines(frame string) []string {
	restore := strings.TrimSuffix(strings.TrimSuffix(bashRequestReasonRestore, "\n"), " || exit 1")
	exec := a.invocation() + ` guard exec --via shell-func -- "$@"`
	switch frame {
	case "_ikesu_guard_forward":
		return []string{
			"[[ " + a.shellActive() + " ]]",
			`[[ -n "${BASH_VERSION:-}" && -n ${_ikesu_guard_reason_name+x} ]]`,
			`[[ -n "${BASH_VERSION:-}" && ${_ikesu_guard_reason_name+x} ]]`,
			"set +o functrace", restore, "exit 1", exec,
			"( set +o functrace; " + restore + " || exit 1; " + exec + " )",
			"return $?", `builtin command "$@"`,
		}
	case "_ikesu_guard_command":
		return []string{`[[ "${1:-}" != -* ]]`, `case "${1:-}" in `, `_ikesu_guard_forward "$@"`, "return $?", "return 0", `builtin command "$@"`}
	case "_ikesu_guard_run_exit_trap":
		return bashExitTrapLines
	}
	return nil
}

var bashExitTrapLines = []string{
	`_ikesu_guard_exit_mark=$( { ( :; _ikesu_guard_trap_return "$_ikesu_guard_exit_saved" && :; builtin eval "$_ikesu_guard_trap_body_EXIT"; builtin printf done >&223; ) 223>&1 >&222 222>&-; } )`,
	":", `_ikesu_guard_trap_return "$_ikesu_guard_exit_saved"`, `builtin eval "$_ikesu_guard_trap_body_EXIT"`,
	"builtin printf done 1>&223", "builtin printf done >&223", "_ikesu_guard_exit_rc=$?",
	"[[ $_ikesu_guard_exit_mark == done ]]", `return "$_ikesu_guard_exit_saved"`, `builtin exit "$_ikesu_guard_exit_rc"`,
}

// bashTrapWrapper replaces the trap builtin after the user's BASH_ENV. EXIT
// and 0 run through _ikesu_guard_run_exit_trap; ERR and RETURN keep their
// text inline after a check. trap -p, trap -P and plain trap show the text
// the user registered. Other signals and options go to the builtin as is.
const bashTrapWrapper = `trap() {
 local _ikesu_guard_in_wrapper=1 _ikesu_guard_trap_out= _ikesu_guard_trap_name= _ikesu_guard_trap_sig= _ikesu_guard_trap_action= _ikesu_guard_trap_text= _ikesu_guard_trap_q= _ikesu_guard_trap_rc=0
 if [[ ${1-} == -- ]]; then
  builtin shift
  (( $# )) || { trap; return $?; }
 elif [[ $# == 0 || $1 == -p ]]; then
  _ikesu_guard_trap_out=$(builtin trap "$@") || _ikesu_guard_trap_rc=$?
  for _ikesu_guard_trap_name in EXIT ERR RETURN; do
   _ikesu_guard_trap_text="_ikesu_guard_trap_shown_$_ikesu_guard_trap_name"
   _ikesu_guard_trap_q="_ikesu_guard_trap_view_$_ikesu_guard_trap_name"
   [[ -n ${!_ikesu_guard_trap_text-} ]] || continue
   _ikesu_guard_trap_out=${_ikesu_guard_trap_out//"${!_ikesu_guard_trap_text}"/"${!_ikesu_guard_trap_q}"}
  done
  [[ -z $_ikesu_guard_trap_out ]] || builtin printf '%s\n' "$_ikesu_guard_trap_out"
  return "$_ikesu_guard_trap_rc"
 elif [[ $1 == -P ]]; then
  builtin shift
  for _ikesu_guard_trap_sig in "$@"; do
   case "$_ikesu_guard_trap_sig" in 0|[Ee][Xx][Ii][Tt]) _ikesu_guard_trap_name=EXIT;; [Ee][Rr][Rr]) _ikesu_guard_trap_name=ERR;; [Rr][Ee][Tt][Uu][Rr][Nn]) _ikesu_guard_trap_name=RETURN;; *) _ikesu_guard_trap_name=;; esac
   _ikesu_guard_trap_text="_ikesu_guard_trap_body_$_ikesu_guard_trap_name"
   if [[ -n $_ikesu_guard_trap_name && ${!_ikesu_guard_trap_text+x} ]]; then
    builtin printf '%s\n' "${!_ikesu_guard_trap_text}"
   else
    builtin trap -P "$_ikesu_guard_trap_sig" || _ikesu_guard_trap_rc=$?
   fi
  done
  return "$_ikesu_guard_trap_rc"
 elif [[ $1 == -?* ]]; then
  builtin trap "$@"
  return $?
 fi
 if [[ $# == 1 || $1 == - ]]; then
  builtin trap -- "$@" || return $?
  for _ikesu_guard_trap_sig in "$@"; do
   case "$_ikesu_guard_trap_sig" in 0|[Ee][Xx][Ii][Tt]) _ikesu_guard_trap_name=EXIT;; [Ee][Rr][Rr]) _ikesu_guard_trap_name=ERR;; [Rr][Ee][Tt][Uu][Rr][Nn]) _ikesu_guard_trap_name=RETURN;; *) continue;; esac
   builtin unset "_ikesu_guard_trap_body_$_ikesu_guard_trap_name" "_ikesu_guard_trap_view_$_ikesu_guard_trap_name" "_ikesu_guard_trap_shown_$_ikesu_guard_trap_name"
  done
  return 0
 fi
 _ikesu_guard_trap_action="$1"
 builtin shift
 for _ikesu_guard_trap_sig in "$@"; do
  case "$_ikesu_guard_trap_sig" in 0|[Ee][Xx][Ii][Tt]) _ikesu_guard_trap_name=EXIT;; [Ee][Rr][Rr]) _ikesu_guard_trap_name=ERR;; [Rr][Ee][Tt][Uu][Rr][Nn]) _ikesu_guard_trap_name=RETURN;; *) _ikesu_guard_trap_name=;; esac
  if [[ -z $_ikesu_guard_trap_name || -z $_ikesu_guard_trap_action ]]; then
   if builtin trap -- "$_ikesu_guard_trap_action" "$_ikesu_guard_trap_sig"; then
    [[ -z $_ikesu_guard_trap_name ]] || builtin unset "_ikesu_guard_trap_body_$_ikesu_guard_trap_name" "_ikesu_guard_trap_view_$_ikesu_guard_trap_name" "_ikesu_guard_trap_shown_$_ikesu_guard_trap_name"
   else
    _ikesu_guard_trap_rc=$?
   fi
   continue
  fi
  if [[ $_ikesu_guard_trap_name == EXIT ]]; then
   _ikesu_guard_trap_text=_ikesu_guard_run_exit_trap
  else
   _ikesu_guard_trap_text="if _ikesu_guard_trap_check $_ikesu_guard_trap_name \"\$?\"; then _ikesu_guard_trap_return \"\$_ikesu_guard_trap_saved_$_ikesu_guard_trap_name\" && :"$'\n'"$_ikesu_guard_trap_action"$'\n'fi
  fi
  builtin printf -v "_ikesu_guard_trap_body_$_ikesu_guard_trap_name" '%s' "$_ikesu_guard_trap_action"
  _ikesu_guard_trap_q=${_ikesu_guard_trap_action//\'/\'\\\'\'}
  builtin printf -v "_ikesu_guard_trap_view_$_ikesu_guard_trap_name" "trap -- '%s' %s" "$_ikesu_guard_trap_q" "$_ikesu_guard_trap_name"
  _ikesu_guard_trap_q=${_ikesu_guard_trap_text//\'/\'\\\'\'}
  builtin printf -v "_ikesu_guard_trap_shown_$_ikesu_guard_trap_name" "trap -- '%s' %s" "$_ikesu_guard_trap_q" "$_ikesu_guard_trap_name"
  builtin trap -- "$_ikesu_guard_trap_text" "$_ikesu_guard_trap_name" || _ikesu_guard_trap_rc=$?
 done
 return "$_ikesu_guard_trap_rc"
}
`

// zshInternalCases narrows the old frame skip of TRAPDEBUG. zsh reports a
// whole compound command per event and runs no TRAPDEBUG inside a trap, so
// in these frames only the function's own lines can run: they are skipped
// when the frame's flag is set, and the line that sets the flag itself.
func (a *Assets) zshInternalCases() string {
	var b strings.Builder
	for _, frame := range []string{"_ikesu_guard_forward", "_ikesu_guard_command"} {
		fmt.Fprintf(&b, " %s|%s) return 0;;\n", quote(frame+":local _ikesu_guard_in_wrapper=1"), quote(frame+":local _ikesu_guard_in_wrapper=1 "))
		fmt.Fprintf(&b, " %s*) [[ ${_ikesu_guard_in_wrapper-} == 1 ]] && return 0;;\n", quote(frame+":"))
	}
	return b.String()
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

// zshTrapRewrap wraps a ZERR trap left by the user's startup files. zsh runs
// no TRAPDEBUG inside a trap, so each run first sends the stored text to the
// judge. The text itself still runs inline, in the trap's own frame.
func (a *Assets) zshTrapRewrap() string {
	return fmt.Sprintf(`_ikesu_guard_trap_check() {
 local _ikesu_guard_trap_ref="_ikesu_guard_trap_body_$1"
 typeset -g "_ikesu_guard_trap_saved_$1=${2:-0}"
 if [[ %s ]]; then
  %s guard line --via zsh-debug -- "${(P)_ikesu_guard_trap_ref-}" || exit $?
 fi
 return 0
}
_ikesu_guard_trap_return() { return "${1:-0}"; }
if (( ${+functions[TRAPZERR]} )); then
 _ikesu_guard_trap_body_ZERR="${functions[TRAPZERR]}"
 builtin eval "TRAPZERR() {
if _ikesu_guard_trap_check ZERR \$?; then _ikesu_guard_trap_return \"\$_ikesu_guard_trap_saved_ZERR\" && :
${_ikesu_guard_trap_body_ZERR}
fi
}"
else
 _ikesu_guard_trap_body_ZERR="$(trap() { [[ ${@[-1]} == ZERR ]] && builtin print -rn -- "$2"; }; builtin eval "$(builtin trap)")"
 if [[ -n $_ikesu_guard_trap_body_ZERR ]]; then
  builtin trap "if _ikesu_guard_trap_check ZERR \$?; then _ikesu_guard_trap_return \"\$_ikesu_guard_trap_saved_ZERR\" && :
${_ikesu_guard_trap_body_ZERR}
fi" ZERR
 fi
fi
`, a.shellActive(), a.invocation())
}
