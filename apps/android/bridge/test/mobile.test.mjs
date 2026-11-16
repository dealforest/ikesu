import test from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes, generateKeyPairSync, sign } from 'node:crypto';
import fs from 'node:fs/promises';
import path from 'node:path';
import { startMobile, remotePairing, connect } from '../mobile-runtime.mjs';
import { addDeviceByHand, saveRelay, loadDevices, loadRelay, revokeDevice, b64u, writeJson } from '../devices.mjs';
import { pairTag, appTag } from '../relay-client.mjs';
import { sasCode, pairProof, proofMatches, verifyOperate, PAIR_TTL_SECONDS } from '../pairing.mjs';
import { FakeApp, FakeRelay, sleep, tmpDir, until } from './mobile-harness.mjs';
import { encodeMessage } from '../noise-channel.mjs';

async function setup(t, overrides = {}) {
  const configDir = await tmpDir(), stateDir = await tmpDir();
  const relayKey = randomBytes(32), room = b64u(randomBytes(32));
  const relay = await FakeRelay.start(relayKey, room);
  await saveRelay(configDir, { url: relay.url, room, relayKey });
  const calls = { answer: [], later: [], shown: [] };
  const clock = { t: Date.now() };
  let decisions = [{ id: 'd1', raw_title: '本番へ出す？', location: 'mini / ikesu', created_unix_ms: 1234, videos: [] }];
  const mobile = await startMobile({ configDir, stateDir, control: false, log: () => {}, pollMs: 20, decisions: {
    snapshot: async () => decisions, answer: async value => { calls.answer.push(value); return { ok: true }; },
    later: async id => { calls.later.push(id); }, shown: async ids => { calls.shown.push(ids); }, videoPath: () => null,
    now: () => clock.t, beatMs: 30, idleMs: 400, ...overrides } });
  t.after(async () => { await mobile.stop(); await relay.stop(); });
  await relay.bridgeUp();
  const bridgePublic = mobile.hub.noiseKey.publicKey;
  return { configDir, stateDir, relay, mobile, hub: mobile.hub, calls, clock, bridgePublic, relayInfo: mobile.relay,
    setDecisions: value => { decisions = value; } };
}

async function pairDevice(env, { permissions } = {}) {
  const pairing = env.hub.startPairing({ name: 'テストPC', permissions: permissions ?? ['notify', 'answer', 'screen', 'input'] });
  const app = new FakeApp(env.relay, 'pair', pairing.id, env.bridgePublic);
  assert.ok(await app.handshake());
  app.sendJson(app.pairMessage(pairing.secret));
  await until(() => pairing.candidates.length === 1);
  const code = app.sas(pairing.secret);
  const device = await env.hub.confirmPairing(code);
  assert.ok(device);
  const paired = await app.recv(m => m.type === 'paired');
  return { app, device, paired };
}

async function connectApp(env, paired, app) {
  const next = new FakeApp(env.relay, 'app', Buffer.from(paired.device, 'base64url'), env.bridgePublic, { noiseKey: app.noiseKey, operateKey: app.operateKey });
  return next;
}

test('ペアリング: 6桁が合うと許可リストに入り paired が届く。QR とタグが約束どおり', async t => {
  const env = await setup(t);
  const pairing = env.hub.startPairing({ name: 'テストPC', permissions: ['notify', 'answer'] });
  const qr = JSON.parse(pairing.qr());
  assert.equal(qr.v, 1); assert.equal(qr.room, env.relayInfo.room); assert.equal(qr.bridge, b64u(env.bridgePublic)); assert.equal(qr.name, 'テストPC');
  assert.equal(qr.tag, pairTag(env.relayInfo, Buffer.from(qr.pairing, 'base64url'), qr.exp));
  assert.ok(Math.abs(qr.exp - (Date.now() / 1000 + PAIR_TTL_SECONDS)) < 5);
  const app = new FakeApp(env.relay, 'pair', pairing.id, env.bridgePublic);
  assert.ok(await app.handshake());
  app.sendJson(app.pairMessage(pairing.secret, 'Pixel'));
  await until(() => pairing.candidates.length === 1);
  assert.equal(await env.hub.confirmPairing('000000') === null || app.sas(pairing.secret) === '000000', true);
  const device = await env.hub.confirmPairing(app.sas(pairing.secret));
  const paired = await app.recv(m => m.type === 'paired');
  assert.equal(paired.device, device.device);
  assert.equal(paired.tag, appTag(env.relayInfo, Buffer.from(device.device, 'base64url')));
  assert.deepEqual(paired.permissions, ['notify', 'answer']);
  const stored = await loadDevices(env.configDir);
  assert.equal(stored.length, 1); assert.equal(stored[0].name, 'Pixel'); assert.equal(stored[0].noise_pub, b64u(app.noiseKey.publicKey));
  assert.equal(env.hub.pairing, null, 'QR の秘密は捨てる');
  const mode = (await fs.stat(path.join(env.configDir, 'devices.json'))).mode & 0o777;
  assert.equal(mode, 0o600);
});

