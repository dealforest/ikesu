// ペアリング（PROTOCOL.md 3.2）と、握手ハッシュ h を使う検証（proof・6桁・操作の署名）。
import { createHmac, createPublicKey, randomBytes, timingSafeEqual, verify } from 'node:crypto';
import { b64u, fromB64u, PERMISSIONS } from './devices.mjs';
import { pairTag } from './relay-client.mjs';

export const PAIR_TTL_SECONDS = 300;
export const MAX_CANDIDATES = 4;
export const MAX_PROOF_FAILURES = 5;
export const MAX_CODE_FAILURES = 5;

const hmac = (secret, label, h) => createHmac('sha256', secret).update(label).update(h).digest();
export const pairProof = (secret, h) => b64u(hmac(secret, 'pair', h));
export const sasCode = (secret, h) => String(hmac(secret, 'sas', h).readUInt32BE(0) % 1_000_000).padStart(6, '0');

export function constantEqual(a, b) {
  const left = Buffer.from(String(a)), right = Buffer.from(String(b));
  const length = Math.max(left.length, right.length, 1);
  const x = Buffer.alloc(length), y = Buffer.alloc(length);
  left.copy(x); right.copy(y);
  return timingSafeEqual(x, y) && left.length === right.length;
}

export function proofMatches(secret, h, proof) {
  return typeof proof === 'string' && constantEqual(pairProof(secret, h), proof);
}

// 操作の署名: "ikesu-operate/1" ‖ h への ECDSA P-256/SHA-256（DER）。公開鍵は SPKI DER。
// h の長さは Noise の握手ハッシュの長さ（BLAKE2b なので 64 バイト）。
// 署名する文: "ikesu-operate/1" ‖ hashes の数（1バイト、1〜8）‖ 各 h の64バイト。自分の h が hashes に入っていることも確かめる。
export function verifyOperate(operatePubB64u, h, hashes, sigB64u) {
  try {
    if (!Array.isArray(hashes) || hashes.length < 1 || hashes.length > 8) return false;
    const list = hashes.map(item => fromB64u(item, h.length));
    let included = false;
    for (const item of list) if (timingSafeEqual(item, h)) included = true;
    if (!included) return false;
    const key = createPublicKey({ key: fromB64u(operatePubB64u), format: 'der', type: 'spki' });
    return verify('sha256', Buffer.concat([Buffer.from('ikesu-operate/1'), Buffer.from([list.length]), ...list]), { key, dsaEncoding: 'der' }, fromB64u(sigB64u));
  } catch { return false; }
}

export function validOperateKey(operatePubB64u) {
  try {
    const key = createPublicKey({ key: fromB64u(operatePubB64u), format: 'der', type: 'spki' });
    return key.asymmetricKeyType === 'ec' && key.asymmetricKeyDetails?.namedCurve === 'prime256v1';
  } catch { return false; }
}

export class Pairing {
  constructor({ relay, bridgePublic, name, permissions = PERMISSIONS, now = Date.now, ttl = PAIR_TTL_SECONDS }) {
    if (!permissions.every(item => PERMISSIONS.includes(item))) throw Error('権限の名前が不正です');
    this.now = now;
    this.relay = relay; this.bridgePublic = bridgePublic; this.name = name; this.permissions = permissions;
    this.id = randomBytes(16); this.secret = randomBytes(32);
    this.exp = Math.floor(now() / 1000) + ttl;
    this.proofFailures = 0; this.codeFailures = 0; this.candidates = []; this.closed = false;
    this.pending = new Set();
  }
  get active() { return !this.closed && this.now() / 1000 < this.exp && this.proofFailures < MAX_PROOF_FAILURES && this.codeFailures < MAX_CODE_FAILURES; }
  matches(id16) { return this.active && Buffer.isBuffer(id16) && id16.length === 16 && timingSafeEqual(id16, this.id); }
  qr() {
    return JSON.stringify({ v: 1, relay: this.relay.url, room: this.relay.room, bridge: b64u(this.bridgePublic), pairing: b64u(this.id), secret: b64u(this.secret), exp: this.exp,
      tag: pairTag(this.relay, this.id, this.exp), name: this.name });
  }
  // 握手に入る前に、候補の枠が残っているかを数える（握手中も含む）。
  reserve() {
    if (!this.active || this.candidates.length + this.pending.size >= MAX_CANDIDATES) return null;
    const slot = {}; this.pending.add(slot); return slot;
  }
  release(slot) { this.pending.delete(slot); }
  // proof が合えば候補にして6桁を返す。外れたら null（5回で QR を無効にする）。
  submitProof(slot, { h, proof, name, operate, noisePublic }) {
    if (!this.active) return null;
    if (!proofMatches(this.secret, h, proof)) { this.proofFailures++; return { error: 'proof' }; }
    if (typeof name !== 'string' || !name.trim() || name.length > 64 || !validOperateKey(operate)) return { error: 'format' };
    this.pending.delete(slot);
    const candidate = { code: sasCode(this.secret, h), name: name.trim(), operate, noisePublic, deliver: null };
    this.candidates.push(candidate);
    return { candidate };
  }
  dropCandidate(candidate) { this.candidates = this.candidates.filter(item => item !== candidate); }
  // 端末で打たれた6桁と合う候補を返す。合わなければ null（5回で無効）。
  confirm(code) {
    if (!this.active) return null;
    const text = String(code).trim();
    const hit = /^\d{6}$/.test(text) ? this.candidates.find(item => constantEqual(item.code, text)) : undefined;
    if (!hit) { this.codeFailures++; return null; }
    return hit;
  }
  close() { this.closed = true; this.secret.fill(0); this.candidates = []; }
}
