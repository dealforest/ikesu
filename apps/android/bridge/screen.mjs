// スマホに出す ikesu の画面（PROTOCOL.md 4章）。PC 側で ikesu をスマホの大きさの PTY の中につなぎ、
// その出力を 30ms ごとにまとめて送る。アプリの文字は ikesu の引数に入れない。
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

const require = createRequire(import.meta.url);
const execute = promisify(execFile);
export const FLUSH_MS = 30;
export const LIMITS = { cols: [20, 300], rows: [10, 200] };
// まとめて送る1通の上限。暗号の1通（60,000 バイト）に base64 と JSON の分を足しても収まる大きさ。
const MAX_BATCH = 40 * 1024;

export const validSize = (cols, rows) => Number.isInteger(cols) && Number.isInteger(rows) &&
  cols >= LIMITS.cols[0] && cols <= LIMITS.cols[1] && rows >= LIMITS.rows[0] && rows <= LIMITS.rows[1];

// node-pty の配布物は、PTY を開く補助のファイルに実行の印が付いていないことがある（npm の install scripts は使わない）。
function ensureSpawnHelper() {
  const root = path.dirname(require.resolve('node-pty/package.json'));
  for (const arch of ['darwin-arm64', 'darwin-x64']) {
    const file = path.join(root, 'prebuilds', arch, 'spawn-helper');
    try { const mode = fs.statSync(file).mode; if ((mode & 0o111) !== 0o111) fs.chmodSync(file, mode | 0o755); } catch { /* その形が無ければよい */ }
  }
}

// 稼働中の ikesu サーバーにつながるか。つながらないときに ikesu を起動すると、新しいサーバーを作ってしまう。
export async function serverAlive(ikesuBin, env = process.env) {
  try { const { stdout } = await execute(ikesuBin, ['pane', 'list'], { env, timeout: 10000 }); return JSON.parse(stdout)?.result?.type === 'pane_list'; }
  catch { return false; }
}

// bridge は ikesu run のジョブ（ikesu サーバーの子孫）として動くので、そのまま ikesu を起動すると
// 「入れ子の ikesu」として断られる（internal/client/nested.go）。この画面は PTY の中のクライアントで、
// ペインの中の入れ子ではない。持ち主の設定を写し、[experimental] に allow_nested = true を足したものを、
// この1つのクライアントにだけ渡す。写しは画面を閉じたら消す。
// 表（[experimental] など）に key = true を1つだけ置く。前からある同じ名前の行は消す。
function setValue(lines, table, key, value) {
  const kept = lines.filter(line => !new RegExp(`^\\s*${key}\\s*=`).test(line));
  const header = kept.findIndex(line => new RegExp(`^\\s*\\[${table}\\]\\s*(#.*)?$`).test(line));
  if (header >= 0) kept.splice(header + 1, 0, `${key} = ${value}`);
  else kept.push('', `[${table}]`, `${key} = ${value}`);
  return kept;
}
const setTrue = (lines, table, key) => setValue(lines, table, key, 'true');