test('ペアリング: 6桁が違うと許可リストに入らない。5回外れると QR が無効になる', async t => {
  const env = await setup(t);
  const pairing = env.hub.startPairing({ name: 'PC', permissions: ['notify'] });
  const app = new FakeApp(env.relay, 'pair', pairing.id, env.bridgePublic);
  await app.handshake();
  app.sendJson(app.pairMessage(pairing.secret));
  await until(() => pairing.candidates.length === 1);
  const right = app.sas(pairing.secret);
  const wrong = right === '123456' ? '654321' : '123456';
  for (let i = 0; i < 4; i++) assert.equal(await env.hub.confirmPairing(wrong), null);
  assert.deepEqual(await loadDevices(env.configDir), []);
  assert.equal(await env.hub.confirmPairing('12ab'), null);
  assert.equal(pairing.active, false);
  assert.equal(await env.hub.confirmPairing(right), null, '無効になった後は正しい6桁でも入らない');
  assert.deepEqual(await loadDevices(env.configDir), []);
});

test('ペアリング: proof が違う接続は切られ、5回外れると QR が無効になる', async t => {
  const env = await setup(t);
  const pairing = env.hub.startPairing({ name: 'PC', permissions: ['notify'] });
  for (let i = 0; i < 5; i++) {
    const app = new FakeApp(env.relay, 'pair', pairing.id, env.bridgePublic);
    assert.ok(await app.handshake(), `${i + 1} 回目の握手`);
    app.sendJson({ ...app.pairMessage(pairing.secret), proof: pairProof(randomBytes(32), app.hash) });
    await until(() => app.conn.closed);
    assert.equal(pairing.candidates.length, 0);
  }
  assert.equal(pairing.active, false);
  const late = new FakeApp(env.relay, 'pair', pairing.id, env.bridgePublic);
  assert.equal(await late.handshake(), false, '無効な QR には握手しない');
});

test('ペアリング: 期限（5分）を過ぎると握手も確認もできない', async t => {
  const env = await setup(t);
  const pairing = env.hub.startPairing({ name: 'PC', permissions: ['notify'] });
  const app = new FakeApp(env.relay, 'pair', pairing.id, env.bridgePublic);
  await app.handshake();
  app.sendJson(app.pairMessage(pairing.secret));
  await until(() => pairing.candidates.length === 1);
  env.clock.t += (PAIR_TTL_SECONDS + 1) * 1000;
  assert.equal(await env.hub.confirmPairing(app.sas(pairing.secret)), null);
  assert.deepEqual(await loadDevices(env.configDir), []);
  const late = new FakeApp(env.relay, 'pair', pairing.id, env.bridgePublic);
  assert.equal(await late.handshake(), false);
});

test('ペアリング: 候補は4本まで', async t => {
  const env = await setup(t);
  const pairing = env.hub.startPairing({ name: 'PC', permissions: ['notify'] });
  for (let i = 0; i < 4; i++) {
    const app = new FakeApp(env.relay, 'pair', pairing.id, env.bridgePublic);
    assert.ok(await app.handshake());
    app.sendJson(app.pairMessage(pairing.secret));
    await until(() => pairing.candidates.length === i + 1);
  }
  assert.equal(await new FakeApp(env.relay, 'pair', pairing.id, env.bridgePublic).handshake(), false);
});

