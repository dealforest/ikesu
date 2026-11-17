package guard

import (
	"context"
	"github.com/kazuph/ikesu/internal/childenv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Probes are read-only, use the caller's environment, and remove our own shim.
// probeTimeout は判定のための問い合わせ（gh・git）を待つ長さ。試験では、負荷で新しい実行ファイルの
// 起動が遅れても落ちないよう TestMain で延ばす。
var probeTimeout = 10 * time.Second
var ghProbeTimeout = 15 * time.Second

func probe(i Input, cmd string, a ...string) string {
	if i.probePrivate {
		return ""
	}
	wait := probeTimeout
	if cmd == "gh" {
		// gh は起動と鍵の読み出しだけで1〜3秒かかり、GitHub が遅いと3秒を超える（2026-11-17 実測 1.1〜3.7秒）。
		// 3秒で打ち切ると権限のある push でも確認が出ていた。答えは24時間覚えるので、待つのは初回だけ。
		wait = ghProbeTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	m := i.Env
	if i.probePath != "" {
		// 判定に使う gh・git は、ペインを開いたときの PATH から探す。行の中の PATH= や export で
		// 偽の gh・git に差し替えさせない。
		m = map[string]string{}
		for k, v := range i.Env {
			m[k] = v
		}
		m["PATH"] = i.probePath
	}
	env := environment(m)
	p, e := Resolve(cmd, env)
	if e != nil {
		return ""
	}
	if cmd == "git" && len(i.gitOptions) > 0 {
		a = append(append([]string{}, i.gitOptions...), a...)
	}
	c := childenv.CommandContext(ctx, p, a...)
	c.Dir = i.Cwd
	c.Env = childenv.Scrub(env)
	b, e := c.Output()
	if e != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
func force(a []string) bool {
	for _, v := range a {
		if has([]string{"--force", "--force-with-lease", "-f"}, v) || strings.HasPrefix(v, "--force-with-lease=") {
			return true
		}
	}
	return false
}
func mainBranch(s string) bool {
	s = strings.TrimPrefix(s, "+")
	if strings.ContainsAny(s, "*?[") {
		// ワイルドカードの refspec は main に届きうる。
		return true
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "refs/"), "heads/")
	return has([]string{"main", "master"}, s)
}

// abbreviates は、name が options のどれかの省略形（前方の一部）か。
func abbreviates(name string, options []string) bool {
	if len(name) <= 2 {
		return false
	}
	for _, o := range options {
		if strings.HasPrefix(o, name) {
			return true
		}
	}
	return false
}

// git push の長いオプション（正式な名前だけ。省略形は一覧に無いので聞く）。
var (
	gitPushForceOptions         = []string{"--force", "--force-with-lease", "--force-if-includes"}
	gitPushValueOptions         = []string{"--push-option", "--receive-pack", "--exec", "--repo"}
	gitPushAttachedValueOptions = []string{"--recurse-submodules", "--signed"}
	gitPushFlagOptions          = []string{"--dry-run", "--porcelain", "--delete", "--tags", "--follow-tags", "--no-follow-tags", "--atomic", "--no-atomic", "--no-force-with-lease", "--no-force-if-includes", "--set-upstream", "--thin", "--no-thin", "--quiet", "--verbose", "--progress", "--no-progress", "--no-verify", "--verify", "--signed", "--no-signed", "--ipv4", "--ipv6", "--no-recurse-submodules", "--prune"}
)

// pushConfigKey は、-c で渡すと push 先の URL やリモートを変えられる設定の名前か。
func pushConfigKey(kv string) bool {
	k := strings.ToLower(kv)
	return strings.HasPrefix(k, "url.") || strings.HasPrefix(k, "remote.") || strings.HasPrefix(k, "branch.") || strings.HasPrefix(k, "push.") || strings.HasPrefix(k, "include")
}

const gitMainReason = "main/master への push は AI からは禁止です。必要なら人間が手動で実行してください。"

func gitPush(i Input) Verdict {
	a := i.Args[1:]
	if force(a) {
		return result("block", "git-push-force", "git push --force 系は禁止です。履歴を書き換えず通常の push を使ってください。")
	}
	branch := probe(i, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	destMain := mainBranch(branch)
	var positional []string
	repoOption := ""
	all := false
	forceBlock := result("block", "git-push-force", "git push --force 系は禁止です。履歴を書き換えず通常の push を使ってください。")
	unknown := func(v string) Verdict {
		// git は長いオプションの省略形（--mirr など）も受け付ける。一覧に無い書き方は意味を確定できないので聞く。
		return askAt("ownership", "git push のオプション "+v+" の意味を確定できないため承認が必要です（省略形は使わず正式な名前で書いてください）。", TierL2, DestGitHub)
	}
	for n := 0; n < len(a); n++ {
		v := a[n]
		if v == "--" {
			// -- の後ろも remote と refspec。読み飛ばさない（git push -- origin main が通っていた）。
			positional = append(positional, a[n+1:]...)
			break
		}
		if strings.HasPrefix(v, "--") {
			name, value, hasValue := strings.Cut(v, "=")
			switch {
			case has(gitPushForceOptions, name):
				return forceBlock
			case name == "--mirror":
				// すべての ref を強制で書き換える。
				return result("block", "git-push-force", "git push --mirror は全 ref を強制で書き換えるため禁止です。")
			case name == "--all" || name == "--branches":
				all = true
			case has(gitPushValueOptions, name):
				if !hasValue {
					if n+1 >= len(a) {
						return unknown(v)
					}
					n++
					value = a[n]
				}
				if name == "--repo" {
					repoOption = value
				}
			case has(gitPushFlagOptions, name) && !hasValue:
			case has(gitPushAttachedValueOptions, name):
			case abbreviates(name, append([]string{"--mirror"}, gitPushForceOptions...)):
				// --force-w・--mirr などの省略形は強制の push。
				return forceBlock
			default:
				return unknown(v)
			}
			continue
		}
		if strings.HasPrefix(v, "-") && len(v) > 1 {
			// まとめた短いオプション（-fu・-qo値 など）を1文字ずつ読む。o の後ろは値。
			for k := 1; k < len(v); k++ {
				switch c := v[k]; {
				case c == 'f':
					return forceBlock
				case c == 'o':
					if k == len(v)-1 {
						n++
					}
					k = len(v)
				case strings.IndexByte("nuqvd46", c) >= 0:
				default:
					return unknown(v)
				}
			}
			continue
		}
		positional = append(positional, v)
	}
	remote := ""
	if len(positional) > 0 {
		remote = positional[0]
	} else if repoOption != "" {
		remote = repoOption
	}
	refspecs := []string{}
	if len(positional) > 1 {
		refspecs = positional[1:]
	}
	for _, v := range refspecs {
		if strings.HasPrefix(v, "+") {
			return result("block", "git-push-force", "+ を付けた refspec は強制 push なので禁止です。")
		}
		if strings.Contains(v, ":") {
			v = v[strings.LastIndex(v, ":")+1:]
			if v == "" || v == "HEAD" {
				v = branch
			}
		} else if v == "HEAD" {
			v = branch
		}
		destMain = destMain || mainBranch(v)
	}
	destMain = destMain || all
	if i.gitDirUnknown {
		// The repository behind an unresolved -C is unknown, so neither the
		// .allow-main permit nor ownership of the current directory applies.
		if destMain {
			return result("block", "git-main", gitMainReason)
		}
		return askAt("ownership", "git -C の場所を確定できないため承認が必要です。", TierL2, DestGitHub)
	}
	if destMain {
		root := probe(i, "git", "rev-parse", "--show-toplevel")
		if root != "" && regularFile(filepath.Join(root, ".allow-main")) {
			return result("allow", "main-permit", "main/master の許可ファイルあり")
		}
		return result("block", "git-main", gitMainReason)
	}
	if i.gitPushConfig {
		// 判定の git には -c を渡さないので、書き換えた後の push 先を確かめられない。
		return askAt("ownership", "-c で push 先や push の動きの設定（url.*・remote.*・branch.*・push.*・include*）を変えた push は、行き先を確定できないため承認が必要です。", TierL2, DestGitHub)
	}
	if strings.HasPrefix(remote, "-") {
		// 判定の git がオプションとして読んでしまう。
		return askAt("ownership", "push 先 "+remote+" を確定できないため承認が必要です。", TierL2, DestGitHub)
	}
	if remote == "" {
		// 引数なし: git と同じ順で push 先のリモートを決める。
		for _, key := range []string{"branch." + branch + ".pushRemote", "remote.pushDefault", "branch." + branch + ".remote"} {
			if branch == "" && strings.HasPrefix(key, "branch.") {
				continue
			}
			if remote = probe(i, "git", "config", "--get", key); remote != "" {
				break
			}
		}
		if remote == "" {
			remote = "origin"
		}
	}
	// push 先は、登録したリモートなら push 用の URL 全部、そうでなければ引数（URL やパス）そのもの。
	// 登録していない名前を今いるリポとして判定しない（git push https://… が cwd のリポで通っていた）。
	urls := strings.Fields(probe(i, "git", "remote", "get-url", "--push", "--all", remote))
	configured := len(urls) > 0
	if !configured {
		// 設定ファイルの url.*.pushInsteadOf は引数の URL も書き換える。判定の URL と実際の行き先がずれるので聞く。
		if probe(i, "git", "config", "--get-regexp", `^url\..*\.pushinsteadof$`) != "" {
			return askAt("ownership", "url.*.pushInsteadOf があり、URL を直接指定した push の行き先を確定できないため承認が必要です。", TierL2, DestGitHub)
		}
		if rewritten := probe(i, "git", "ls-remote", "--get-url", remote); rewritten != "" {
			remote = rewritten
		}
		urls = []string{remote}
	}
	var verdict Verdict
	for _, url := range urls {
		// URL から読めれば GitHub に聞かない（gh repo view は遅いと毎回3秒待っていた）。
		repo := repository(url)
		if repo == "" && configured {
			repo = probe(i, "gh", "repo", "view", url, "--json", "nameWithOwner", "--jq", ".nameWithOwner")
		}
		verdict = ownerVerdict(i, repo, "push")
		if verdict.Decision != "allow" {
			return verdict
		}
	}
	return verdict
}

// repository は github.com の URL（https://・ssh://・git@github.com:）から owner/name を読む。
// ほかのホストや、パスの途中に github.com を含むだけの URL は空を返す。
var githubURL = regexp.MustCompile(`^(?:(?:https?|ssh|git)://(?:[^@/]+@)?github\.com(?::[0-9]+)?/|[^@/:]+@github\.com:)([A-Za-z0-9-]+/[A-Za-z0-9._-]+?)(?:\.git)?/?$`)

func repository(s string) string {
	if m := githubURL.FindStringSubmatch(strings.TrimSpace(s)); m != nil {
		return m[1]
	}
	return ""
}

// 組織の push 権限を GitHub で確かめた結果を覚える長さと置き場所。GitHub の応答が遅い
// （probe の3秒を超える）ときに、確認済みのリポジトリへの push で毎回聞かないため
// （2026-11-17、gh api /user が 3.7秒・gh repo view が 10秒かかり、組織のブランチの push で
// ownership の確認が出た）。置き場所は guard の状態のフォルダの下で、AI が書こうとすると
// guard-boundary の確認（L3）になる。覚えるのは「権限あり」だけで、断られた結果は覚えない。
const ownerCacheDuration = 24 * time.Hour

func ownerCacheFile(root string) string { return filepath.Join(root, "guard", "owner-cache") }

var githubName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
var githubRepo = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)

// ownerCache は覚えた結果のファイル。github.com 以外（GH_HOST）を見る判定と、状態のフォルダが無い判定では覚えない。
func ownerCache(i Input) string {
	if i.stateRoot == "" || (i.Env["GH_HOST"] != "" && i.Env["GH_HOST"] != "github.com") {
		return ""
	}
	return ownerCacheFile(i.stateRoot)
}

// ghFound は、判定に使う gh を見つけられるか（probe と同じ PATH で探す）。
func ghFound(i Input) bool {
	m := map[string]string{}
	for k, v := range i.Env {
		m[k] = v
	}
	if i.probePath != "" {
		m["PATH"] = i.probePath
	}
	_, e := Resolve("gh", environment(m))
	return e == nil
}

// githubAPI は GitHub への問い合わせ。答えを覚えるとき（ownerCache が空でない）は、手元の gh 設定の
// 既定ホストが差し替えられても github.com に聞くよう、ホストを固定する。
func githubAPI(i Input, endpoint, jq string) string {
	if ownerCache(i) != "" {
		return probe(i, "gh", "api", "--hostname", "github.com", endpoint, "--jq", jq)
	}
	return probe(i, "gh", "api", endpoint, "--jq", jq)
}

// githubLogin は gh のログイン名。手元の gh の設定の値は、24時間以内に GitHub（gh api /user）で
// 確かめた名前と同じときだけ使う（ネットに出ない）。手元の設定は GH_CONFIG_DIR などで差し替えられるので、
// 確かめていない名前は使わず GitHub に聞き、答えを覚える。
// localGitHubUser は gh の設定ファイル（hosts.yml）から github.com の user を読む。gh を起動しない
// （負荷が高いと gh の起動だけで3秒を超え、確認が出ていた）。読めなければ空。
func localGitHubUser(i Input) string {
	dir := i.Env["GH_CONFIG_DIR"]
	if dir == "" && i.Env["XDG_CONFIG_HOME"] != "" {
		dir = filepath.Join(i.Env["XDG_CONFIG_HOME"], "gh")
	}
	if dir == "" && i.Env["HOME"] != "" {
		dir = filepath.Join(i.Env["HOME"], ".config", "gh")
	}
	if dir == "" {
		return ""
	}
	b, e := os.ReadFile(filepath.Join(dir, "hosts.yml"))
	if e != nil {
		return ""
	}
	in, indent := false, -1
	for _, line := range strings.Split(string(b), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		depth := len(line) - len(trimmed)
		if depth == 0 {
			in = strings.TrimRight(trimmed, " ") == "github.com:"
			indent = -1
			continue
		}
		if !in {
			continue
		}
		if indent < 0 {
			indent = depth
		}
		if depth == indent && strings.HasPrefix(trimmed, "user:") {
			return strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "user:")), `"'`)
		}
	}
	return ""
}

