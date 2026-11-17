package guard

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kazuph/ikesu/internal/api"
)

// ownerFixture は、引数を記録する偽の gh と git を置いた、guard が有効なペインの環境を作る。
// ファイル down があると、gh は GitHub への問い合わせ（api）に失敗する（GitHub が遅い・落ちているとき）。
// gh config get は GH_CONFIG_DIR があればその中の login を返す（手元の設定の差し替え）。
func ownerFixture(t *testing.T, remote string) (string, Input, string) {
	t.Helper()
	root, bin := t.TempDir(), t.TempDir()
	gh := `#!/bin/sh
echo "$*" >> "` + bin + `/calls"
case "$1 $2" in
"config get") if [ -n "$GH_CONFIG_DIR" ]; then cat "$GH_CONFIG_DIR/login"; else echo kazuph; fi; exit 0;;
esac
[ -e "` + bin + `/down" ] && exit 1
case "$*" in
*"repo view"*) exit 1;;
*"--hostname github.com /user"*|"api /user --jq .login") echo kazuph; exit 0;;
*victim*".owner.type"*) echo User;;
*".owner.type"*) echo Organization;;
*".permissions.push"*) case "$*" in *denied*) echo false;; *) echo true;; esac;;
"api /user --jq .login") echo kazuph;;
*) exit 1;;
esac
`
	git := `#!/bin/sh
case "$*" in
*"symbolic-ref"*) echo feat;;
*"remote get-url --push origin"*) echo "` + remote + `";;
*) exit 1;;
esac
`
	for name, body := range map[string]string{"gh": gh, "git": git} {
		if e := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); e != nil {
			t.Fatal(e)
		}
	}
	a := NewAssets(root, "p1", "/usr/bin/true", Config{})
	env, err := a.Prepare([]string{"HOME=" + root, "PATH=" + bin + ":/usr/bin:/bin", "IKESU_PANE_ID=p1", "IKESU_SOCKET_PATH=" + root + "/socket"}, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Sync("codex"); err != nil {
		t.Fatal(err)
	}
	i := Input{Cwd: root, Env: EnvMap(env)}
	i.stateRoot = root
	return root, i, bin
}

func githubDown(t *testing.T, bin string) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(bin, "down"), nil, 0o600); e != nil {
		t.Fatal(e)
	}
}

func TestOwnerVerdictLoginLocalOnlyAfterGitHubConfirmed(t *testing.T) {
	_, i, bin := ownerFixture(t, "")
	if v := ownerVerdict(i, "kazuph/ikesu", "push"); v.Decision != "allow" || v.Rule != "owner" {
		t.Fatalf("own repo: %+v", v)
	}
	// 一度 GitHub で確かめた名前は、GitHub が答えなくても手元の設定から使う。
	githubDown(t, bin)
	if v := ownerVerdict(i, "kazuph/other", "push"); v.Decision != "allow" || v.Rule != "owner" {
		t.Fatalf("own repo while GitHub is down: %+v", v)
	}
}

func TestOwnerVerdictRemembersOrganizationPush(t *testing.T) {
	_, i, bin := ownerFixture(t, "")
	if v := ownerVerdict(i, "Org/App", "push"); v.Decision != "allow" {
		t.Fatalf("first: %+v", v)
	}
	githubDown(t, bin)
	if v := ownerVerdict(i, "org/app", "push"); v.Decision != "allow" || !strings.Contains(v.Reason, "確認済み") {
		t.Fatalf("cached: %+v", v)
	}
	if v := ownerVerdict(i, "org/other", "push"); v.Decision != "ask" || v.Rule != "ownership" {
		t.Fatalf("unconfirmed repo while down: %+v", v)
	}
	// 状態のフォルダが無い判定と、別のホストを見る判定は、覚えた結果を使わない。
	x := i
	x.stateRoot = ""
	if v := ownerVerdict(x, "org/app", "push"); v.Decision != "ask" {
		t.Fatalf("no state root: %+v", v)
	}
	x = i
	x.Env = EnvMap(environment(i.Env))
	x.Env["GH_HOST"] = "ghe.example.com"
	if v := ownerVerdict(x, "org/app", "push"); v.Decision != "ask" {
		t.Fatalf("other host: %+v", v)
	}
}

func TestOwnerVerdictDoesNotRememberDeniedOrPersonal(t *testing.T) {
	root, i, _ := ownerFixture(t, "")
	if v := ownerVerdict(i, "org/denied", "push"); v.Decision != "block" {
		t.Fatalf("denied: %+v", v)
	}
	if v := ownerVerdict(i, "victim/repo", "push"); v.Decision != "block" || v.Rule != "foreign-repo" {
		t.Fatalf("personal: %+v", v)
	}
	b, _ := os.ReadFile(ownerCacheFile(root))
	if strings.Contains(string(b), "denied") || strings.Contains(string(b), "victim") {
		t.Fatalf("denied or personal remembered: %q", b)
	}
}

