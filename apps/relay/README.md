# ikesu-relay

ikesu の Android アプリと、各 PC の bridge をつなぐ中継です。Cloudflare Worker と Durable Object で動きます。約束は `apps/android/PROTOCOL.md` の「2. 中継」です。

中継は暗号文を渡すだけで、Noise の鍵は持ちません。HMAC の接続タグの照合、部屋ごとの接続のつなぎ合わせ、上限の強制だけを行います。

## 仕組み

- `GET /v1/bridge/<room>`、`GET /v1/app/<room>/<device>`、`GET /v1/pair/<room>/<pairing>/<exp>` の3つ。すべて WebSocket で、`Authorization: IKESU-RELAY <16進のタグ>` を付けます。
- Worker が `RELAY_KEY` で HMAC-SHA256 を定数時間で照合します。合わなければ Durable Object を作らずに 401、長さ不正は 400、pair の期限切れは 401 です。
- 部屋（room）ごとに Durable Object が1つ。bridge 1本（新しいものが古いものを置き換える）とアプリ4本まで。Hibernation の WebSocket API を使い、役割と ID は `serializeAttachment` に置きます。
- 1通は 65,535 バイトまで。アプリごとに毎秒200通・毎秒4MBまで。bridge がいない部屋のアプリは 30 秒（`APP_WAIT_MS`）で切ります。

## 試験

```sh
npm install
npm test
```

workerd をローカルで動かして、15 件の試験を流します（`@cloudflare/vitest-plugin`）。

## デプロイ

1. `npm install`
2. Cloudflare にログインします。`npx wrangler login`
3. `RELAY_KEY`（base64url の32バイト）を作って secret に入れます。

   ```sh
   node -e 'console.log(require("crypto").randomBytes(32).toString("base64url"))'
   npx wrangler secret put RELAY_KEY
   ```

   同じ値を bridge とアプリの設定（`PROTOCOL.md` の relay key）にも入れます。
4. `npx wrangler deploy`
5. 出た `https://ikesu-relay.<サブドメイン>.workers.dev` を、`wss://` にして QR の `relay` に使います。

`RELAY_KEY` を入れ替えると、すべての端末がつながらなくなります（`PROTOCOL.md` の 5 章）。

## ローカルで動かす

`.dev.vars` に `RELAY_KEY=<base64url の32バイト>` を書いて `npx wrangler dev` を実行します。`.dev.vars` はコミットしません。