func githubLogin(i Input) string {
	cache := ownerCache(i)
	if cache != "" {
		local := localGitHubUser(i)
		if local == "" {
			local = probe(i, "gh", "config", "get", "-h", "github.com", "user")
		}
		if githubName.MatchString(local) && validApproval(cache, "login:github.com:"+local, time.Now()) {
			return local
		}
	}
	login := githubAPI(i, "/user", ".login")
	if cache != "" && githubName.MatchString(login) {
		_ = WriteApproval(cache, "login:github.com:"+login, time.Now().Add(ownerCacheDuration))
	}
	return login
}

func ownerVerdict(i Input, repo, operation string) Verdict {
	ask := askAt("ownership", "リモート・所有者を確認できないため承認が必要です。", TierL2, DestGitHub)
	if !strings.Contains(repo, "/") {
		return askAt("ownership", "push 先の GitHub のリポジトリを確定できないため承認が必要です。", TierL2, DestGitHub)
	}
	login := githubLogin(i)
	if login == "" {
		if !ghFound(i) {
			return askAt("ownership", "判定に使う gh が、ペインを開いたときの PATH に見つからないため承認が必要です。", TierL2, DestGitHub)
		}
		return askAt("ownership", "GitHub のログイン名を時間内に確かめられないため承認が必要です。", TierL2, DestGitHub)
	}
	owner, _, _ := strings.Cut(repo, "/")
	if owner == login {
		return result("allow", "owner", "所有リポジトリ")
	}
	cache, key := ownerCache(i), "write:github.com:"+login+":"+strings.ToLower(repo)
	if !githubRepo.MatchString(repo) {
		cache = ""
	}
	if cache != "" {
		if validApproval(cache, key, time.Now()) {
			return result("allow", "organization", "組織への書き込み権限あり（24時間以内に確認済み）")
		}
	}
	allowed := func() Verdict {
		if cache != "" {
			_ = WriteApproval(cache, key, time.Now().Add(ownerCacheDuration))
		}
		return result("allow", "organization", "組織への書き込み権限あり")
	}
	typ := githubAPI(i, "repos/"+repo, ".owner.type")
	if typ == "Organization" {
		p := githubAPI(i, "repos/"+repo, ".permissions.push")
		if p == "true" {
			return allowed()
		}
		if p == "false" {
			return result("block", "organization-denied", "組織 ("+owner+") への "+operation+" 権限がないため禁止です。")
		}
		p = githubAPI(i, "repos/"+repo+"/collaborators/"+login+"/permission", ".permission")
		if has([]string{"admin", "maintain", "write"}, p) {
			return allowed()
		}
		if has([]string{"read", "triage"}, p) {
			return result("block", "organization-denied", "組織 ("+owner+") への "+operation+" 権限がないため禁止です。")
		}
		return askAt("ownership", "組織 ("+owner+") への書き込み権限を GitHub で時間内に確かめられないため承認が必要です。", TierL2, DestGitHub)
	}
	if typ == "" {
		return askAt("ownership", "リポジトリ ("+repo+") の持ち主を GitHub で時間内に確かめられないため承認が必要です。", TierL2, DestGitHub)
	}
	if operation == "push" {
		return result("block", "foreign-repo", "非所有リポジトリ ("+owner+") への push は禁止です。")
	}
	if operation == "PR 作成" {
		return result("block", "foreign-repo", "AI による他人所有リポ ("+repo+") への PR 作成は禁止です。")
	}
	return ask
}