test('登録済みの端末は握手でき、notice（id・title・origin・created_unix_ms・notify だけ）が届く', async t => {
  const env = await setup(t);
  const { app, paired } = await pairDevice(env);
  const again = await connectApp(env, paired, app);
  assert.ok(await again.handshake());
  const notice = await again.recv(m => m.type === 'notice');
  assert.deepEqual(notice, { type: 'notice', decisions: [{ id: 'd1', title: '本番へ出す？', origin: 'mini / ikesu', created_unix_ms: 1234, notify: true }] });
  // PC で見ている・「あとで」にした判断待ちは、鳴らさない印のまま届く。
  env.setDecisions([{ id: 'd2', raw_title: '見ている', location: '', created_unix_ms: 1, notify: false, videos: [] }]);
  env.hub.publish();
  assert.equal((await again.recv(m => m.type === 'notice' && m.decisions[0]?.id === 'd2')).decisions[0].notify, false);
  env.setDecisions([]);
  env.hub.publish();
  assert.deepEqual((await again.recv(m => m.type === 'notice' && m.decisions.length === 0)).decisions, []);
  again.sendJson({ type: 'shown', ids: ['d1'] });
  await until(() => env.calls.shown.length === 1);
  assert.deepEqual(env.calls.shown, [['d1']]);
});

test('許可リストに無い鍵・知らない device は、返事なしで切られる', async t => {
  const env = await setup(t);
  const { app, paired } = await pairDevice(env);
  const stranger = new FakeApp(env.relay, 'app', Buffer.from(paired.device, 'base64url'), env.bridgePublic);
  assert.equal(await stranger.handshake(), false);
  assert.equal(stranger.conn.inbox.length, 0);
  const unknown = new FakeApp(env.relay, 'app', randomBytes(16), env.bridgePublic, { noiseKey: app.noiseKey });
  assert.equal(await unknown.handshake(), false);
});

test('改ざんした握手の1通目・暗号文の1通で切られる', async t => {
  const env = await setup(t);
  const { app, paired } = await pairDevice(env);
  const bad = await connectApp(env, paired, app);
  assert.equal(await bad.handshake({ tamperFirst: true }), false);
  const good = await connectApp(env, paired, app);
  assert.ok(await good.handshake());
  await good.recv(m => m.type === 'notice');
  const forged = Buffer.from(good.tx.encrypt(Buffer.concat([Buffer.from([0]), Buffer.from('{"type":"shown","ids":[]}')])));
  forged[forged.length - 1] ^= 1;
  good.conn.send(forged);
  await until(() => good.conn.closed);
  assert.equal(env.calls.shown.length, 0);
});

test('operate の前の answer / later は operate required で拒否される', async t => {
  const env = await setup(t);
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson({ type: 'answer', id: 'd1', options: ['yes'], text: '', images: [], sequence: 's1' });
  assert.deepEqual(await a.recv(m => m.type === 'error'), { type: 'error', sequence: 's1', message: 'operate required' });
  a.sendJson({ type: 'later', id: 'd1' });
  assert.equal((await a.recv(m => m.type === 'error')).message, 'operate required');
  assert.equal(env.calls.answer.length + env.calls.later.length, 0);
});

test('operate: 正しい署名で operate_ok・snapshot、answer と later が通る。違う署名は拒否して切る', async t => {
  const env = await setup(t, { idleMs: 60000 });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  const ok = await a.recv(m => m.type === 'operate_ok');
  assert.ok(ok.until > Date.now());
  assert.equal((await a.recv(m => m.type === 'snapshot')).decisions[0].id, 'd1');
  a.sendJson({ type: 'answer', id: 'd1', options: ['yes'], text: '', images: [], sequence: 's2' });
  assert.deepEqual(await a.recv(m => m.type === 'answer_result'), { type: 'answer_result', sequence: 's2', ok: true });
  assert.equal(env.calls.answer[0].id, 'd1');
  a.sendJson({ type: 'later', id: 'd1' });
  await until(() => env.calls.later.length === 1);
  a.sendJson({ type: 'answer', id: 'd1', bogus: 1, sequence: 's3' });
  assert.equal((await a.recv(m => m.type === 'error')).sequence, 's3');
  // 別の鍵の署名
  const b = await connectApp(env, paired, app);
  await b.handshake();
  const other = generateKeyPairSync('ec', { namedCurve: 'prime256v1' });
  b.sendJson({ ...b.operateMessage(), sig: b64u(sign('sha256', Buffer.concat([Buffer.from('ikesu-operate/1'), Buffer.from([1]), b.hash]), { key: other.privateKey, dsaEncoding: 'der' })) });
  assert.equal((await b.recv(m => m.type === 'error')).message, 'operate rejected');
  await until(() => b.conn.closed);
  // 別の握手の h に対する署名の使い回し
  const c = await connectApp(env, paired, app);
  await c.handshake();
  c.sendJson(a.operateMessage());
  assert.equal((await c.recv(m => m.type === 'error')).message, 'operate rejected');
});

