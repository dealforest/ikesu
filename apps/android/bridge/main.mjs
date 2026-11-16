#!/usr/bin/env node
import fs from 'node:fs';
import path from 'node:path';
import os from 'node:os';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { fileURLToPath } from 'node:url';
import { acquireDaemonLock, createGetDecision, createRunAnswer, ensureNativeDialog, spawnDecisionWatch } from '../../mac-dialog-decisions/mac-dialog-decisions.mjs';
import { ClientViews } from '../../mac-dialog-decisions/client-views.mjs';
import { RichService } from '../../mac-dialog-decisions/rich-service.mjs';
import { ownerAlive, ownerProven, readOwner } from '../../mac-dialog-decisions/lifecycle.mjs';
import { DecisionRegistry } from './registry.mjs';
import { loadDevices, loadNoiseKey, loadRelay, PERMISSIONS, revokeDevice, saveRelay, addDeviceByHand, fingerprint, fromB64u } from './devices.mjs';
import { localPairing, remotePairing, startMobile } from './mobile-runtime.mjs';
import readline from 'node:readline/promises';
import qrcode from 'qrcode-terminal';

const exec = promisify(execFile);
const root = path.dirname(fileURLToPath(import.meta.url));
const pluginId = 'kazuph.android';
const configDir = process.env.IKESU_PLUGIN_CONFIG_DIR || path.join(os.homedir(), '.config/ikesu/plugins/config', pluginId);
const stateDir = process.env.IKESU_PLUGIN_STATE_DIR || path.join(os.homedir(), '.local/state/ikesu/plugins', pluginId);
let file = {};
try { file = JSON.parse(fs.readFileSync(path.join(configDir, 'config.json'), 'utf8')); }
catch (error) { if (error.code !== 'ENOENT') throw error; }
const config = { pluginId, configDir, stateDir, enabled: file.enabled !== false,
  ikesuBin: process.env.IKESU_BIN_PATH || file.ikesu_bin || 'ikesu',
  laterMs: (file.later_minutes ?? 10) * 60000 };
if (!Number.isFinite(config.laterMs) || config.laterMs < 0) throw Error('設定値が不正です');
const log = message => process.stderr.write(`[android-decisions] ${message}\n`);

// 中継の設定（relay.json）を読めるか。読めなければ1行の案内を出して false。
async function relayReady() {
  try {
    if (await loadRelay(configDir)) return true;
    log('relay.json がありません。main.mjs relay-setup --url <wss://…> で中継を設定してください');
  } catch (error) { log(`relay.json を読めません: ${error.message}`); }
  return false;
}

async function ensure() {
  // 中継の設定が無いと run はすぐ終わるので、判断待ちのたびにジョブを起こさない。
  if (!await relayReady()) return;
  fs.mkdirSync(stateDir, { recursive: true, mode: 0o700 });
  const lock = path.join(stateDir, 'ensure.lock');
  try { fs.mkdirSync(lock, { mode: 0o700 }); }
  catch (error) {
    if (error.code !== 'EEXIST') throw error;
    try { if (Date.now() - fs.statSync(lock).mtimeMs <= 60_000) return; fs.rmdirSync(lock); fs.mkdirSync(lock, { mode: 0o700 }); }
    catch (failure) { if (failure.code === 'ENOENT' || failure.code === 'EEXIST') return; throw failure; }
  }
  try {
    const owner = readOwner(stateDir);
    if (ownerAlive(owner)) {
      if (!ownerProven(owner, stateDir)) log('稼働中プロセスの所有を確認できないため起動しません');
      return;
    }
    const starting = path.join(stateDir, 'starting.json');
    try { if (Date.now() - JSON.parse(fs.readFileSync(starting, 'utf8')).started < 30000) return; } catch {}
    const vars = ['HOME', 'PATH', 'IKESU_SOCKET_PATH', 'IKESU_SOCKET_PATH_EXPLICIT', 'IKESU_CONFIG_PATH', 'IKESU_BIN_PATH'].filter(key => process.env[key]).map(key => `${key}=${process.env[key]}`);
    vars.push(`IKESU_PLUGIN_CONFIG_DIR=${configDir}`, `IKESU_PLUGIN_STATE_DIR=${stateDir}`);
    await exec(config.ikesuBin, ['run', '--label', 'android-decisions', '--cwd', root, '--completion', 'none', '--', 'env', ...vars, process.execPath, path.join(root, 'main.mjs'), 'run']);
    fs.writeFileSync(starting, JSON.stringify({ started: Date.now() }), { mode: 0o600 });
  } finally { fs.rmdirSync(lock); }
}

async function run() {
  // パネルの準備・ロック・daemon.json の前に確かめる（終わったプロセスの daemon.json を残さない）。
  if (!await relayReady()) { process.exitCode = 1; return; }
  fs.mkdirSync(stateDir, { recursive: true, mode: 0o700 });
  const nativeDialog = ensureNativeDialog(config, log);
  if (!nativeDialog) throw Error('画像の検査に使う Mac ダイアログを準備できません');
  const lock = await acquireDaemonLock(nativeDialog, stateDir);
  if (!lock) return;
  fs.writeFileSync(path.join(stateDir, 'daemon.json'), JSON.stringify({ pid: process.pid, stateDir: fs.realpathSync(stateDir), script: fileURLToPath(import.meta.url), node: process.execPath }), { mode: 0o600 });
  const clientViews = new ClientViews();
  const rich = new RichService({ ...config, nativeDialog });
  const daemon = new DecisionRegistry({ config, clientViews, rich,
    spawnWatch: () => spawnDecisionWatch(config, log),
    runAnswer: createRunAnswer(config, log), getDecision: createGetDecision(config, log), log });
  // スマホとは中継（relay.json）だけでつながる。Tailscale と合言葉の経路は 0.4.155 で消した。
  const mobile = await startMobile({ configDir, stateDir, log, decisions: {
    snapshot: () => daemon.snapshot(), answer: value => daemon.answer(value), later: id => daemon.later(id), shown: ids => daemon.shown(ids),
    videoPath: (id, key) => daemon.videoPath(id, key), createdOf: id => daemon.find(id)?.decision?.created_unix_ms } });
  if (!mobile) { log('relay.json がありません。main.mjs relay-setup --url <wss://…> で中継を設定してください'); await lock.close(); return; }
  daemon.onSnapshot = () => mobile.hub.publish();
  daemon.hasClients = () => mobile.hub.clientCount() > 0;
  await daemon.start();
  const stop = async () => { await mobile.stop(); await daemon.stop(); await lock.close(); };
  for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, () => { void stop().then(() => process.exit(0)); });
  log(`中継 ${mobile.relay.url} につなぎます`);
}

