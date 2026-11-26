import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { once } from 'node:events';
import fs from 'node:fs/promises';
import net from 'node:net';
import path from 'node:path';
import { createInterface } from 'node:readline';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import pty from 'node-pty';
import { addDevice, b64u, loadDevices, loadNoiseKey, saveRelay } from '../devices.mjs';
import { startMobile } from '../mobile-runtime.mjs';
import { Pairing } from '../pairing.mjs';
import { FakeApp, FakeRelay, allowFor, allowHeader, startDisconnectingPairControl, until } from './mobile-harness.mjs';

const exec = promisify(execFile);
const cwd = fileURLToPath(new URL('../', import.meta.url));
const args = ['./main.mjs', 'pair', '--name', 'pair-test'];

async function fixture(t) {
  const home = await fs.mkdtemp('/tmp/ikesu-pair-');
  const configDir = path.join(home, '.config/ikesu/plugins/config/kazuph.android');
  const stateDir = path.join(home, '.local/state/ikesu/plugins/kazuph.android');
  const lockPath = path.join(stateDir, 'pair.lock');
  const env = {
    ...process.env, HOME: home, PATH: `${path.dirname(process.execPath)}:${process.env.PATH}`,
    IKESU_PLUGIN_CONFIG_DIR: configDir, IKESU_PLUGIN_STATE_DIR: stateDir,
  };
  const relayKey = randomBytes(32), room = b64u(randomBytes(32));
  const relay = await FakeRelay.start(relayKey, room);
  const terminals = [];
  const logs = [];
  let daemon;
  t.after(async () => {
    for (const term of terminals) term.kill();
    await Promise.all(terminals.map(term => term.result));
    await daemon?.stop();
    await relay.stop();
    await fs.rm(home, { recursive: true, force: true });
  });
  await saveRelay(configDir, { url: relay.url, room, relayKey });
  return {
    configDir, stateDir, lockPath, relay, logs,
    async startDaemon(decisions = {}) {
      daemon = await startMobile({ configDir, stateDir, log: message => logs.push(message), decisions: {
        snapshot: async () => [], answer: async () => ({}), later: async () => {}, ...decisions,
      } });
      await relay.bridgeUp();
      return daemon.hub;
    },
    open(cols = 160, rows = 60, nodeOptions = []) {
      const terminal = pty.spawn(process.execPath, [...nodeOptions, ...args], { cwd, env, cols, rows });
      let output = '', exited = false;
      terminal.onData(data => { output += data; });
      const result = new Promise(resolve => terminal.onExit(event => { exited = true; resolve(event); }));
      const term = {
        terminal, result,
        get output() { return output; },
        get exited() { return exited; },
        kill(signal) { if (!exited) terminal.kill(signal); },
      };
      terminals.push(term);
      return term;
    },
    manual() { return exec('/bin/sh', ['./pair.command'], { cwd, env }); },
    async readLock() { return JSON.parse(await fs.readFile(lockPath, 'utf8')); },
  };
}

