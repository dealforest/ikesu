// 中継から届く接続を受け持つ。握手・ペアリング・通知・操作（PROTOCOL.md 3〜3.5・5）。
import { createReadStream } from 'node:fs';
import { randomBytes } from 'node:crypto';
import { Assembler, encodeMessage, NoiseResponder } from './noise-channel.mjs';
import { addDevice, b64u, loadDevices } from './devices.mjs';
import { constantEqual, Pairing, verifyOperate } from './pairing.mjs';
import { appTag } from './relay-client.mjs';
import { validateAnswer, validateShown } from './socket.mjs';

const ONLY = (object, keys) => object && typeof object === 'object' && !Array.isArray(object) && Object.keys(object).every(key => keys.includes(key));
const HANDSHAKE_WINDOW_MS = 60_000;
const HANDSHAKE_FAILURES_PER_WINDOW = 10;
const FAILURE_IDS_KEPT = 256;

class Session {
  constructor(hub, key, role, id) {
    Object.assign(this, { hub, key, role, id, closed: false, ready: false, operating: false, beatN: 0, device: null, sentVideos: new Set(), assembler: new Assembler() });
    this.noise = new NoiseResponder({ staticKey: hub.noiseKey, room: hub.roomBytes, now: hub.now });
    this.handshakeTimer = setTimeout(() => this.close(), hub.handshakeMs); this.handshakeTimer.unref?.();
  }
  close(failedHandshake = false, notifyRelay = true) {
    if (this.closed) return;
    this.closed = true;
    if (failedHandshake) this.hub.noteHandshakeFailure(this.id);
    clearTimeout(this.handshakeTimer); this.stopOperating(false);
    if (this.slot) this.hub.pairing?.release(this.slot);
    if (this.candidate) this.hub.pairing?.dropCandidate(this.candidate);
    this.hub.sessions.delete(this.key);
    if (notifyRelay) this.hub.transport.closeConn(this.key);
  }
  send(value) {
    if (this.closed || !this.ready) return;
    if (this.noise.needsRenewal) { this.close(); return; }
    try { for (const part of encodeMessage(value, () => (this.messageId = ((this.messageId ?? 0) + 1) >>> 0))) this.hub.transport.sendData(this.key, this.noise.encrypt(part)); }
    catch (error) { this.hub.log(`送信に失敗: ${error.message}`); this.close(); }
  }
  async onData(buffer) {
    if (this.closed) return;
    if (!this.ready) { this.handshake(buffer); return; }
    let message;
    try {
      const plain = this.noise.decrypt(buffer);
      // 分割したメッセージは、顔・指紋で開いた後の回答（画像つき）だけが使う。開く前は溜めさせない。
      if (plain[0] === 0x01 && !(this.role === 'app' && this.operating)) { this.close(); return; }
      message = this.assembler.push(plain);
    } catch { this.close(); return; }
    if (message === undefined) return;
    try { await (this.role === 'pair' ? this.onPair(message) : this.onApp(message)); }
    catch (error) { this.hub.log(`処理に失敗: ${error.message}`); this.close(); }
  }
  handshake(buffer) {
    try {
      const remote = this.noise.receiveFirst(buffer);
      if (this.role === 'app') {
        this.device = this.hub.devices.find(device => device.device === b64u(this.id));
        if (!this.device || !constantEqual(this.device.noise_pub, b64u(remote))) { this.close(true); return; }
      } else {
        this.slot = this.hub.pairing?.matches(this.id) ? this.hub.pairing.reserve() : null;
        if (!this.slot) { this.close(true); return; }
      }
      const reply = this.noise.reply();
      this.hub.transport.sendData(this.key, reply);
      this.ready = true; clearTimeout(this.handshakeTimer);
      this.remote = remote;
      if (this.role === 'app') { this.hub.log(`${this.device.name} が接続しました`); this.sendNotice(); }
    } catch { this.close(true); }
  }
  async onPair(message) {
    if (this.paired || this.candidate || !ONLY(message, ['type', 'proof', 'name', 'operate']) || message.type !== 'pair') { this.close(); return; }
    const pairing = this.hub.pairing;
    const result = pairing?.submitProof(this.slot, { h: this.noise.hash, proof: message.proof, name: message.name, operate: message.operate, noisePublic: this.remote });
    if (!result || result.error) {
      if (result?.error === 'proof') this.hub.log('ペアリングの proof が合いません');
      if (result?.error === 'proof' && !pairing.active) this.hub.log('外れが続いたため、この QR を無効にしました');
      this.close(); return;
    }
    this.candidate = result.candidate; this.candidate.session = this;
    this.hub.log(`ペアリング候補が来ました（${this.candidate.name}）。PC の端末に 6 桁を入力してください`);
    this.hub.onCandidate?.(this.candidate);
  }
  // ---- ふだんの接続 ----
  can(permission) { return this.device.permissions.includes(permission); }
  async sendNotice() {
    if (!this.ready || this.closed || !this.can('notify')) return;
    try {
      const decisions = (await this.hub.snapshot()).map(item => ({ id: item.id, title: item.raw_title ?? item.title ?? '', origin: item.location ?? '',
        created_unix_ms: this.hub.createdOf?.(item.id) ?? item.created_unix_ms ?? 0, notify: item.notify !== false }));
      this.send({ type: 'notice', decisions });
    } catch (error) { this.hub.log(`通知の作成に失敗: ${error.message}`); }
  }
  async sendSnapshot() {
    if (!this.operating) return;
    const decisions = await this.hub.snapshot();
    if (!this.operating) return;
    this.send({ type: 'snapshot', decisions });
    void this.sendVideos(decisions);
  }
  async sendVideos(decisions) {
    for (const decision of decisions) for (const video of decision.videos || []) {
      if (!video || this.sentVideos.has(video.key)) continue;
      const file = this.hub.videoPath(decision.id, video.key);
      if (!file) continue;
      this.sentVideos.add(video.key);
      try {
        let index = 0;
        for await (const bytes of createReadStream(file, { highWaterMark: 32 * 1024 })) {
          if (!this.operating) return;
          this.send({ type: 'video_chunk', id: decision.id, key: video.key, index: index++, data: bytes.toString('base64') });
          await this.hub.transport.drain?.();
        }
        if (this.operating) this.send({ type: 'video_end', id: decision.id, key: video.key });
      } catch { this.sentVideos.delete(video.key); }
    }
  }
  startOperating() {
    this.stopOperating(false);
    this.operating = true; this.touch();
    this.send({ type: 'operate_ok', until: this.until });
    this.beatTimer = setInterval(() => this.send({ type: 'beat', n: ++this.beatN }), this.hub.beatMs);
    this.beatTimer.unref?.();
    void this.sendSnapshot().catch(() => this.close());
  }
  touch() {
    clearTimeout(this.idleTimer);
    this.until = this.hub.now() + this.hub.idleMs;
    this.idleTimer = setTimeout(() => this.stopOperating(true), this.hub.idleMs); this.idleTimer.unref?.();
  }
  stopOperating(notify) {
    clearTimeout(this.idleTimer); clearInterval(this.beatTimer);
    const was = this.operating;
    this.operating = false; this.sentVideos.clear();
    // 終わった接続は閉じる。同じ握手ハッシュへの古い署名を送り直して開き直すことをさせない。
    // アプリはつなぎ直し、新しい握手ハッシュに顔・指紋で署名し直す。
    if (was && notify) { this.send({ type: 'operate_expired' }); this.close(); }
  }
  fresh(message) {
    if (message.beat === undefined) return true;
    return Number.isInteger(message.beat) && this.beatN - message.beat < 2;
  }
  async onApp(message) {
    const type = message?.type;
    if (type === 'operate') {
      if (!ONLY(message, ['type', 'hashes', 'sig']) || (!this.can('answer') && !this.can('screen') && !this.can('input')) ||
          !verifyOperate(this.device.operate_pub, this.noise.hash, message.hashes, message.sig)) {
        this.send({ type: 'error', message: 'operate rejected' }); this.close(); return;
      }
      this.startOperating(); return;
    }
    if (type === 'shown') {
      await this.hub.shown(validateShown(message).ids); return;
    }
    if (type === 'operate_end') { this.stopOperating(true); return; }
    if (type !== 'answer' && type !== 'later') { this.close(); return; }
    if (!this.operating) { this.send({ type: 'error', sequence: message?.sequence, message: 'operate required' }); return; }
    if (!this.can('answer')) { this.send({ type: 'error', sequence: message?.sequence, message: 'permission denied' }); return; }
    if (!this.fresh(message)) { this.send({ type: 'error', sequence: message.sequence, message: 'stale beat' }); return; }
    const { beat, ...body } = message;
    this.touch();
    try {
      if (type === 'answer') {
        const response = await this.hub.answer(validateAnswer(body));
        this.send({ type: 'answer_result', sequence: body.sequence, ...response });
      } else if (ONLY(body, ['type', 'id']) && typeof body.id === 'string') await this.hub.later(body.id);
      else this.close();
    } catch (error) { this.send({ type: 'error', sequence: body?.sequence, message: error.message }); }
  }
}