test('operate の hashes: 複数の PC の h を1つの署名に入れられる。自分の h が無い・0個・9個・32バイトでないものは拒否', async t => {
  const env = await setup(t, { idleMs: 60000 });
  const { app, paired } = await pairDevice(env);
  const others = n => Array.from({ length: n }, () => randomBytes(64));
  const open = async () => { const x = await connectApp(env, paired, app); await x.handshake(); return x; };
  const good = await open();
  good.sendJson(good.operateMessage(others(2)));
  await good.recv(m => m.type === 'operate_ok');
  for (const make of [
    x => x.operateMessage(others(2), { includeOwn: false }),
    x => ({ ...x.operateMessage(), hashes: [] }),
    x => x.operateMessage(others(8)),
    x => ({ ...x.operateMessage(), hashes: [b64u(randomBytes(32))] }),
    x => ({ ...x.operateMessage(), hashes: [x.operateMessage().hashes[0], x.operateMessage().hashes[0]] }),
  ]) {
    const x = await open();
    x.sendJson(make(x));
    assert.equal((await x.recv(m => m.type === 'error')).message, 'operate rejected');
    await until(() => x.conn.closed);
  }
});

test('operate は無操作で終わり operate_expired が届く。beat が5秒ごと（試験では短縮）に届き、operate_end でも終わる', async t => {
  const env = await setup(t, { idleMs: 300, beatMs: 30 });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  const first = await a.recv(m => m.type === 'beat');
  const second = await a.recv(m => m.type === 'beat' && m.n > first.n);
  assert.equal(second.n, first.n + 1);
  await a.recv(m => m.type === 'operate_expired');
  // 終わった接続は閉じる。同じ署名を送り直して開き直すことはできない。
  await until(() => a.conn.closed);
  const b = await connectApp(env, paired, app);
  assert.ok(await b.handshake());
  b.sendJson({ type: 'later', id: 'd1' });
  assert.equal((await b.recv(m => m.type === 'error')).message, 'operate required');
  b.sendJson(b.operateMessage());
  await b.recv(m => m.type === 'operate_ok');
  b.sendJson({ type: 'operate_end' });
  await b.recv(m => m.type === 'operate_expired');
  await until(() => b.conn.closed);
});

test('外した端末が何度つないでも、ほかの登録済みの端末は入れる', async t => {
  const env = await setup(t);
  const { app, paired } = await pairDevice(env);
  const removed = randomBytes(16);
  for (let i = 0; i < 12; i++) assert.equal(await new FakeApp(env.relay, 'app', removed, env.bridgePublic, { noiseKey: app.noiseKey }).handshake(), false);
  const again = await connectApp(env, paired, app);
  assert.ok(await again.handshake());
});

test('顔・指紋で開く前の分割メッセージは、溜めずに接続を切る', async t => {
  const env = await setup(t);
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.conn.send(Buffer.from(a.tx.encrypt(Buffer.concat([Buffer.from([1, 0, 0, 0, 7, 0]), Buffer.alloc(59994)]))));
  await until(() => a.conn.closed);
});

test('beat を付けた answer: 2つ以上古いものは実行せず stale beat を返す。新しいものと beat なしは通る', async t => {
  const env = await setup(t, { idleMs: 60000, beatMs: 20 });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  const latest = (await a.recv(m => m.type === 'beat' && m.n >= 5)).n;
  a.sendJson({ type: 'answer', id: 'd1', options: ['yes'], text: '', images: [], sequence: 'old', beat: 0 });
  assert.deepEqual(await a.recv(m => m.sequence === 'old'), { type: 'error', sequence: 'old', message: 'stale beat' });
  assert.equal(env.calls.answer.length, 0);
  a.sendJson({ type: 'answer', id: 'd1', options: ['yes'], text: '', images: [], sequence: 'new', beat: latest });
  await a.recv(m => m.type === 'answer_result' && m.sequence === 'new');
  assert.equal(env.calls.answer.length, 1);
  assert.equal('beat' in env.calls.answer[0], false);
});

