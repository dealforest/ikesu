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
	// The hook leaves out the functions the guard generated, by where they
	// were defined; a user's function with the same name stays in the list.
	for _, name := range strings.Fields(i.Env["IKESU_GUARD_SHELL_FUNCTIONS"]) {
		if !strings.HasPrefix(name, "_ikesu_guard_") {
			i.shellFunctions[name] = true
		}
	}
	i.trapBody = i.Env["IKESU_GUARD_TRAP_BODY"] == "1"
	i.trapNames = trapCalledNames(i.Env["IKESU_GUARD_TRAPS"], 0)
	return i
}

// trapCalledNames reads a listing of the registered traps (trap -p, or zsh's
// trap with its TRAP<SIG> functions) and returns every command name their
// texts call. Changing one of these names later changes what a trap runs.
func trapCalledNames(listing string, depth int) map[string]bool {
	names := map[string]bool{}
	if listing == "" || depth > 3 {
		return names
	}
	tree, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(listing), "trap listing")
	if err != nil {
		return names
	}
	syntax.Walk(tree, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		words := make([]string, len(call.Args))
		for k, word := range call.Args {
			words[k] = word.Lit()
			if words[k] == "" {
				if value, e := claudeLiteral(word); e {
					words[k] = value
				}
			}
		}
		if depth == 0 && words[0] == "trap" {
			args := words[1:]
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			if len(args) > 1 {
				for name := range trapCalledNames(args[0], depth+1) {
					names[name] = true
				}
			}
			return false
		}
		for len(words) > 1 && has([]string{"builtin", "command", "exec", "noglob", "nocorrect"}, words[0]) {
			names[words[0]] = true
			words = words[1:]
		}
		if words[0] != "" {
			names[words[0]] = true
		}
		return true
	})
	return names
}