export class MobileHub {
  constructor({ configDir, noiseKey, relay, transport, snapshot, answer, later, shown = async () => {}, videoPath = () => null, createdOf,
    now = Date.now, log = () => {}, beatMs = 5000, idleMs = 600_000, handshakeMs = 10_000 }) {
    Object.assign(this, { configDir, noiseKey, relay, room: relay.room, transport, snapshot, answer, later, shown, videoPath, createdOf, now, log, beatMs, idleMs, handshakeMs });
    this.roomBytes = Buffer.from(relay.room, 'base64url');
    this.sessions = new Map(); this.devices = []; this.pairing = null; this.failures = new Map();
  }
  async reloadDevices() {
    this.devices = await loadDevices(this.configDir);
    for (const session of [...this.sessions.values()]) {
      if (session.role !== 'app') continue;
      const current = this.devices.find(device => device.device === session.device?.device);
      if (!current || current.noise_pub !== session.device.noise_pub || current.operate_pub !== session.device.operate_pub) session.close();
      else session.device = current;
    }
  }
  // 握手の失敗は端末（またはペアリング）の ID ごとに数える。外した端末が繰り返しつないでも、ほかの端末は入れる。
  noteHandshakeFailure(id) {
    const key = b64u(id ?? Buffer.alloc(0));
    const list = this.failures.get(key) ?? [];
    list.push(this.now());
    this.failures.delete(key); this.failures.set(key, list);
    while (this.failures.size > FAILURE_IDS_KEPT) this.failures.delete(this.failures.keys().next().value);
  }
  tooManyFailures(id) {
    const key = b64u(id ?? Buffer.alloc(0));
    const limit = this.now() - HANDSHAKE_WINDOW_MS;
    const list = (this.failures.get(key) ?? []).filter(time => time > limit);
    if (list.length) this.failures.set(key, list); else this.failures.delete(key);
    return list.length > HANDSHAKE_FAILURES_PER_WINDOW;
  }
  onOpen(key, role, id) {
    if (this.tooManyFailures(id)) { this.transport.closeConn(key); return; }
    this.sessions.set(key, new Session(this, key, role, id));
  }
  onData(key, buffer) { void this.sessions.get(key)?.onData(buffer); }
  onClose(key) { this.sessions.get(key)?.close(false, false); }
  onReset() { for (const session of [...this.sessions.values()]) session.close(); }
  publish() { for (const session of this.sessions.values()) if (session.role === 'app' && session.ready) { void session.sendNotice(); if (session.operating) void session.sendSnapshot().catch(() => session.close()); } }
  clientCount() { let count = 0; for (const session of this.sessions.values()) if (session.role === 'app' && session.ready) count++; return count; }
  startPairing({ name, permissions }) {
    this.pairing?.close();
    this.pairing = new Pairing({ relay: this.relay, bridgePublic: this.noiseKey.publicKey, name, permissions, now: this.now });
    clearTimeout(this.pairingTimer);
    const pairing = this.pairing;
    this.pairingTimer = setTimeout(() => { if (this.pairing === pairing) this.closePairing(); }, Math.max(0, pairing.exp * 1000 - this.now()) + 50);
    this.pairingTimer.unref?.();
    return pairing;
  }
  appTag(device16) { return appTag(this.relay, device16); }
  // PC の端末で打たれた6桁を確かめ、合えば許可リストに入れる。
  async confirmPairing(code) {
    const pairing = this.pairing;
    const candidate = pairing?.confirm(code);
    if (!candidate) { if (pairing && !pairing.active) { this.closePairing(); } return null; }
    const device = { device: b64u(randomBytes(16)), noise_pub: b64u(candidate.noisePublic), operate_pub: candidate.operate, name: candidate.name,
      permissions: pairing.permissions, created: new Date(this.now()).toISOString() };
    await addDevice(this.configDir, device);
    await this.reloadDevices();
    const session = candidate.session;
    session.send({ type: 'paired', device: device.device, tag: this.appTag(Buffer.from(device.device, 'base64url')), name: pairing.name, permissions: pairing.permissions });
    session.paired = true;
    this.closePairing(session);
    const linger = setTimeout(() => session.close(), 10_000); linger.unref?.();
    return device;
  }
  closePairing(except) {
    const pairing = this.pairing; this.pairing = null; clearTimeout(this.pairingTimer);
    if (!pairing) return;
    for (const session of [...this.sessions.values()]) if (session.role === 'pair' && session !== except) session.close();
    pairing.close();
  }
  stop() { for (const session of [...this.sessions.values()]) session.close(); }
}