test('answer の権限が無い端末は operate しても answer できない', async t => {
  const env = await setup(t);
  const { app, paired } = await pairDevice(env, { permissions: ['notify', 'screen'] });
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  a.sendJson({ type: 'later', id: 'd1' });
  assert.equal((await a.recv(m => m.type === 'error')).message, 'permission denied');
  assert.equal(env.calls.later.length, 0);
});

test('revoke で再起動なしに接続が切れ、再接続も返事なしで切られる', async t => {
  const env = await setup(t);
  const { app, paired, device } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  await a.recv(m => m.type === 'notice');
  assert.equal(await revokeDevice(env.configDir, device.device), true);
  await until(() => a.conn.closed, 3000);
  assert.deepEqual(await loadDevices(env.configDir), []);
  const again = await connectApp(env, paired, app);
  assert.equal(await again.handshake(), false);
});

test('60,000 バイトを超える snapshot が分割されて組み立てられる', async t => {
  const env = await setup(t, { idleMs: 60000 });
  const big = 'あ'.repeat(70000);
  env.setDecisions([{ id: 'big', raw_title: 't', raw_body: big, location: '', videos: [] }]);
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  const snapshot = await a.recv(m => m.type === 'snapshot');
  assert.equal(snapshot.decisions[0].raw_body, big);
  // 送る側の分割も組み立てられる
  a.sendJson({ type: 'answer', id: 'big', options: [], text: 'い'.repeat(70000), images: [], sequence: 'long' });
  await until(() => env.calls.answer.length === 1);
  assert.equal(env.calls.answer[0].text.length, 70000);
});

test('握手の失敗が毎分10回を超えると、それ以降の open は握手せず閉じる', async t => {
  const env = await setup(t);
  const { app, paired } = await pairDevice(env);
  for (let i = 0; i < 11; i++) {
    const bad = new FakeApp(env.relay, 'app', Buffer.from(paired.device, 'base64url'), env.bridgePublic);
    await bad.handshake({ tamperFirst: true });
  }
  const good = await connectApp(env, paired, app);
  assert.equal(await good.handshake(), false);
  env.clock.t += 61_000;
  const later = await connectApp(env, paired, app);
  assert.ok(await later.handshake());
});

test('relay.json があるときだけつなぐ。中継の接続に bridge のタグが付く', async t => {
  const configDir = await tmpDir();
  assert.equal(await startMobile({ configDir, stateDir: configDir, log: () => {}, decisions: {} }), null);
  const env = await setup(t);
  assert.match(env.relay.bridgeAuth[0], /^IKESU-RELAY [0-9a-f]{64}$/);
});

test('中継が落ちて戻ると bridge がつなぎ直す', async t => {
  const env = await setup(t);
  env.mobile.client.minDelay = 20; env.mobile.client.delay = 20;
  const first = env.relay.bridge;
  first.terminate();
  await until(() => env.relay.bridge !== first && env.relay.bridge?.readyState === 1, 5000);
});

test('devices.json と relay.json は壊れていたら失敗にする。権限が緩いと失敗にする', async t => {
  const configDir = await tmpDir();
  await fs.writeFile(path.join(configDir, 'devices.json'), '{oops', { mode: 0o600 });
  await assert.rejects(loadDevices(configDir), /壊れています/);
  await fs.writeFile(path.join(configDir, 'devices.json'), '{"devices":[]}', { mode: 0o644 });
  await fs.chmod(path.join(configDir, 'devices.json'), 0o644);
  await assert.rejects(loadDevices(configDir), /権限/);
  await writeJson(path.join(configDir, 'devices.json'), { devices: [{ device: 'x' }] });
  await assert.rejects(loadDevices(configDir), /不正/);
  await fs.writeFile(path.join(configDir, 'relay.json'), '{"url":"http://x"}', { mode: 0o600 });
  await assert.rejects(loadRelay(configDir), /不正/);
});

test('proof・6桁・署名の部品: 約束どおりの値になる', () => {
  const secret = Buffer.alloc(32, 7), h = Buffer.alloc(32, 9);
  assert.equal(proofMatches(secret, h, pairProof(secret, h)), true);
  assert.equal(proofMatches(secret, h, pairProof(secret, Buffer.alloc(32, 1))), false);
  assert.equal(proofMatches(secret, h, 5), false);
  assert.match(sasCode(secret, h), /^\d{6}$/);
  assert.equal(verifyOperate('AAAA', h, 'AAAA'), false);
});