function option(name) { const index = process.argv.indexOf(`--${name}`); return index < 0 ? undefined : process.argv[index + 1]; }

async function relaySetup() {
  const url = option('url');
  if (!url) throw Error('usage: main.mjs relay-setup --url <wss://…> [--key <base64url 32バイト>] [--force]');
  if (await loadRelay(configDir) && !process.argv.includes('--force')) throw Error('relay.json はすでにあります。作り直すと登録済みの端末がつながらなくなります（上書きは --force）');
  const key = option('key') ?? process.env.RELAY_KEY;
  const saved = await saveRelay(configDir, { url, relayKey: key ? fromB64u(key, 32) : undefined });
  process.stdout.write(`relay.json を書きました。\n部屋 ID: ${saved.room}\nRELAY_KEY: ${saved.relayKey}\n中継の Worker に同じ値を入れてください:\n  printf '%s' '${saved.relayKey}' | npx wrangler secret put RELAY_KEY\n`);
}

async function pair() {
  const name = option('name');
  if (!name) throw Error('usage: main.mjs pair --name <PC の名前> [--permissions notify,answer,screen,input]');
  const permissions = (option('permissions') ?? PERMISSIONS.join(',')).split(',').filter(Boolean);
  if (!permissions.every(item => PERMISSIONS.includes(item))) throw Error(`権限は ${PERMISSIONS.join(',')} から選んでください`);
  let session = await remotePairing(stateDir);
  let mobile = null;
  if (!session) {
    mobile = await startMobile({ configDir, stateDir, log, control: false, decisions: { snapshot: async () => [], answer: async () => ({}), later: async () => {} } });
    if (!mobile) throw Error('relay.json がありません。先に relay-setup --url <wss://…> を実行してください');
    log('run 中の bridge が見つからないため、この端末で一時的に中継へつなぎます');
    session = localPairing(mobile);
  }
  const qr = await session.start({ name, permissions });
  qrcode.generate(qr, { small: true });
  process.stdout.write(`この PC の鍵の指紋: ${fingerprint((await loadNoiseKey(configDir)).publicKey)}（アプリに出る指紋と同じか確かめてください）\n`);
  process.stdout.write('アプリのカメラで上の QR を読んでください（5分間有効）。アプリに出る6桁をここに入力します。\n');
  session.onCandidate(candidate => process.stdout.write(`\n候補が接続しました（${candidate.name}）\n`));
  const prompt = readline.createInterface({ input: process.stdin, output: process.stdout });
  try {
    for (;;) {
      const code = await prompt.question('6桁: ');
      const result = await session.confirm(code);
      if (result.device) { process.stdout.write(`登録しました（端末 ID ${result.device}）\n`); return; }
      if (!result.active) { process.stdout.write('この QR は無効になりました。もう一度 pair を実行してください\n'); process.exitCode = 1; return; }
      process.stdout.write('一致しません\n');
    }
  } finally { prompt.close(); await session.cancel().catch(() => {}); session.close(); await mobile?.stop(); }
}

async function devices() {
  if (process.argv[3] === 'add') {
    const permissions = option('permissions')?.split(',').filter(Boolean);
    const { link, fingerprints } = await addDeviceByHand(configDir, { name: option('name'), noise: option('noise'), operate: option('operate'), pcName: option('pc-name'), ...(permissions ? { permissions } : {}) });
    process.stderr.write(`登録したスマホの鍵の指紋: ${fingerprints.device}（アプリの「鍵を作る」に出る指紋と同じか確かめてください）\nこの PC の鍵の指紋: ${fingerprints.bridge}\n`);
    process.stdout.write(`${link}\n`);
    return;
  }
  const list = await loadDevices(configDir);
  if (!list.length) process.stdout.write('登録済みの端末はありません\n');
  for (const device of list) process.stdout.write(`${device.device}\t${device.name}\t${device.permissions.join(',')}\t${device.created}\n`);
}

async function revoke() {
  const id = process.argv[3];
  if (!id) throw Error('usage: main.mjs revoke <device>');
  if (!await revokeDevice(configDir, id)) throw Error('その端末 ID は登録されていません');
  process.stdout.write('外しました。run 中の bridge は再起動なしでその端末の接続を切ります\n');
}

const mode = process.argv[2];
if (mode === 'relay-setup') await relaySetup();
else if (mode === 'pair') await pair();
else if (mode === 'devices') await devices();
else if (mode === 'revoke') await revoke();
else if (process.platform !== 'darwin') throw Error('bridge は macOS 専用です');
else if (!config.enabled) log('設定で無効化されています');
else if (mode === 'ensure') await ensure();
else if (mode === 'run') await run();
else throw Error('usage: main.mjs ensure|run|relay-setup|pair|devices|revoke');
