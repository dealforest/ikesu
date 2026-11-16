# bridge の中継接続（relay）

約束は `apps/android/PROTOCOL.md`。今までの合言葉の待ち受け（Tailscale）はそのまま動く。`relay.json` があるときだけ、`run` が中継へも外向きにつなぐ。

## 準備

1. 中継の設定を作る。`relay.json`（部屋 ID と RELAY_KEY を含む）が設定フォルダに 0600 で書かれる。
   `node apps/android/bridge/main.mjs relay-setup --url wss://<中継の Worker>`
   画面に出る RELAY_KEY を、中継の Worker に入れる: `printf '%s' '<値>' | npx wrangler secret put RELAY_KEY`
   すでに `relay.json` があるときは上書きしない。作り直すと登録済みの端末がつながらなくなる（`--force` で上書き）。
   決めた RELAY_KEY を使うときは `--key <base64url の32バイト>` か環境変数 `RELAY_KEY`。
2. `run` を再起動する（`relay.json` の読み込みは起動時）。

## 端末の登録

- `node apps/android/bridge/main.mjs pair --name <PC の名前> [--permissions notify,answer,screen,input]`
  端末に QR が出る（5分有効）。アプリで読むと、アプリに6桁が出る。それを端末に打つと登録される。
  `run` が動いていればそこへ頼み、動いていなければこの端末が一時的に中継へつなぐ。

登録済みの端末（端末 ID・名前・権限・登録日時）は `node apps/android/bridge/main.mjs devices` で見る。

QR を読めないときは、アプリの公開鍵を受け取って手で登録する。

`node apps/android/bridge/main.mjs devices add --name <端末名> --noise <Noise の公開鍵 base64url 32バイト> --operate <操作用の公開鍵 SPKI DER の base64url> [--pc-name <PC の名前>]`

形を検査して `devices.json` に入れ（権限は全部）、アプリに貼る接続情報を1行の JSON で出す。秘密は含まない。`run` 中の bridge には再起動なしで効く。

端末を外すには `node apps/android/bridge/main.mjs revoke <端末 ID>` を実行する。`run` 中の bridge は `devices.json` の変更を見て、再起動なしで接続を切る。

設定フォルダは `IKESU_PLUGIN_CONFIG_DIR`（既定 `~/.config/ikesu/plugins/config/kazuph.android`）。`noise-key.json`・`devices.json`・`relay.json` は 0600 で、壊れていると失敗として扱う（推測で直さない）。
