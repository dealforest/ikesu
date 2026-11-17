package guard

import (
	"context"
	"github.com/kazuph/ikesu/internal/childenv"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Probes are read-only, use the caller's environment, and remove our own shim.
// probeTimeout は判定のための問い合わせ（gh・git）を待つ長さ。試験では、負荷で新しい実行ファイルの
// 起動が遅れても落ちないよう TestMain で延ばす。
var probeTimeout = 3 * time.Second
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
	return has([]string{"main", "master", "refs/heads/main", "refs/heads/master"}, s)
}
func gitPush(i Input) Verdict {
	a := i.Args[1:]
	if force(a) {
		return result("block", "git-push-force", "git push --force 系は禁止です。履歴を書き換えず通常の push を使ってください。")
	}
	branch := probe(i, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	destMain := mainBranch(branch)
	count := 0
	remote := "origin"
	for _, v := range a {
		if v == "--" {
			break
		}
		if strings.HasPrefix(v, "-") {
			continue
		}
		count++
		if count == 1 {
			remote = v
			continue
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
	if i.gitDirUnknown {
		// The repository behind an unresolved -C is unknown, so neither the
		// .allow-main permit nor ownership of the current directory applies.
		if destMain {
			return result("block", "git-main", "main/master への push は AI からは禁止です。必要なら人間が手動で実行してください。")
		}
		return askAt("ownership", "git -C の場所を確定できないため承認が必要です。", TierL2, DestGitHub)
	}
	if destMain {
		root := probe(i, "git", "rev-parse", "--show-toplevel")
		if root != "" && regularFile(filepath.Join(root, ".allow-main")) {
			return result("allow", "main-permit", "main/master の許可ファイルあり")
		}
		return result("block", "git-main", "main/master への push は AI からは禁止です。必要なら人間が手動で実行してください。")
	}
	// push 先は、登録したリモートなら push 用の URL、そうでなければ引数（URL やパス）そのもの。
	// 登録していない名前を今いるリポとして判定しない（git push https://… が cwd のリポで通っていた）。
	url := probe(i, "git", "remote", "get-url", "--push", remote)
	configured := url != ""
	if !configured {
		url = remote
	}
	// URL から読めれば GitHub に聞かない（gh repo view は遅いと毎回3秒待っていた）。
	repo := repository(url)
	if repo == "" && configured {
		repo = probe(i, "gh", "repo", "view", url, "--json", "nameWithOwner", "--jq", ".nameWithOwner")
	}
	return ownerVerdict(i, repo, "push")
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
func githubLogin(i Input) string {
	cache := ownerCache(i)
	if cache != "" {
		if local := probe(i, "gh", "config", "get", "-h", "github.com", "user"); githubName.MatchString(local) && validApproval(cache, "login:github.com:"+local, time.Now()) {
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
		return ask
	}
	login := githubLogin(i)
	if login == "" {
		return ask
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
		return ask
	}
	if operation == "push" && typ != "" {
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
			n += 2
		case strings.HasPrefix(v, "--config-env="):
			if strings.HasPrefix(strings.ToLower(strings.TrimPrefix(v, "--config-env=")), "alias.") {
				i.gitAlias = true
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