test('一時 bridge は手動起動と排他し、強制終了で残ったロックを回収する', async t => {
  const f = await fixture(t);
  const first = f.open();
  await until(() => first.output.includes('6桁:'));
  assert.doesNotMatch(first.output, /画面が小さい/);
  const owner = await f.readLock();
  assert.equal(owner.pid, first.terminal.pid);
  assert.equal((await fs.stat(f.lockPath)).mode & 0o777, 0o600);
  await assert.rejects(f.manual(), error => error.code === 1 &&
    error.stderr.includes(`既にペアリングが実行中です（PID ${owner.pid}）`));
  assert.deepEqual(await f.readLock(), owner);
  assert.equal(first.exited, false);

  first.kill('SIGKILL');
  await first.result;
  assert.deepEqual(await f.readLock(), owner);
  const contenders = [f.open(), f.open()];
  await until(() => contenders.some(term => term.output.includes('6桁:')) && contenders.some(term => term.exited));
  const entered = contenders.find(term => !term.exited);
  const rejected = contenders.find(term => term.exited);
  assert.equal((await rejected.result).exitCode, 1);
  assert.equal((await f.readLock()).pid, entered.terminal.pid);
  entered.kill('SIGTERM');
  assert.equal((await entered.result).exitCode, 143);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

test('常駐 bridge の QR を後発が置き換えず、6桁で登録後にロックを解放する', async t => {
  const f = await fixture(t);
  const hub = await f.startDaemon();
  const first = f.open();
  await until(() => first.output.includes('6桁:'));
  const pairing = hub.pairing;
  assert.ok(pairing?.active);
  const second = f.open();
  assert.equal((await second.result).exitCode, 1);
  assert.match(second.output, /既にペアリングが実行中です/);
  assert.equal(hub.pairing, pairing);

  const app = new FakeApp(f.relay, 'pair', pairing.id, hub.noiseKey.publicKey);
  assert.ok(await app.handshake());
  app.sendJson(app.pairMessage(pairing.secret, 'pair-lock test device'));
  await until(() => pairing.candidates.length === 1);
  first.terminal.write(`${app.sas(pairing.secret)}\r`);
  assert.equal((await first.result).exitCode, 0);
  assert.match(first.output, /登録しました/);
  assert.equal((await loadDevices(f.configDir)).length, 1);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });

  const third = f.open();
  await until(() => third.output.includes('6桁:'));
  third.terminal.write('\x03');
  assert.equal((await third.result).exitCode, 130);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

test('小さい PTY では待機し、必要寸法で QR の全行と入力欄を描画する', async t => {
  const f = await fixture(t);
  const term = f.open(80, 24);
  const size = await until(() => term.output.match(/現在 80×24 \/ 必要 (\d+)×(\d+)/));
  assert.doesNotMatch(term.output, /6桁:|[█▀▄]{10}/);
  const cols = Number(size[1]), rows = Number(size[2]);
  term.terminal.resize(cols, rows);
  await until(() => term.output.includes('6桁:'));
  const marker = '\x1b[2J\x1b[H';
  assert.ok(term.output.includes(marker));
  const qr = term.output.split(marker).at(-1).split(/\r?\n/).slice(0, rows - 4);
  assert.equal(qr.length, rows - 4);
  assert.equal(qr[0], '▄'.repeat(cols - 2));
  for (const line of qr.slice(1)) {
    assert.equal(line.length, cols - 2);
    assert.match(line, /^█[█▀▄ ]+█$/);
  }
  term.kill('SIGINT');
  assert.equal((await term.result).exitCode, 130);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

test('サイズ変更待ちの Esc はロックを解放する', async t => {
  const f = await fixture(t);
  const term = f.open(80, 24);
  await until(() => term.output.includes('中止: Esc / Ctrl-C'));
  term.terminal.write('\x1b');
  assert.equal((await term.result).exitCode, 130);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

test('B8 確定後の読込に失敗すると paired を送らず、PC の pair は失敗し、追加した端末が残らない', async t => {
  const f = await fixture(t);
  let failedDevice;
  const hub = await f.startDaemon({
    async loadDevices(dir) {
      const devices = await loadDevices(dir);
      if (!devices.length || failedDevice) return devices;
      const file = path.join(dir, 'devices.json');
      failedDevice = devices[0].device;
      // 一度だけ実際の読込エラーを起こし、後続の revoke には正しい権限を戻す。
      await fs.chmod(file, 0o644);
      try { return await loadDevices(dir); }
      finally { await fs.chmod(file, 0o600); }
    },
  });
  const term = f.open();
  await until(() => term.output.includes('6桁:'));
  const pairing = hub.pairing;
  const app = new FakeApp(f.relay, 'pair', pairing.id, hub.noiseKey.publicKey);
  assert.ok(await app.handshake());
  app.sendJson(app.pairMessage(pairing.secret));
  await until(() => pairing.candidates.length === 1);
  f.relay.frames = [];

  term.terminal.write(`${app.sas(pairing.secret)}\r`);
  assert.equal((await term.result).exitCode, 1);
  assert.match(term.output, /登録に失敗しました: 登録を反映できませんでした/);
  assert.doesNotMatch(term.output, /登録しました/);
  assert.ok(failedDevice);
  assert.deepEqual(await loadDevices(f.configDir), []);
  assert.deepEqual(hub.devices, []);
  await until(() => app.conn.closed);
  assert.ok(!f.relay.frames.some(frame => frame[0] === 0x02 && frame.subarray(1, 17).equals(app.conn.key)));
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

test('B16 paired を送れないと PC の pair は失敗し、端末登録を取り消す', async t => {
  const f = await fixture(t);
  const existingApp = new FakeApp(f.relay, 'app', randomBytes(16));
  const existing = {
    device: b64u(existingApp.id16), noise_pub: b64u(existingApp.noiseKey.publicKey), operate_pub: existingApp.operatePub,
    name: '登録済み端末', permissions: ['notify'], created: new Date().toISOString(),
  };
  await addDevice(f.configDir, existing);
  const hub = await f.startDaemon();
  const term = f.open();
  await until(() => term.output.includes('6桁:'));
  const pairing = hub.pairing;
  const app = new FakeApp(f.relay, 'pair', pairing.id, hub.noiseKey.publicKey);
  assert.ok(await app.handshake());
  app.sendJson(app.pairMessage(pairing.secret));
  await until(() => pairing.candidates.length === 1);
  const sendData = hub.transport.sendData.bind(hub.transport);
  const pairKey = app.conn.key.toString('hex');
  hub.transport.sendData = (key, bytes) => key === pairKey ? false : sendData(key, bytes);
  f.relay.frames = [];

  term.terminal.write(`${app.sas(pairing.secret)}\r`);
  await until(() => term.exited);
  assert.equal((await term.result).exitCode, 1);
  assert.match(term.output, /登録に失敗しました: 登録の知らせをスマホへ送れませんでした/);
  assert.doesNotMatch(term.output, /登録しました/);
  assert.deepEqual(await loadDevices(f.configDir), [existing]);
  assert.deepEqual(hub.devices, [existing]);
  await until(() => app.conn.closed);
  const expected = allowFor(existing);
  await until(() => f.relay.frames.findLast(frame => frame[0] === 0x04)?.equals(expected));
  assert.ok(f.relay.frames.some(frame => frame[0] === 0x04 && frame.length === expected.length + 16 &&
    frame.subarray(0, expected.length).equals(expected)));
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

test('B17 端末登録の取り消しにも失敗すると PC の pair が端末 ID と外し方を表示する', async t => {
  const f = await fixture(t);
  const file = path.join(f.configDir, 'devices.json');
  let failedDevice;
  const hub = await f.startDaemon({
    async loadDevices(dir) {
      if (failedDevice) return loadDevices(dir);
      const devices = await loadDevices(dir);
      if (!devices.length) return devices;
      await fs.chmod(file, 0o000);
      failedDevice = devices[0].device;
      return loadDevices(dir);
    },
  });
  const term = f.open();
  await until(() => term.output.includes('6桁:'));
  const pairing = hub.pairing;
  const app = new FakeApp(f.relay, 'pair', pairing.id, hub.noiseKey.publicKey);
  assert.ok(await app.handshake());
  app.sendJson(app.pairMessage(pairing.secret));
  await until(() => pairing.candidates.length === 1);
  f.relay.frames = [];

  try {
    term.terminal.write(`${app.sas(pairing.secret)}\r`);
    await until(() => term.exited);
    assert.equal((await term.result).exitCode, 1);
    assert.ok(failedDevice);
    assert.ok(term.output.includes(`取り消しにも失敗したため、端末 ID ${failedDevice} が devices.json に残っているかもしれません`));
    assert.ok(term.output.includes(`main.mjs revoke ${failedDevice}`));
    assert.match(term.output, /EACCES/);
    const rollback = f.logs.find(message => message.includes(`端末 ID ${failedDevice}`) &&
      message.includes(`main.mjs revoke ${failedDevice}`));
    assert.ok(rollback, '常駐ログに端末 ID と解除コマンドが必要');
    assert.ok(term.output.includes(`登録に失敗しました: ${rollback}`));
    await until(() => f.relay.frames.some(frame => frame.equals(allowHeader)));
    await until(() => app.conn.closed);
    await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
  } finally {
    if (failedDevice) await fs.chmod(file, 0o600);
  }
  assert.deepEqual((await loadDevices(f.configDir)).map(device => device.device), [failedDevice]);
});

test('B19 確定中に制御ソケットが切れると PC の pair は失敗し、ロックを解放する', async t => {
  const f = await fixture(t);
  const { publicKey } = await loadNoiseKey(f.configDir);
  const pairing = new Pairing({ relay: f.relay, bridgePublic: publicKey, name: 'pair-test', permissions: ['notify'] });
  const requests = await startDisconnectingPairControl(t, f.stateDir, pairing.qr());
  // exit ハンドラによる回収と区別するため、試験側でロック解放を確認するまでプロセスを生かす。
  const keepAlive = `
    const timer = setInterval(() => {}, 60_000);
    process.once('SIGUSR2', () => clearInterval(timer));
  `;
  const term = f.open(160, 60, [`--import=data:text/javascript,${encodeURIComponent(keepAlive)}`]);
  await until(() => term.output.includes('6桁:'));
  assert.equal((await f.readLock()).pid, term.terminal.pid);

  term.terminal.write('123456\r');
  await until(() => term.output.includes('登録に失敗しました: 接続が切れました'));
  await until(async () => {
    try { await fs.stat(f.lockPath); return false; }
    catch (error) { if (error.code === 'ENOENT') return true; throw error; }
  });
  term.kill('SIGUSR2');
  await until(() => term.exited);
  assert.equal((await term.result).exitCode, 1);
  assert.deepEqual(requests.map(request => request.op), ['pair_start', 'pair_confirm']);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

async function pauseLocalConfirmation(t) {
  const f = await fixture(t);
  const qrFile = path.join(f.stateDir, 'test-qr.json');
  const marker = path.join(f.stateDir, 'confirm-paused');
  const preload = `
    import { existsSync, writeFileSync } from 'node:fs';
    import { setTimeout as delay } from 'node:timers/promises';
    import { MobileHub } from ${JSON.stringify(new URL('../mobile-hub.mjs', import.meta.url).href)};
    import { Pairing } from ${JSON.stringify(new URL('../pairing.mjs', import.meta.url).href)};
    const qrFile = ${JSON.stringify(qrFile)};
    const marker = ${JSON.stringify(marker)};
    const qr = Pairing.prototype.qr;
    Pairing.prototype.qr = function () {
      const value = qr.call(this);
      writeFileSync(qrFile, value);
      return value;
    };
    const reload = MobileHub.prototype.reloadDevices;
    let firstWithDevices = true;
    MobileHub.prototype.reloadDevices = async function () {
      const result = await reload.call(this);
      if (firstWithDevices && this.devices.length > 0) {
        firstWithDevices = false;
        writeFileSync(marker, '');
      }
      // ファイル監視による並行 reload が先に着いても、confirm 側を同じ目印で止める。
      while (existsSync(marker)) await delay(5);
      return result;
    };
  `;
  const term = f.open(160, 60, [`--import=data:text/javascript,${encodeURIComponent(preload)}`]);
  await until(() => term.output.includes('6桁:'));
  const qr = JSON.parse(await fs.readFile(qrFile, 'utf8'));
  await f.relay.bridgeUp();
  const app = new FakeApp(f.relay, 'pair', Buffer.from(qr.pairing, 'base64url'), Buffer.from(qr.bridge, 'base64url'));
  const secret = Buffer.from(qr.secret, 'base64url');
  assert.ok(await app.handshake());
  app.sendJson(app.pairMessage(secret));
  await until(() => term.output.includes('候補が接続しました'));
  term.terminal.write(`${app.sas(secret)}\r`);
  await until(async () => fs.stat(marker).then(() => true, error => {
    if (error.code === 'ENOENT') return false;
    throw error;
  }));
  return { f, term, app, marker };
}

test('B21 確定中の Ctrl+C は端末登録と paired の通知を待ってから終了する', async t => {
  const { f, term, app, marker } = await pauseLocalConfirmation(t);
  term.terminal.write('\x03');
  await until(() => {
    assert.equal(term.exited, false, '確定中の最初の SIGINT で CLI が終了した');
    return term.output.includes('登録の処理が終わるまで待っています');
  });
  await fs.unlink(marker);

  await until(() => term.exited);
  assert.equal((await term.result).exitCode, 130);
  assert.match(term.output, /登録しました/);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
  const paired = await app.recv(message => message.type === 'paired');
  assert.deepEqual((await loadDevices(f.configDir)).map(device => device.device), [paired.device]);
  assert.ok(term.output.includes(`端末 ID ${paired.device}`));
});

test('B22 確定中の2回目の Ctrl+C は確定を待たずにロックを解放する', async t => {
  const { f, term, marker } = await pauseLocalConfirmation(t);
  term.terminal.write('\x03');
  await until(() => term.output.includes('登録の処理が終わるまで待っています'));
  assert.equal(term.exited, false);
  term.kill('SIGINT');

  await until(() => term.exited);
  assert.equal((await term.result).exitCode, 130);
  await fs.stat(marker);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

async function startWaitingPairControl(t, f, ttl, { answerCancel = true } = {}) {
  const { publicKey } = await loadNoiseKey(f.configDir);
  const requests = [];
  const sockets = new Set();
  await fs.mkdir(f.stateDir, { recursive: true, mode: 0o700 });
  const server = net.createServer(socket => {
    sockets.add(socket);
    socket.on('close', () => sockets.delete(socket));
    const lines = createInterface({ input: socket });
    lines.on('line', line => {
      const request = JSON.parse(line);
      requests.push(request.op);
      if (request.op === 'pair_start') {
        const pairing = new Pairing({ relay: f.relay, bridgePublic: publicKey, name: request.name, permissions: request.permissions, ttl });
        socket.write(`${JSON.stringify({ ok: true, qr: pairing.qr() })}\n`);
      }
      if (request.op === 'pair_cancel' && answerCancel) socket.write('{"ok":true}\n');
    });
  });
  server.listen(path.join(f.stateDir, 'pair.sock'));
  await once(server, 'listening');
  const disconnect = () => { for (const socket of sockets) socket.destroy(); };
  t.after(async () => {
    disconnect();
    await new Promise(resolve => server.close(resolve));
  });
  return { disconnect, requests };
}

test('B23 6桁の入力待ちは QR の期限切れで終了しロックを解放する', async t => {
  const f = await fixture(t);
  const control = await startWaitingPairControl(t, f, 2);
  const term = f.open();
  await until(() => term.output.includes('6桁:'));

  await until(() => term.exited);
  assert.equal((await term.result).exitCode, 1);
  assert.match(term.output, /この QR の期限が切れました/);
  assert.deepEqual(control.requests, ['pair_start', 'pair_cancel']);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

test('B24 6桁の入力待ちは制御ソケットの切断で終了しロックを解放する', async t => {
  const f = await fixture(t);
  const control = await startWaitingPairControl(t, f);
  const term = f.open();
  await until(() => term.output.includes('6桁:'));
  control.disconnect();

  await until(() => term.exited);
  assert.equal((await term.result).exitCode, 1);
  assert.match(term.output, /常駐の bridge との接続が切れました/);
  assert.deepEqual(control.requests, ['pair_start']);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

test('B25 期限切れの後、常駐の bridge が pair_cancel に答えなくても、ロックを解放して終わる', async t => {
  const f = await fixture(t);
  const control = await startWaitingPairControl(t, f, 2, { answerCancel: false });
  const term = f.open();
  await until(() => term.output.includes('6桁:'));

  await until(() => term.exited, 6000);
  assert.equal((await term.result).exitCode, 1);
  assert.match(term.output, /この QR の期限が切れました/);
  assert.deepEqual(control.requests, ['pair_start', 'pair_cancel']);
  await assert.rejects(fs.stat(f.lockPath), { code: 'ENOENT' });
});

test('B26 6桁を受け付けた後で QR の期限が来ても、確定を最後まで進めて paired を送る', async t => {
  const f = await fixture(t);
  let pairing, hub, candidateSession, candidateOpenAtExpiry;
  hub = await f.startDaemon({
    async loadDevices(dir) {
      const devices = await loadDevices(dir);
      // Hold the reload that follows saving the device until the hub's own
      // expiry has run, then record whether the accepted candidate survived it.
      if (devices.length && candidateSession) {
        await until(() => hub.pairing === null, 5000);
        candidateOpenAtExpiry = !candidateSession.closed;
      }
      return devices;
    },
  });
  // exp is whole seconds, so 2 leaves at least 1 second to accept the code.
  pairing = hub.startPairing({ name: 'pair-test', permissions: ['notify'], ttl: 2 });
  const app = new FakeApp(f.relay, 'pair', pairing.id, hub.noiseKey.publicKey);
  assert.ok(await app.handshake());
  app.sendJson(app.pairMessage(pairing.secret));
  await until(() => pairing.candidates.length === 1);

  candidateSession = pairing.candidates[0].session;
  const device = await hub.confirmPairing(app.sas(pairing.secret));
  assert.equal(candidateOpenAtExpiry, true, '期限の処理が、受け付けた候補の接続を閉じた');
  assert.ok(device, '期限をまたいだ確定が失敗した');
  const paired = await app.recv(message => message.type === 'paired');
  assert.equal(paired.device, device.device);
  assert.deepEqual((await loadDevices(f.configDir)).map(item => item.device), [device.device]);
  assert.equal(hub.pairing, null);
});

test('B27 確定の保存が例外で終わったら、受け付けた候補の接続をすぐ閉じる', async t => {
  const f = await fixture(t);
  const hub = await f.startDaemon();
  const pairing = hub.startPairing({ name: 'pair-test', permissions: ['notify'] });
  const app = new FakeApp(f.relay, 'pair', pairing.id, hub.noiseKey.publicKey);
  assert.ok(await app.handshake());
  app.sendJson(app.pairMessage(pairing.secret));
  await until(() => pairing.candidates.length === 1);
  const session = pairing.candidates[0].session;

  // A read-only config folder makes saving the device fail with EACCES.
  await fs.chmod(f.configDir, 0o500);
  try {
    await assert.rejects(hub.confirmPairing(app.sas(pairing.secret)), { code: 'EACCES' });
  } finally {
    await fs.chmod(f.configDir, 0o700);
  }
  assert.equal(session.closed, true);
  await until(() => app.conn.closed);
  assert.deepEqual(await loadDevices(f.configDir), []);
});