// normalizeGit reads git's global options off the front of the arguments so the
// rules see the real subcommand. -C accumulates into the working directory the
// judgment probes run in; the other options are handed to every git probe.
// It is idempotent: once the subcommand leads, nothing more is stripped.
func normalizeGit(i Input) Input {
	if filepath.Base(i.Command) != "git" {
		return i
	}
	a := i.Args
	n := 0
	var opts []string
	cwd := i.Cwd
	for n < len(a) {
		v := a[n]
		switch {
		case v == "-C":
			if n+1 >= len(a) {
				return i
			}
			if a[n+1] == unresolvedPlaceholder {
				i.gitDirUnknown = true
			}
			if d := a[n+1]; d != "" && d != unresolvedPlaceholder {
				if !filepath.IsAbs(d) {
					d = filepath.Join(cwd, d)
				}
				cwd = d
			}
			n += 2
		case v == "-c" || v == "--git-dir" || v == "--work-tree" || v == "--namespace" || v == "--config-env" || v == "--super-prefix":
			if n+1 >= len(a) {
				return i
			}
			// Only repository-locating options reach the probes: -c and the
			// like could run configured commands before anyone approves.
			if has([]string{"--git-dir", "--work-tree", "--namespace"}, v) {
				opts = append(opts, v, a[n+1])
			}
			if (v == "-c" || v == "--config-env") && strings.HasPrefix(strings.ToLower(a[n+1]), "alias.") {
				i.gitAlias = true
			}
			if (v == "-c" || v == "--config-env") && pushConfigKey(a[n+1]) {
				i.gitPushConfig = true
			}
			n += 2
		case strings.HasPrefix(v, "--config-env="):
			if strings.HasPrefix(strings.ToLower(strings.TrimPrefix(v, "--config-env=")), "alias.") {
				i.gitAlias = true
			}
			if pushConfigKey(strings.TrimPrefix(v, "--config-env=")) {
				i.gitPushConfig = true
			}
			n++
		case strings.HasPrefix(v, "--git-dir=") || strings.HasPrefix(v, "--work-tree=") || strings.HasPrefix(v, "--namespace="):
			opts = append(opts, v)
			n++
		case strings.HasPrefix(v, "--super-prefix=") || strings.HasPrefix(v, "--exec-path="):
			// Not passed to probes: --exec-path picks which git programs run.
			n++
		case has([]string{"--no-pager", "-P", "-p", "--paginate", "--no-optional-locks", "--bare", "--no-replace-objects", "--literal-pathspecs", "--glob-pathspecs", "--noglob-pathspecs", "--icase-pathspecs", "--no-lazy-fetch", "--no-advice"}, v):
			opts = append(opts, v)
			n++
		default:
			if n == 0 {
				return i
			}
			i.gitStripped = append(append([]string{}, i.gitStripped...), a[:n]...)
			i.Args = append([]string{}, a[n:]...)
			i.Cwd = cwd
			i.gitOptions = append(append([]string{}, i.gitOptions...), opts...)
			return i
		}
	}
	return i
}