// アプリの ≡ が送る F12 で、隠したサイドバーを出し入れする。持ち主の割り当て（無ければ既定の prefix+b）も残す。
export const SIDEBAR_KEY = 'f12';
function sidebarBindings(lines) {
  const line = lines.find(l => /^\s*toggle_sidebar\s*=/.test(l));
  const value = line ? line.replace(/^\s*toggle_sidebar\s*=\s*/, '').replace(/\s+#.*$/, '').trim() : '"prefix+b"';
  const specs = value.startsWith('[') ? [...value.matchAll(/"([^"]*)"/g)].map(m => m[1]) : [value.replace(/^"|"$/g, '')];
  const kept = specs.filter(spec => spec && spec !== SIDEBAR_KEY);
  return `[${[...kept, SIDEBAR_KEY].map(spec => JSON.stringify(spec)).join(', ')}]`;
}

// スマホの画面の ikesu だけ: 入れ子を許し、メニューとダイアログを Android の画面で出す合図（ui.native_menus）を有効にする。
// 狭い画面を広く使うため、左のサイドバーは最初から隠し（畳んだときも幅を残さない）、アプリの ≡（F12）で出す。
export function nestedClientConfig(text) {
  let lines = String(text).split('\n');
  const toggle = sidebarBindings(lines);
  lines = setTrue(lines, 'experimental', 'allow_nested');
  lines = setTrue(lines, 'ui', 'native_menus');
  lines = setTrue(lines, 'ui', 'sidebar_start_collapsed');
  lines = setValue(lines, 'ui', 'sidebar_collapsed_mode', '"hidden"');
  lines = setValue(lines, 'keys', 'toggle_sidebar', toggle);
  return lines.join('\n') + '\n';
}

function writeClientConfig(env) {
  const source = env.IKESU_CONFIG || path.join(env.HOME || os.homedir(), '.config/ikesu/config.toml');
  let text = '';
  try { text = fs.readFileSync(source, 'utf8'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ikesu-screen-'));
  const file = path.join(dir, 'config.toml');
  // 書いている途中で失敗したら、作ったフォルダごと消してから失敗を返す（写しの一部を残さない）。
  try { fs.writeFileSync(file, nestedClientConfig(text), { mode: 0o600, flag: 'wx' }); }
  catch (error) { fs.rmSync(dir, { recursive: true, force: true }); throw error; }
  return { file, dir };
}

// スマホから送られたファイルを保存する場所と、画面に打つパス（シェルで使える形にクォートする）。
export const MAX_FILE_BYTES = 20 * 1024 * 1024;
export function safeFileName(name) {
  const base = path.basename(String(name || '')).normalize('NFC').replace(/[\u0000-\u001f\u007f/\\:]/g, '_').replace(/^\.+/, '').slice(0, 120);
  return base || 'file';
}
export const shellQuote = text => `'${String(text).replace(/'/g, `'\\''`)}'`;
export function saveIncomingFile(name, bytes, { home = os.homedir(), now = new Date() } = {}) {
  const dir = path.join(home, 'Downloads', 'ikesu-phone');
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const stamp = now.toISOString().replace(/[-:]/g, '').replace(/\..*/, '').replace('T', '-');
  const safe = safeFileName(name);
  for (let i = 0; ; i++) {
    const file = path.join(dir, `${stamp}${i ? `-${i}` : ''}-${safe}`);
    try { fs.writeFileSync(file, bytes, { mode: 0o600, flag: 'wx' }); return file; }
    catch (error) { if (error.code !== 'EEXIST' || i > 100) throw error; }
  }
}

export class Screen {
  constructor({ id, cols, rows, ikesuBin, env = process.env, onData, onExit, spawn }) {
    Object.assign(this, { id, onData, onExit });
    this.seq = 0; this.pending = []; this.pendingBytes = 0; this.timer = null; this.closed = false;
    const childEnv = { ...env, TERM: 'xterm-256color', COLORTERM: 'truecolor' };
    delete childEnv.IKESU_PANE_ID;
    if (!spawn) { this.config = writeClientConfig(env); childEnv.IKESU_CONFIG = this.config.file; }
    if (!spawn) { try { ensureSpawnHelper(); spawn = require('node-pty').spawn; } catch (error) { this.removeConfig(); throw error; } }
    // つなぐセッションは、bridge が話しているサーバーと同じにする（IKESU_SOCKET_PATH のフォルダ名。引数なしは default）。
    const session = env.IKESU_SOCKET_PATH ? path.basename(path.dirname(env.IKESU_SOCKET_PATH)) : '';
    // 引数に渡せない名前のセッションは開かない（default につないで新しいサーバーを作らないため）。
    if (session && session !== 'default' && !/^[A-Za-z0-9._-]+$/.test(session)) { this.removeConfig(); throw Error(`このセッション名には画面を開けません: ${session}`); }
    const args = session && session !== 'default' ? ['--session', session] : [];
    // 起動に失敗したら、写した設定を残さない。
    try { this.pty = spawn(ikesuBin, args, { name: 'xterm-256color', cols, rows, cwd: env.HOME || process.cwd(), env: childEnv, encoding: null }); }
    catch (error) { this.removeConfig(); throw error; }
    // 最初の出力の一部を残しておき、すぐ終わったときの理由を記録に書けるようにする。
    this.head = Buffer.alloc(0);
    this.pty.onData(chunk => { const bytes = Buffer.from(chunk); if (this.head.length < 400) this.head = Buffer.concat([this.head, bytes]).subarray(0, 400); this.buffer(bytes); });
    this.pty.onExit(({ exitCode, signal } = {}) => { this.flush(); const wasClosed = this.closed; this.closed = true; clearTimeout(this.timer); this.removeConfig(); this.onExit?.({ exitCode, signal, closedByUs: wasClosed, head: this.head }); });
  }
  buffer(bytes) {
    if (this.closed) return;
    this.pending.push(bytes); this.pendingBytes += bytes.length;
    if (this.pendingBytes >= MAX_BATCH) { this.flush(); return; }
    if (!this.timer) { this.timer = setTimeout(() => this.flush(), FLUSH_MS); this.timer.unref?.(); }
  }
  flush() {
    clearTimeout(this.timer); this.timer = null;
    if (!this.pendingBytes) return;
    const all = Buffer.concat(this.pending); this.pending = []; this.pendingBytes = 0;
    for (let offset = 0; offset < all.length; offset += MAX_BATCH) this.onData(this.seq++, all.subarray(offset, offset + MAX_BATCH));
  }
  // 終わりかけの PTY への書き込みは失敗しうる。接続ごと切らずに捨てる。
  write(bytes) { if (!this.closed) try { this.pty.write(bytes); } catch { /* 終わりかけ */ } }
  resize(cols, rows) { if (!this.closed) try { this.pty.resize(cols, rows); } catch { /* 終わりかけ */ } }
  removeConfig() { if (this.config) { fs.rmSync(this.config.dir, { recursive: true, force: true }); this.config = null; } }
  close() {
    if (this.closed) return;
    this.closed = true; clearTimeout(this.timer);
    try { this.pty.kill(); } catch { /* もう終わっている */ }
    this.removeConfig();
  }
}