// 行の中で GH_CONFIG_DIR を差し替えて手元の設定の名前を偽っても、他人のリポへの push や PR は通らない。
func TestOwnerVerdictLocalConfigCannotSpoofLogin(t *testing.T) {
	_, i, _ := ownerFixture(t, "git@github.com:victim/repo.git")
	// 本物の名前を先に覚えた状態でも、偽の名前は使わない。
	if v := ownerVerdict(i, "kazuph/ikesu", "push"); v.Decision != "allow" {
		t.Fatalf("own repo: %+v", v)
	}
	fake := t.TempDir()
	if e := os.WriteFile(filepath.Join(fake, "login"), []byte("victim\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	for _, line := range []string{
		"git push origin HEAD:refs/heads/feat",
		"GH_CONFIG_DIR=" + fake + " git push origin HEAD:refs/heads/feat",
		"env GH_CONFIG_DIR=" + fake + " git push origin HEAD:refs/heads/feat",
		"GH_CONFIG_DIR=" + fake + " gh pr create --repo victim/repo --title x --body y",
	} {
		if _, v, _ := lineVerdict(Config{}, i, line); v.Decision == "allow" {
			t.Errorf("%s => allowed (%s)", line, v.Rule)
		}
	}
}

// Claude のペインなどが通る行の入口でも、覚えた結果で push を聞かずに通す。main は今までどおり止める。
func TestAuthorizeLineUsesRememberedOrganizationPush(t *testing.T) {
	root, i, bin := ownerFixture(t, "git@github.com:org/app.git")
	if v := ownerVerdict(i, "org/app", "push"); v.Decision != "allow" {
		t.Fatalf("first: %+v", v)
	}
	githubDown(t, bin)
	i.stateRoot = ""
	asked := false
	await := func(api.DecisionCreateParams, time.Duration) (api.Decision, error) {
		asked = true
		return api.Decision{}, errors.New("asked")
	}
	var out bytes.Buffer
	if !AuthorizeLine(Config{}, root, i, "git push origin HEAD:refs/heads/feat", &out, await) || asked {
		t.Fatalf("remembered push asked: %s", out.String())
	}
	out.Reset()
	if AuthorizeLine(Config{}, root, i, "git push origin HEAD:refs/heads/main", &out, await) {
		t.Fatalf("main push allowed: %s", out.String())
	}
}

// 登録していない URL への push は、今いるリポではなく URL の先で判定する。GitHub に repo view を聞かない。
func TestGitPushJudgesTheURLItPushesTo(t *testing.T) {
	_, i, bin := ownerFixture(t, "git@github.com:org/app.git")
	for line, want := range map[string]string{
		"git push origin HEAD:refs/heads/feat":                              "allow",
		"git push https://github.com/victim/repo.git HEAD:refs/heads/feat":  "block",
		"git push git@github.com:victim/repo HEAD:refs/heads/feat":          "block",
		"git push https://example.com/org/app.git HEAD:refs/heads/feat":     "ask",
		"git push https://example.com/github.com/org/app HEAD:refs/heads/x": "ask",
		"git push not-a-remote HEAD:refs/heads/feat":                        "ask",
	} {
		if _, v, _ := lineVerdict(Config{}, i, line); v.Decision != want {
			t.Errorf("%s => %s/%s, want %s", line, v.Decision, v.Rule, want)
		}
	}
	if c, _ := os.ReadFile(filepath.Join(bin, "calls")); strings.Contains(string(c), "repo view") {
		t.Fatalf("asked GitHub for repo view: %q", c)
	}
}

// 行の中の PATH= で偽の gh を前に置いても、判定はペインを開いたときの PATH の gh で行う。
func TestAuthorizeLineProbesWithPanePATH(t *testing.T) {
	root, i, bin := ownerFixture(t, "git@github.com:victim/repo.git")
	fake := t.TempDir()
	if e := os.WriteFile(filepath.Join(fake, "gh"), []byte("#!/bin/sh\ncase \"$*\" in *owner.type*) echo Organization;; *permissions.push*) echo true;; *) echo kazuph;; esac\n"), 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(filepath.Join(bin, "git"), filepath.Join(fake, "git")); e != nil {
		t.Fatal(e)
	}
	i.stateRoot = ""
	await := func(api.DecisionCreateParams, time.Duration) (api.Decision, error) {
		return api.Decision{}, errors.New("asked")
	}
	var out bytes.Buffer
	if AuthorizeLine(Config{}, root, i, "PATH="+fake+":/usr/bin:/bin git push origin HEAD:refs/heads/feat", &out, await) {
		t.Fatalf("fake gh allowed a push to a personal repo: %s", out.String())
	}
	if b, _ := os.ReadFile(ownerCacheFile(root)); strings.Contains(string(b), "victim") {
		t.Fatalf("fake answer remembered: %q", b)
	}
}

// PR 作成のログイン名の確認も、覚えた名前を使い、GitHub が遅くても block しない。
func TestPRCreateUsesRememberedLogin(t *testing.T) {
	_, i, bin := ownerFixture(t, "git@github.com:org/app.git")
	if v := ownerVerdict(i, "org/app", "PR 作成"); v.Decision != "allow" {
		c, _ := os.ReadFile(filepath.Join(bin, "calls"))
		t.Fatalf("first: %+v\n%s", v, c)
	}
	githubDown(t, bin)
	if _, v, _ := lineVerdict(Config{}, i, "gh pr create --repo org/app --title x --body y"); v.Decision != "allow" {
		t.Fatalf("PR create while GitHub is down: %s/%s %s", v.Decision, v.Rule, v.Reason)
	}
}

// ログイン名は gh を起動せず hosts.yml から読む（GitHub で確かめた名前と一致するときだけ）。
func TestLocalGitHubUserReadsHostsFile(t *testing.T) {
	dir := t.TempDir()
	hosts := "github.com:\n    users:\n        other:\n            user: nested\n    git_protocol: ssh\n    user: kazuph\nghe.example.com:\n    user: someone\n"
	if e := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte(hosts), 0o600); e != nil {
		t.Fatal(e)
	}
	if got := localGitHubUser(Input{Env: map[string]string{"GH_CONFIG_DIR": dir}}); got != "kazuph" {
		t.Fatalf("user = %q", got)
	}
	if got := localGitHubUser(Input{Env: map[string]string{"GH_CONFIG_DIR": t.TempDir()}}); got != "" {
		t.Fatalf("missing file = %q", got)
	}
}