test('devices add: 手で登録した鍵のにせのアプリが握手して notice を受け、operate も通る。形の違う鍵は拒否', async t => {
  const env = await setup(t, { idleMs: 60000 });
  const app = new FakeApp(env.relay, 'app', Buffer.alloc(16), env.bridgePublic);
  const { link, device } = await addDeviceByHand(env.configDir, { name: '手で登録', noise: b64u(app.noiseKey.publicKey), operate: app.operatePub, pcName: 'テストPC' });
  const info = JSON.parse(link);
  assert.deepEqual(Object.keys(info), ['v', 'kind', 'relay', 'room', 'bridge', 'device', 'tag', 'name']);
  assert.equal(info.kind, 'ikesu-link'); assert.equal(info.relay, env.relayInfo.url); assert.equal(info.bridge, b64u(env.bridgePublic));
  assert.equal(info.tag, appTag(env.relayInfo, Buffer.from(device.device, 'base64url')));
  assert.ok(!link.includes(b64u(env.relayInfo.relayKey)));
  await until(() => env.hub.devices.some(item => item.device === info.device));
  const a = new FakeApp(env.relay, 'app', Buffer.from(info.device, 'base64url'), env.bridgePublic, { noiseKey: app.noiseKey, operateKey: app.operateKey });
  assert.ok(await a.handshake());
  await a.recv(m => m.type === 'notice');
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  const bad = [{ noise: b64u(randomBytes(31)), operate: app.operatePub }, { noise: b64u(app.noiseKey.publicKey), operate: b64u(randomBytes(40)) },
    { noise: b64u(app.noiseKey.publicKey), operate: b64u(generateKeyPairSync('ec', { namedCurve: 'secp256k1' }).publicKey.export({ type: 'spki', format: 'der' })) }];
  for (const keys of bad) await assert.rejects(addDeviceByHand(env.configDir, { name: 'x', ...keys }), /不正|ではありません/);
  assert.equal((await loadDevices(env.configDir)).length, 1);
});

test('run 中の bridge へ手元の unix socket 経由で pair を頼める', async t => {
  const env = await setup(t);
  const dir = await tmpDir();
  const control = await startMobile({ configDir: env.configDir, stateDir: dir, control: true, log: () => {}, pollMs: 20, decisions: { snapshot: async () => [], answer: async () => ({}), later: async () => {} } });
  t.after(() => control.stop());
  const remote = await remotePairing(dir);
  assert.ok(remote);
  const qr = JSON.parse(await remote.start({ name: 'PC', permissions: ['notify'] }));
  assert.equal(qr.room, env.relayInfo.room);
  assert.deepEqual(await remote.confirm('000000'), { device: null, active: true });
  remote.close();
});

test('操作中に同じ operate を送り直しても、期限は延びない', async t => {
  const env = await setup(t, { idleMs: 400, beatMs: 1000 });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  const message = a.operateMessage();
  a.sendJson(message);
  const first = await a.recv(m => m.type === 'operate_ok');
  await new Promise(r => setTimeout(r, 150));
  a.sendJson(message);
  assert.equal((await a.recv(m => m.type === 'operate_ok')).until, first.until);
  await a.recv(m => m.type === 'operate_expired', 1000);
});

test('clientCount はペアリング中と握手前の接続を数えず、握手の済んだアプリだけを数える', async t => {
  const env = await setup(t);
  const { app, paired } = await pairDevice(env);
  assert.equal(env.hub.clientCount(), 0);
  const a = await connectApp(env, paired, app);
  assert.ok(await a.handshake());
  await until(() => env.hub.clientCount() === 1);
  a.conn.close?.();
  env.hub.onReset();
  assert.equal(env.hub.clientCount(), 0);
});

// にせの画面: 書かれたキーと大きさを覚え、出力を流せる。
function fakeScreens() {
  const screens = [];
  return { screens, serverAlive: async () => true, openScreen: options => {
    const screen = { ...options, written: [], sizes: [[options.cols, options.rows]], closed: false,
      write(bytes) { this.written.push(Buffer.from(bytes)); }, resize(c, r) { this.sizes.push([c, r]); }, close() { this.closed = true; } };
    screens.push(screen); return screen;
  } };
}

