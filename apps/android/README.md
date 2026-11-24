# ikesu for Android

Android から ikesu の判断待ちを確認し、回答します。Mac 上の bridge は判断待ちを読み、Cloudflare の中継（`apps/relay`）へ外向きにつなぎます。スマホとは中継越しに、端から端まで暗号のまま話します。仕組みは `PROTOCOL.md` に書いてあります。Android アプリのパッケージ ID は `kazuph.ikesu`、プラグイン ID は `kazuph.android` です。

## 導入

macOS に Node.js と Xcode が必要です。リポジトリのルートから依存物を導入し、bridge をプラグインとしてリンクします。

```sh
cd apps/android/bridge
npm ci
ikesu plugin link "$PWD"
```

判断待ちが作成されると bridge が起動します。手動では `ikesu plugin action invoke start --plugin kazuph.android` を使います。設定フォルダの `config.json` で `enabled` と `later_minutes`（既定 10）を指定できます。中継の設定（`relay.json`）が無いとき、bridge は起動しません。中継の用意は `bridge/README.md` の `relay-setup` を見てください。

Android SDK を用意し、`apps/android` で `./gradlew assembleDebug` を実行します。USB で対象端末を接続し、端末を確認してから次を実行します。

```sh
adb devices -l
adb -s <端末ID> install -r app/build/outputs/apk/debug/app-debug.apk
```

PC の ikesu のグローバル `[menu]` → `Android pairing QR...` を開くと、選択中のワークスペースのポップアップに QR と6桁の入力欄が出ます。画面が小さいときは、案内に従ってウィンドウを広げると自動で表示されます。中止するには Esc または Ctrl-C を押します。SSH 接続中は、その接続先にリンクした bridge と設定を使います。同じ状態ディレクトリで同時に開始できるペアリングは1件です。

ポップアップは、同じワークスペースを表示しているクライアント間で共有されます。登録に使う QR が見えるため、ペアリングは信頼できる参加者だけが表示しているワークスペースで実行してください。

スマホの登録は2通りです。PC の前にいるときは `node bridge/main.mjs pair --name <PC の名前>` の QR をアプリで読み、アプリに出る6桁を PC に入力します。QR を読めないときは、アプリの「鍵を作る（公開鍵を出す）」で出た1行を PC で `node bridge/main.mjs devices add --name … --noise … --operate …` に渡し、出てきた接続情報をアプリの「接続情報で PC を追加」に貼ります。どちらも、アプリと PC に出る鍵の指紋が同じか確かめます。端末の通知許可も必要です。

端末を紛失したときは `node bridge/main.mjs revoke <端末 ID>` で外します（端末 ID は `devices` で見られます）。動いている bridge は、再起動なしでその端末の接続を切ります。

## 音声で答える

判断待ちのコメント欄の下の「🎤 話して入力」を押すと録音が始まり、もう一度押すと止めて文字にします。文字はコメント欄の後ろに足されます。文字起こしは、接続設定の「音声の文字起こし」に入れた OpenAI 互換の `/v1/audio/transcriptions` の接続先（例: Tailscale 経由の `http://ubuntu-3090:8002`）で行います。音声はこの接続先へだけ送ります。初回はマイクの許可が要ります。

## 通信と保存

bridge は登録した端末の鍵で握手が済むまで何も送りません。握手の後は判断待ちの題名だけを送り、顔・指紋で署名した後に中身を送って回答を受けます。中継は暗号文を渡すだけで、中身を読めません。Android が送れるのは質問の識別子、選択肢、コメント、画像、送信番号です。マシン、パス、コマンド、回答者の指定は bridge が決めます。

添付は4枚まで、1枚20MBまでです。画像を検査し、判断待ちの出どころへ保存してから、そのパスをコメントに追加します。保存失敗時は回答を送信しません。取消・期限切れでは添付を消し、回答済みの添付は7日後に消します。画像やURLの表示取得に失敗しても回答できます。

## 試験

```sh
cd apps/android/bridge && npm test
cd ../ && ./gradlew test
```

bridge の実サーバー試験は一時 HOME と隔離サーバーを使います。画像検査にはインストール済みの Mac パネルを使います。別の実パネルを使う場合は `ANDROID_TEST_NATIVE_BINARY` に実行ファイルの絶対パスを指定します。実機の検証も名前付き隔離 session と一時 HOME を使い、普段の判断待ちには接続しません。Pixel の確認では、対象端末のシリアルを明示します。