// redefinesTrapName reports a line that defines, aliases, removes or disables
// a name a registered trap calls.
func redefinesTrapName(i Input, node syntax.Node, line string) bool {
	if len(i.trapNames) == 0 {
		return false
	}
	switch n := node.(type) {
	case *syntax.FuncDecl:
		return i.trapNames[n.Name.Value]
	case *syntax.CallExpr:
		if len(n.Args) < 2 {
			return false
		}
		words := make([]string, len(n.Args))
		for k, word := range n.Args {
			words[k] = strings.Trim(line[word.Pos().Offset():word.End().Offset()], "'\"")
		}
		for len(words) > 1 && has([]string{"builtin", "command"}, words[0]) {
			words = words[1:]
		}
		if !has([]string{"alias", "unalias", "unset", "unfunction", "disable", "enable", "hash"}, words[0]) {
			return false
		}
		for _, word := range words[1:] {
			if strings.HasPrefix(word, "-") {
				continue
			}
			if i.trapNames[strings.SplitN(word, "=", 2)[0]] {
				return true
			}
		}
	}
	return false
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

// bashTrapWrapper is defined before the user's BASH_ENV. It wraps the text
// of every trap but DEBUG in a check (see _ikesu_guard_trap_check) and keeps
// the text and the definitions of the names it may call in read-only records.
// The text itself runs in place, by bash: the check only comes first, and
// _ikesu_guard_trap_return gives the text the status from before the trap.
// The listings show the registered text, as a bash without the guard does.
// _ikesu_guard_wrap_trap is the same function, read-only, for the startup.
const bashTrapWrapper = `trap() {
 local _ikesu_guard_in_wrapper=1 _ikesu_guard_trap_out= _ikesu_guard_trap_sig= _ikesu_guard_trap_kind= _ikesu_guard_trap_text= _ikesu_guard_trap_view= _ikesu_guard_trap_rc=0 _ikesu_guard_trap_hide= _ikesu_guard_trap_n= _ikesu_guard_trap_k= IFS=$' \t\n'
 local -a _ikesu_guard_trap_words=()
 # Without errtrace a function sees no ERR trap; the listing shows none there.
 [[ $- == *E* || ${FUNCNAME[1]-main} == main || ${FUNCNAME[1]} == source ]] || _ikesu_guard_trap_hide=1
 if [[ $# == 0 || $1 == -p ]]; then
  [[ $# == 0 ]] || builtin shift
  if [[ $# == 0 ]]; then
   _ikesu_guard_trap_out=$(builtin trap -p) || _ikesu_guard_trap_rc=$?
  else
   for _ikesu_guard_trap_sig in "$@"; do
    case "$_ikesu_guard_trap_sig" in [Ee][Rr][Rr]|[Ss][Ii][Gg][Ee][Rr][Rr]) continue;; esac
    _ikesu_guard_trap_text=$(builtin trap -p "$_ikesu_guard_trap_sig") || _ikesu_guard_trap_rc=$?
    _ikesu_guard_trap_out+=${_ikesu_guard_trap_out:+$'\n'}$_ikesu_guard_trap_text
   done
  fi
  # Show each wrapped text as registered. Entries are four words each.
  _ikesu_guard_trap_split "$_ikesu_guard_trap_out"
  _ikesu_guard_trap_out=
  _ikesu_guard_trap_k=0
  while [[ $((_ikesu_guard_trap_k + 3)) -lt ${#_ikesu_guard_trap_words[@]} ]]; do
   _ikesu_guard_trap_text=${_ikesu_guard_trap_words[_ikesu_guard_trap_k+2]}
   _ikesu_guard_trap_sig=${_ikesu_guard_trap_words[_ikesu_guard_trap_k+3]}
   if [[ $_ikesu_guard_trap_text == 'if _ikesu_guard_trap_check '* ]]; then
    builtin read -r _ _ _ikesu_guard_trap_n _ <<< "$_ikesu_guard_trap_text"
    _ikesu_guard_trap_view="_ikesu_guard_trap_body_$_ikesu_guard_trap_n"
    _ikesu_guard_trap_text=${!_ikesu_guard_trap_view-}
   fi
   # ERR comes before RETURN in the builtin's order.
   if [[ $_ikesu_guard_trap_sig == RETURN && -z $_ikesu_guard_trap_hide && -n ${_ikesu_guard_trap_rec_ERR-} ]] && { [[ $# == 0 ]] || _ikesu_guard_trap_has_err "$@"; }; then
    _ikesu_guard_trap_view="_ikesu_guard_trap_body_$_ikesu_guard_trap_rec_ERR"
    builtin printf -v _ikesu_guard_trap_view "trap -- '%s' ERR" "${!_ikesu_guard_trap_view//\'/\'\\\'\'}"
    _ikesu_guard_trap_out+=$_ikesu_guard_trap_view$'\n'
    _ikesu_guard_trap_hide=done
   fi
   builtin printf -v _ikesu_guard_trap_view "trap -- '%s' %s" "${_ikesu_guard_trap_text//\'/\'\\\'\'}" "$_ikesu_guard_trap_sig"
   _ikesu_guard_trap_out+=$_ikesu_guard_trap_view$'\n'
   _ikesu_guard_trap_k=$((_ikesu_guard_trap_k + 4))
  done
  if [[ -z $_ikesu_guard_trap_hide && -n ${_ikesu_guard_trap_rec_ERR-} ]] && { [[ $# == 0 ]] || _ikesu_guard_trap_has_err "$@"; }; then
   _ikesu_guard_trap_view="_ikesu_guard_trap_body_$_ikesu_guard_trap_rec_ERR"
   builtin printf -v _ikesu_guard_trap_view "trap -- '%s' ERR" "${!_ikesu_guard_trap_view//\'/\'\\\'\'}"
   _ikesu_guard_trap_out+=$_ikesu_guard_trap_view$'\n'
  fi
  builtin printf '%s' "$_ikesu_guard_trap_out"
  return "$_ikesu_guard_trap_rc"
 fi
 if [[ $1 == -P ]] && (( BASH_VERSINFO[0] > 5 || BASH_VERSINFO[0] == 5 && BASH_VERSINFO[1] >= 1 )); then
  builtin shift
  (( $# )) || { builtin trap -P; return $?; }
  for _ikesu_guard_trap_sig in "$@"; do
   case "$_ikesu_guard_trap_sig" in
   [Ee][Rr][Rr]|[Ss][Ii][Gg][Ee][Rr][Rr])
    [[ -z $_ikesu_guard_trap_hide && -n ${_ikesu_guard_trap_rec_ERR-} ]] || { builtin trap -P "$_ikesu_guard_trap_sig" || _ikesu_guard_trap_rc=$?; continue; }
    _ikesu_guard_trap_text="if _ikesu_guard_trap_check $_ikesu_guard_trap_rec_ERR ";;
   *) _ikesu_guard_trap_text=$(builtin trap -P "$_ikesu_guard_trap_sig") || { _ikesu_guard_trap_rc=$?; continue; };;
   esac
   if [[ $_ikesu_guard_trap_text == 'if _ikesu_guard_trap_check '* ]]; then
    builtin read -r _ _ _ikesu_guard_trap_n _ <<< "$_ikesu_guard_trap_text"
    _ikesu_guard_trap_view="_ikesu_guard_trap_body_$_ikesu_guard_trap_n"
    _ikesu_guard_trap_text=${!_ikesu_guard_trap_view-}
   fi
   builtin printf '%s\n' "$_ikesu_guard_trap_text"
  done
  return "$_ikesu_guard_trap_rc"
 fi
 # Any other option, such as -P before Bash 5.1, is the builtin's to answer.
 case $1 in -[!-]*) builtin trap "$@"; return $?;; esac
 [[ $1 != -- ]] || builtin shift
 # One operand, "-", "", or a leading unsigned number resets or ignores.
 case "${1-}" in ''|*[!0-9]*) _ikesu_guard_trap_rc=1;; *) _ikesu_guard_trap_rc=0;; esac
 if [[ $# -le 1 || $1 == - || -z $1 || $_ikesu_guard_trap_rc == 0 ]]; then
  builtin trap -- "$@" || return $?
  ! _ikesu_guard_trap_has_err "$@" || builtin unset _ikesu_guard_trap_rec_ERR
  return 0
 fi
 _ikesu_guard_trap_text=$1
 builtin shift
 # A new read-only record: the text, the words of the text that may name a
 # command, and their definitions now.
 _ikesu_guard_trap_n=${_ikesu_guard_trap_count:-0}
 while _ikesu_guard_trap_view="_ikesu_guard_trap_body_$_ikesu_guard_trap_n"; [[ -n ${!_ikesu_guard_trap_view+x} ]]; do
  _ikesu_guard_trap_n=$((_ikesu_guard_trap_n + 1))
 done
 _ikesu_guard_trap_count=$((_ikesu_guard_trap_n + 1))
 builtin read -r -a _ikesu_guard_trap_words <<< "${_ikesu_guard_trap_text//[!A-Za-z0-9_.:-]/ }"
 builtin printf -v "_ikesu_guard_trap_body_$_ikesu_guard_trap_n" '%s' "$_ikesu_guard_trap_text"
 builtin printf -v "_ikesu_guard_trap_names_$_ikesu_guard_trap_n" '%s' "${_ikesu_guard_trap_words[*]-}"
 builtin printf -v "_ikesu_guard_trap_defs_$_ikesu_guard_trap_n" '%s' "$(_ikesu_guard_trap_defs_of ${_ikesu_guard_trap_words[*]-})"
 [[ ${_ikesu_guard_finalized-} == 1 ]] || builtin printf -v "_ikesu_guard_trap_early_$_ikesu_guard_trap_n" 1
 builtin readonly "_ikesu_guard_trap_body_$_ikesu_guard_trap_n" "_ikesu_guard_trap_names_$_ikesu_guard_trap_n" "_ikesu_guard_trap_defs_$_ikesu_guard_trap_n"
 [[ ${_ikesu_guard_finalized-} == 1 ]] || builtin readonly "_ikesu_guard_trap_early_$_ikesu_guard_trap_n"
 _ikesu_guard_trap_rc=0
 for _ikesu_guard_trap_sig in "$@"; do
  case "$_ikesu_guard_trap_sig" in
  [Dd][Ee][Bb][Uu][Gg]|[Ss][Ii][Gg][Dd][Ee][Bb][Uu][Gg]) builtin trap -- "$_ikesu_guard_trap_text" "$_ikesu_guard_trap_sig" || _ikesu_guard_trap_rc=$?; continue;;
  [Ee][Rr][Rr]|[Ss][Ii][Gg][Ee][Rr][Rr]) _ikesu_guard_trap_kind=ERR;;
  [Rr][Ee][Tt][Uu][Rr][Nn]|[Ss][Ii][Gg][Rr][Ee][Tt][Uu][Rr][Nn]) _ikesu_guard_trap_kind=RETURN;;
  *) _ikesu_guard_trap_kind=OTHER;;
  esac
  _ikesu_guard_trap_view="if _ikesu_guard_trap_check $_ikesu_guard_trap_n $_ikesu_guard_trap_kind \"\$?\"; then _ikesu_guard_trap_return \"\$_ikesu_guard_trap_saved_$_ikesu_guard_trap_n\" && :"$'\n'"$_ikesu_guard_trap_text"$'\n'fi
  if builtin trap -- "$_ikesu_guard_trap_view" "$_ikesu_guard_trap_sig"; then
   [[ $_ikesu_guard_trap_kind != ERR ]] || _ikesu_guard_trap_rec_ERR=$_ikesu_guard_trap_n
  else
   _ikesu_guard_trap_rc=$?
  fi
 done
 return "$_ikesu_guard_trap_rc"
}
_ikesu_guard_trap_split() {
 # The caller's _ikesu_guard_trap_words gets the words of a trap -p listing.
 builtin eval "_ikesu_guard_trap_words=($1)"
}
_ikesu_guard_trap_defs_of() {
 builtin declare -f -- "$@" 2>/dev/null
 :
}
_ikesu_guard_trap_has_err() {
 local _ikesu_guard_trap_sig
 for _ikesu_guard_trap_sig in "$@"; do
  case "$_ikesu_guard_trap_sig" in [Ee][Rr][Rr]|[Ss][Ii][Gg][Ee][Rr][Rr]) return 0;; esac
 done
 return 1
}
`

// bashTrapWrappers is the trap wrapper and its read-only copy. The startup
// wraps traps through the copy, which a user's BASH_ENV cannot replace.
func bashTrapWrappers() string {
	fn := bashTrapWrapper[:strings.Index(bashTrapWrapper, "_ikesu_guard_trap_split() {")]
	return bashTrapWrapper + strings.Replace(fn, "trap() {", "_ikesu_guard_wrap_trap() {", 1) + "readonly -f _ikesu_guard_wrap_trap _ikesu_guard_trap_split _ikesu_guard_trap_defs_of _ikesu_guard_trap_has_err\n"
}

// zshStartupTraps judges, once, every trap the user's startup files left:
// trap … EXIT/ZERR/ERR and other signals, and TRAP<SIG> functions. The same
// judgment as a registration in the shell applies. It runs before the
// execution string is judged, which may end the shell. The startup TRAPDEBUG
// is removed first: it is the guard's, and the runtime one replaces it.
func (a *Assets) zshStartupTraps() string {
	return fmt.Sprintf(`if [[ %s ]]; then
 builtin unfunction TRAPDEBUG 2>/dev/null
 _ikesu_guard_zfuncs
 _ikesu_guard_ztraps
 if [[ -n $_ikesu_guard_trap_list ]]; then
  # A rejected text must not run when the shell exits here.
  IKESU_GUARD_SHELL_FUNCTIONS=$_ikesu_guard_zf %s guard line --via zsh-debug -- "$_ikesu_guard_trap_list" || {
   _ikesu_guard_trap_rc=$?
   builtin trap - EXIT ZERR ERR
   { builtin unfunction TRAPEXIT TRAPZERR } 2>/dev/null || :
   builtin exit "$_ikesu_guard_trap_rc"
  }
 fi
 unset _ikesu_guard_trap_list
fi
`, a.shellActive(), a.invocation())
}

// zshStartupHook comes before the user's .zshenv in a shell that is not
// interactive. It keeps the guard's shell directory (the loaders and the
// generated files) read-only, the
// helpers that list the user's functions and the traps, and a TRAPDEBUG for
// the startup that judges the lines of the user's files that register a trap
// (an EXIT text may run when a startup file ends the shell).
func (a *Assets) zshStartupHook() string {
	return fmt.Sprintf(`if [[ ! -o interactive ]]; then
typeset -gr _ikesu_guard_zdir=%s _ikesu_guard_zgen="${${(%%):-%%x}:h}"
_ikesu_guard_zfuncs() {
 # The functions the generated files defined are the guard's.
 local _ikesu_guard_n
 _ikesu_guard_zf=
 for _ikesu_guard_n in ${(k)functions}; do
  [[ ${functions_source[$_ikesu_guard_n]-} == ("$_ikesu_guard_zdir"|"$_ikesu_guard_zgen")/* ]] || _ikesu_guard_zf+="$_ikesu_guard_n "
 done
}
_ikesu_guard_ztraps() {
 # zsh lists the traps of this shell to a file, not to a subshell.
 local _ikesu_guard_f=%s/.traps.$$
 if builtin trap >| "$_ikesu_guard_f"; then
  _ikesu_guard_trap_list="$(<"$_ikesu_guard_f")"
 else
  _ikesu_guard_trap_list="trap -- unreadable EXIT"
 fi
 zmodload -F zsh/files b:zf_rm 2>/dev/null && zf_rm -f -- "$_ikesu_guard_f" 2>/dev/null
 :
}
setopt DEBUG_BEFORE_CMD
TRAPDEBUG() {
 # The loaders and the generated files are the guard's; the loader may name
 # the generated file by another path than the shell directory.
 [[ ${funcfiletrace[1]-} == ("$_ikesu_guard_zdir"|"$_ikesu_guard_zgen")/* ]] && return 0
 if [[ %s ]]; then
  case "${ZSH_DEBUG_CMD-}" in
  *trap*|*TRAP*|*functions*) _ikesu_guard_zfuncs; IKESU_GUARD_SHELL_FUNCTIONS=$_ikesu_guard_zf %s guard line --via zsh-debug -- "${ZSH_DEBUG_CMD-}" || exit $?;;
  esac
 fi
 return 0
}
fi
`, quote(filepath.Dir(a.RuntimePath())), quote(filepath.Dir(a.RuntimePath())), a.shellActive(), a.invocation())
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
