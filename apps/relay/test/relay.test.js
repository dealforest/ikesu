import { SELF } from "cloudflare:test";
import { describe, expect, it } from "vitest";

const enc = new TextEncoder();
const KEY = new Uint8Array(32).fill(1);
const b64u = (u) => btoa(String.fromCharCode(...u)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
const hex = (u) => Array.from(u, (b) => b.toString(16).padStart(2, "0")).join("");
const cat = (...p) => Uint8Array.from(p.flatMap((x) => Array.from(x)));
const rnd = (n) => crypto.getRandomValues(new Uint8Array(n));

let hk;
async function tag(...parts) {
  hk ??= await crypto.subtle.importKey("raw", KEY, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  return hex(new Uint8Array(await crypto.subtle.sign("HMAC", hk, cat(...parts))));
}
const u64 = (n) => {
  const o = new Uint8Array(8);
  new DataView(o.buffer).setBigUint64(0, BigInt(n));
  return o;
};

async function connect(path, tagHex) {
  const headers = { Upgrade: "websocket" };
  if (tagHex !== undefined) headers.Authorization = `IKESU-RELAY ${tagHex}`;
  const res = await SELF.fetch("https://relay.test" + path, { headers });
  if (!res.webSocket) return { res };
  const ws = res.webSocket;
  ws.binaryType = "arraybuffer";
  ws.accept();
  const q = [];
  const waiters = [];
  const c = { res, ws, closed: null, q };
  ws.addEventListener("message", (e) => {
    const d = typeof e.data === "string" ? e.data : new Uint8Array(e.data);
    if (waiters.length) waiters.shift()(d);
    else q.push(d);
  });
  c.closedP = new Promise((r) => ws.addEventListener("close", (e) => { c.closed = e.code; r(e.code); }));
  c.next = (ms = 2000) =>
    q.length
      ? Promise.resolve(q.shift())
      : Promise.race([
          new Promise((r) => waiters.push(r)),
          new Promise((_, rej) => setTimeout(() => rej(new Error("message timeout")), ms)),
        ]);
  c.waitClose = (ms = 3000) =>
    Promise.race([c.closedP, new Promise((_, rej) => setTimeout(() => rej(new Error("close timeout")), ms))]);
  return c;
}

const newRoom = () => rnd(32);
async function bridgeOf(room) {
  return connect(`/v1/bridge/${b64u(room)}`, await tag(enc.encode("bridge"), room));
}
async function appOf(room, device = rnd(16)) {
  return connect(`/v1/app/${b64u(room)}/${b64u(device)}`, await tag(enc.encode("app"), room, device));
}

describe("接続", () => {
  it("bridge とアプリがつながり、データが両方向に流れる", async () => {
    const room = newRoom();
    const device = rnd(16);
    const b = await bridgeOf(room);
    expect(b.res.status).toBe(101);
    const a = await appOf(room, device);
    expect(a.res.status).toBe(101);

    const open = await b.next();
    expect(open.length).toBe(34);
    expect(open[0]).toBe(0x01);
    expect(open[17]).toBe(0x01);
    expect(hex(open.slice(18))).toBe(hex(device));
    const id = open.slice(1, 17);

    a.ws.send(Uint8Array.of(9, 8, 7));
    const data = await b.next();
    expect(data[0]).toBe(0x02);
    expect(hex(data.slice(1, 17))).toBe(hex(id));
    expect(Array.from(data.slice(17))).toEqual([9, 8, 7]);

    b.ws.send(cat([0x02], id, [5, 6]));
    expect(Array.from(await a.next())).toEqual([5, 6]);
  });

  it("pair の接続は役割 0x02 と pairing ID を bridge に伝える", async () => {
    const room = newRoom();
    const pid = rnd(16);
    const exp = Math.floor(Date.now() / 1000) + 300;
    const b = await bridgeOf(room);
    const p = await connect(`/v1/pair/${b64u(room)}/${b64u(pid)}/${exp}`, await tag(enc.encode("pair"), room, pid, u64(exp)));
    expect(p.res.status).toBe(101);
    const open = await b.next();
    expect(open[17]).toBe(0x02);
    expect(hex(open.slice(18))).toBe(hex(pid));
  });
});

describe("断る", () => {
  it("タグ違い・タグ無し・形の悪い値は 401", async () => {
    const room = newRoom();
    const device = rnd(16);
    const path = `/v1/app/${b64u(room)}/${b64u(device)}`;
    const good = await tag(enc.encode("app"), room, device);
    const wrong = (good[0] === "0" ? "1" : "0") + good.slice(1);
    expect((await connect(path, wrong)).res.status).toBe(401);
    expect((await connect(path)).res.status).toBe(401);
    expect((await connect(path, "zz")).res.status).toBe(401);
    // 別の役割のタグは使い回せない
    expect((await connect(`/v1/bridge/${b64u(room)}`, good)).res.status).toBe(401);
  });

  it("pair の期限切れは 401、タグの exp とパスの exp が違っても 401", async () => {
    const room = newRoom();
    const pid = rnd(16);
    const past = Math.floor(Date.now() / 1000) - 10;
    const t = await tag(enc.encode("pair"), room, pid, u64(past));
    expect((await connect(`/v1/pair/${b64u(room)}/${b64u(pid)}/${past}`, t)).res.status).toBe(401);
    const exp = Math.floor(Date.now() / 1000) + 300;
    expect((await connect(`/v1/pair/${b64u(room)}/${b64u(pid)}/${exp + 1}`, await tag(enc.encode("pair"), room, pid, u64(exp)))).res.status).toBe(401);
  });

  it("長さの合わない room・device は 400", async () => {
    const t = "0".repeat(64);
    expect((await connect(`/v1/bridge/${b64u(rnd(31))}`, t)).res.status).toBe(400);
    expect((await connect(`/v1/app/${b64u(rnd(32))}/${b64u(rnd(15))}`, t)).res.status).toBe(400);
    expect((await connect(`/v1/pair/${b64u(rnd(32))}/${b64u(rnd(17))}/9999999999`, t)).res.status).toBe(400);
  });

  it("5本目のアプリは断る", async () => {
    const room = newRoom();
    const b = await bridgeOf(room);
    const apps = [];
    for (let i = 0; i < 4; i++) {
      const a = await appOf(room);
      expect(a.res.status).toBe(101);
      apps.push(a);
    }
    const fifth = await appOf(room);
    expect(fifth.res.status).toBe(429);
    // 1本切れたら、また入れる
    apps[0].ws.close(1000, "bye");
    await b.next(); // open 群のうち最初でよい（読み捨て）
    await new Promise((r) => setTimeout(r, 100));
    expect((await appOf(room)).res.status).toBe(101);
  });
});

describe("上限と切断", () => {
  it("65,535 バイトは通り、65,536 バイトで切られる", async () => {
    const room = newRoom();
    const b = await bridgeOf(room);
    const a = await appOf(room);
    await b.next();
    a.ws.send(new Uint8Array(65535));
    const ok = await b.next();
    expect(ok.length).toBe(17 + 65535);
    a.ws.send(new Uint8Array(65536));
    expect(await a.waitClose()).toBe(1009);
    const close = await b.next();
    expect(close[0]).toBe(0x03);
  });

  it("bridge の close でアプリが切れる", async () => {
    const room = newRoom();
    const b = await bridgeOf(room);
    const a = await appOf(room);
    const id = (await b.next()).slice(1, 17);
    b.ws.send(cat([0x03], id));
    await a.waitClose();
    expect(a.closed).toBe(1000);
  });

  it("bridge の WebSocket が閉じるとアプリも全部切れる", async () => {
    const room = newRoom();
    const b = await bridgeOf(room);
    const a1 = await appOf(room);
    const a2 = await appOf(room);
    b.ws.close(1000, "gone");
    await a1.waitClose();
    await a2.waitClose();
  });

  it("アプリが切れると bridge に close が届く", async () => {
    const room = newRoom();
    const b = await bridgeOf(room);
    const a = await appOf(room);
    const id = (await b.next()).slice(1, 17);
    a.ws.close(1000, "bye");
    const m = await b.next();
    expect(m[0]).toBe(0x03);
    expect(hex(m.slice(1, 17))).toBe(hex(id));
  });

  it("bridge 不在のアプリは設定の時間（試験では 400ms）で切られる", async () => {
    const room = newRoom();
    const a = await appOf(room);
    expect(a.res.status).toBe(101);
    const t0 = Date.now();
    await a.waitClose(5000);
    expect(Date.now() - t0).toBeGreaterThanOrEqual(300);
  });

  it("bridge が来れば、待っていたアプリは期限で切られず、bridge に open が届く", async () => {
    const room = newRoom();
    const a = await appOf(room);
    await new Promise((r) => setTimeout(r, 100));
    const b = await bridgeOf(room);
    // bridge が来たので期限切れでは切られない
    await new Promise((r) => setTimeout(r, 700));
    expect(a.closed).toBe(null);
    const open = await b.next();
    expect(open[0]).toBe(0x01);
    b.ws.close(1000, "x");
  });

  it("アプリごとに毎秒200通を超えると切られる", async () => {
    const room = newRoom();
    const b = await bridgeOf(room);
    const a = await appOf(room);
    await b.next();
    for (let i = 0; i < 201; i++) a.ws.send(Uint8Array.of(1));
    expect(await a.waitClose()).toBe(1008);
  });

  it("アプリごとに毎秒4MBを超えると切られる", async () => {
    const room = newRoom();
    const b = await bridgeOf(room);
    const a = await appOf(room);
    await b.next();
    for (let i = 0; i < 65; i++) a.ws.send(new Uint8Array(65535));
    expect(await a.waitClose()).toBe(1008);
  });

  it("新しい bridge が古いものを置き換える", async () => {
    const room = newRoom();
    const b1 = await bridgeOf(room);
    const a1 = await appOf(room);
    await b1.next();
    const b2 = await bridgeOf(room);
    expect(b2.res.status).toBe(101);
    await b1.waitClose();
    expect(b1.closed).toBe(1000);
    await a1.waitClose();
    // 新しい bridge の下で新しいアプリがつながる
    const a2 = await appOf(room);
    const open = await b2.next();
    expect(open[0]).toBe(0x01);
    a2.ws.send(Uint8Array.of(1));
    expect((await b2.next())[0]).toBe(0x02);
    expect(b2.closed).toBe(null);
  });
});