test('画面: 顔・指紋の前は開けない。開いたら出力が届き、新しい心拍のキーだけが書かれる。閉じると止まる', async t => {
  const fake = fakeScreens();
  const env = await setup(t, { idleMs: 60000, beatMs: 20, serverAlive: fake.serverAlive, openScreen: fake.openScreen });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson({ type: 'screen_open', cols: 40, rows: 20 });
  assert.equal((await a.recv(m => m.type === 'error')).message, 'operate required');
  assert.equal(fake.screens.length, 0);
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  a.sendJson({ type: 'screen_open', cols: 40, rows: 20 });
  const opened = await a.recv(m => m.type === 'screen_opened');
  assert.deepEqual([opened.cols, opened.rows], [40, 20]);
  const screen = fake.screens[0];
  screen.onData(0, Buffer.from('hello'));
  const data = await a.recv(m => m.type === 'screen_data');
  assert.equal(Buffer.from(data.data, 'base64').toString(), 'hello');
  const beat = (await a.recv(m => m.type === 'beat' && m.n >= 4)).n;
  a.sendJson({ type: 'screen_input', screen: opened.screen, beat: 0, data: Buffer.from('old').toString('base64') });
  assert.equal((await a.recv(m => m.type === 'error')).message, 'stale beat');
  a.sendJson({ type: 'screen_input', screen: opened.screen, data: Buffer.from('nobeat').toString('base64') });
  await a.recv(m => m.type === 'error' && m.message === 'stale beat');
  a.sendJson({ type: 'screen_input', screen: opened.screen, beat, data: Buffer.from('ls\r').toString('base64') });
  await until(() => screen.written.length === 1);
  assert.equal(screen.written[0].toString(), 'ls\r');
  a.sendJson({ type: 'screen_resize', screen: opened.screen, cols: 50, rows: 25 });
  await until(() => screen.sizes.length === 2);
  assert.deepEqual(screen.sizes[1], [50, 25]);
  a.sendJson({ type: 'screen_close', screen: opened.screen });
  await a.recv(m => m.type === 'screen_closed');
  assert.ok(screen.closed);
});

