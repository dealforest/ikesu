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
export function nestedClientConfig(text) {
  const lines = String(text).split('\n').filter(line => !/^\s*allow_nested\s*=/.test(line));
  const header = lines.findIndex(line => /^\s*\[experimental\]\s*(#.*)?$/.test(line));
  if (header >= 0) lines.splice(header + 1, 0, 'allow_nested = true');
  else lines.push('', '[experimental]', 'allow_nested = true');
  return lines.join('\n') + '\n';
}

function writeClientConfig(env) {
  const source = env.IKESU_CONFIG || path.join(env.HOME || os.homedir(), '.config/ikesu/config.toml');
  let text = '';
  try { text = fs.readFileSync(source, 'utf8'); } catch (error) { if (error.code !== 'ENOENT') throw error; }
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ikesu-screen-'));
  const file = path.join(dir, 'config.toml');
  fs.writeFileSync(file, nestedClientConfig(text), { mode: 0o600, flag: 'wx' });
  return { file, dir };
}

export class Screen {
  constructor({ id, cols, rows, ikesuBin, env = process.env, onData, onExit, spawn }) {
    Object.assign(this, { id, onData, onExit });
    this.seq = 0; this.pending = []; this.pendingBytes = 0; this.timer = null; this.closed = false;
    const childEnv = { ...env, TERM: 'xterm-256color', COLORTERM: 'truecolor' };
    delete childEnv.IKESU_PANE_ID;
    if (!spawn) { this.config = writeClientConfig(env); childEnv.IKESU_CONFIG = this.config.file; }
    if (!spawn) { ensureSpawnHelper(); spawn = require('node-pty').spawn; }
    this.pty = spawn(ikesuBin, [], { name: 'xterm-256color', cols, rows, cwd: env.HOME || process.cwd(), env: childEnv, encoding: null });
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
  write(bytes) { if (!this.closed) this.pty.write(bytes); }
  resize(cols, rows) { if (!this.closed) this.pty.resize(cols, rows); }
  removeConfig() { if (this.config) { fs.rmSync(this.config.dir, { recursive: true, force: true }); this.config = null; } }
  close() {
    if (this.closed) return;
    this.closed = true; clearTimeout(this.timer);
    try { this.pty.kill(); } catch { /* もう終わっている */ }
  }
}