test('画面: screen の権限が無い端末は開けず、input の権限が無い端末は打てない。操作の終わりで画面も閉じる', async t => {
  const fake = fakeScreens();
  const env = await setup(t, { idleMs: 60000, beatMs: 20, serverAlive: fake.serverAlive, openScreen: fake.openScreen });
  const viewOnly = await pairDevice(env, { permissions: ['notify', 'answer', 'screen'] });
  const a = await connectApp(env, viewOnly.paired, viewOnly.app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  a.sendJson({ type: 'screen_open', cols: 40, rows: 20 });
  const opened = await a.recv(m => m.type === 'screen_opened');
  const beat = (await a.recv(m => m.type === 'beat')).n;
  a.sendJson({ type: 'screen_input', screen: opened.screen, beat, data: Buffer.from('x').toString('base64') });
  assert.equal((await a.recv(m => m.type === 'error')).message, 'permission denied');
  assert.equal(fake.screens[0].written.length, 0);
  a.sendJson({ type: 'operate_end' });
  await a.recv(m => m.type === 'screen_closed');
  assert.ok(fake.screens[0].closed);

  const noScreen = await pairDevice(env, { permissions: ['notify', 'answer'] });
  const b = await connectApp(env, noScreen.paired, noScreen.app);
  await b.handshake();
  b.sendJson(b.operateMessage());
  await b.recv(m => m.type === 'operate_ok');
  b.sendJson({ type: 'screen_open', cols: 40, rows: 20 });
  assert.equal((await b.recv(m => m.type === 'error')).message, 'permission denied');
  assert.equal(fake.screens.length, 1);
});

test('画面: 大きさが範囲の外なら接続を切る。サーバーが無ければ開かない', async t => {
  const fake = fakeScreens();
  const env = await setup(t, { idleMs: 60000, beatMs: 1000, serverAlive: async () => false, openScreen: fake.openScreen });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  a.sendJson({ type: 'screen_open', cols: 40, rows: 20 });
  assert.match((await a.recv(m => m.type === 'error')).message, /サーバー/);
  assert.equal(fake.screens.length, 0);
  a.sendJson({ type: 'screen_open', cols: 5000, rows: 20 });
  await until(() => a.conn.closed);
});

test('画面: スマホのファイルを保存し、そのパスを画面に打つ。input の権限と新しい心拍が要る', async t => {
  const fake = fakeScreens();
  const saved = [];
  // 心拍は 100ms ごと。送るまでに古くならない間隔にする。
  const env = await setup(t, { idleMs: 60000, beatMs: 100, serverAlive: fake.serverAlive, openScreen: fake.openScreen,
    saveFile: (name, bytes) => { saved.push([name, bytes.toString()]); return `/tmp/x/${name}`; } });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  a.sendJson({ type: 'screen_open', cols: 40, rows: 20 });
  const opened = await a.recv(m => m.type === 'screen_opened');
  const beat = (await a.recv(m => m.type === 'beat' && m.n >= 3)).n;
  a.sendJson({ type: 'screen_file', screen: opened.screen, beat: 0, name: 'a.png', data: Buffer.from('img').toString('base64') });
  assert.equal((await a.recv(m => m.type === 'error')).message, 'stale beat');
  assert.equal(saved.length, 0);
  a.sendJson({ type: 'screen_file', screen: opened.screen, beat, name: 'a.png', data: Buffer.from('img').toString('base64') });
  const done = await a.recv(m => m.type === 'screen_file_saved');
  assert.equal(done.path, '/tmp/x/a.png');
  assert.deepEqual(saved, [['a.png', 'img']]);
  assert.equal(fake.screens[0].written.map(b => b.toString()).join(''), "\x1b[200~'/tmp/x/a.png' \x1b[201~");
});

test('画面: screen_open が続けて届いても、開いたままの PTY は1つだけ。切断ですべて閉じる', async t => {
  const fake = fakeScreens();
  const env = await setup(t, { idleMs: 60000, beatMs: 1000, serverAlive: () => new Promise(r => setTimeout(() => r(true), 30)), openScreen: fake.openScreen });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  a.sendJson({ type: 'screen_open', cols: 40, rows: 20 });
  a.sendJson({ type: 'screen_open', cols: 41, rows: 20 });
  await a.recv(m => m.type === 'screen_opened');
  await sleep(100);
  assert.equal(fake.screens.filter(screen => !screen.closed).length, 1);
  a.conn.close?.();
  env.hub.onReset();
  assert.equal(fake.screens.filter(screen => !screen.closed).length, 0);
});

test('心拍: まだ送っていない番号は受けない', async t => {
  const fake = fakeScreens();
  const env = await setup(t, { idleMs: 60000, beatMs: 1000, serverAlive: fake.serverAlive, openScreen: fake.openScreen });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  a.sendJson({ type: 'screen_open', cols: 40, rows: 20 });
  const opened = await a.recv(m => m.type === 'screen_opened');
  a.sendJson({ type: 'screen_input', screen: opened.screen, beat: 999999, data: Buffer.from('x').toString('base64') });
  assert.equal((await a.recv(m => m.type === 'error')).message, 'stale beat');
  assert.equal(fake.screens[0].written.length, 0);
});

test('画面: 届き終えるまでに心拍が進む大きなファイルも、送り始めの心拍で受ける', async t => {
  const fake = fakeScreens();
  const saved = [];
  const env = await setup(t, { idleMs: 60000, beatMs: 50, serverAlive: fake.serverAlive, openScreen: fake.openScreen,
    saveFile: (name, bytes) => { saved.push(bytes.length); return `/tmp/x/${name}`; } });
  const { app, paired } = await pairDevice(env);
  const a = await connectApp(env, paired, app);
  await a.handshake();
  a.sendJson(a.operateMessage());
  await a.recv(m => m.type === 'operate_ok');
  a.sendJson({ type: 'screen_open', cols: 40, rows: 20 });
  const opened = await a.recv(m => m.type === 'screen_opened');
  const beat = (await a.recv(m => m.type === 'beat')).n;
  const parts = encodeMessage({ type: 'screen_file', screen: opened.screen, beat, name: 'big.bin', data: Buffer.alloc(200 * 1024, 7).toString('base64') }, () => 9);
  assert.ok(parts.length > 2);
  a.conn.send(Buffer.from(a.tx.encrypt(parts[0])));
  await a.recv(m => m.type === 'beat' && m.n >= beat + 3);
  for (const part of parts.slice(1)) a.conn.send(Buffer.from(a.tx.encrypt(part)));
  assert.equal((await a.recv(m => m.type === 'screen_file_saved' || m.type === 'error')).type, 'screen_file_saved');
  assert.deepEqual(saved, [200 * 1024]);
  assert.equal(fake.screens[0].written.map(b => b.toString()).join(''), "\x1b[200~'/tmp/x/big.bin' \x1b[201~");
});
