// Noise IK（Noise_IK_25519_ChaChaPoly_BLAKE2b）の応答側と、平文の形（PROTOCOL.md 3・3.1）。
import Noise from 'noise-handshake';
import Cipher from 'noise-handshake/cipher.js';

export const MAX_PLAIN = 60000;
export const MAX_MESSAGE = 128 * 1024 * 1024;
export const REKEY_AFTER = 2 ** 31;
export const MAX_AGE_MS = 24 * 3600 * 1000;
const CHUNK = MAX_PLAIN - 6;

export const prologueFor = room => Buffer.concat([Buffer.from('ikesu-mobile/1'), room]);

export class NoiseResponder {
  constructor({ staticKey, room, now = Date.now }) {
    this.noise = new Noise('IK', false, { publicKey: staticKey.publicKey, secretKey: staticKey.secretKey });
    this.noise.initialise(prologueFor(room));
    this.now = now;
    this.started = now();
    this.sent = 0;
    this.tx = null; this.rx = null;
  }
  // 1通目を受ける。復号に失敗したら例外。
  receiveFirst(message) {
    const payload = this.noise.recv(message);
    if (payload.length !== 0) throw Error('握手の中身は空のはずです');
    return Buffer.from(this.noise.rs);
  }
  reply() {
    const message = this.noise.send();
    if (!this.noise.complete) throw Error('握手が終わっていません');
    // README の「rx for sending」は誤り。応答側は rx で復号し tx で暗号化する。
    this.rx = new Cipher(this.noise.rx); this.tx = new Cipher(this.noise.tx);
    this.hash = Buffer.from(this.noise.hash);
    return Buffer.from(message);
  }
  get remoteStatic() { return Buffer.from(this.noise.rs); }
  get needsRenewal() { return this.sent >= REKEY_AFTER || this.now() - this.started >= MAX_AGE_MS; }
  encrypt(plain) {
    if (plain.length > MAX_PLAIN) throw Error('平文が大きすぎます');
    this.sent++;
    return Buffer.from(this.tx.encrypt(plain));
  }
  decrypt(cipher) { return Buffer.from(this.rx.decrypt(cipher)); }
}

// JSON を、暗号化前の平文の列にする。
export function encodeMessage(value, nextId) {
  const body = Buffer.from(JSON.stringify(value), 'utf8');
  if (body.length + 1 <= MAX_PLAIN) return [Buffer.concat([Buffer.from([0x00]), body])];
  if (body.length > MAX_MESSAGE) throw Error('メッセージが 128 MB を超えています');
  const id = Buffer.alloc(4); id.writeUInt32BE(nextId() >>> 0);
  const parts = [];
  for (let offset = 0; offset < body.length; offset += CHUNK) {
    const last = offset + CHUNK >= body.length;
    parts.push(Buffer.concat([Buffer.from([0x01]), id, Buffer.from([last ? 1 : 0]), body.subarray(offset, offset + CHUNK)]));
  }
  return parts;
}

// 受けた平文を JSON にする。分割の途中なら undefined。形が不正なら例外。
export class Assembler {
  constructor() { this.parts = new Map(); }
  push(plain) {
    if (plain.length === 0) throw Error('空の平文です');
    if (plain[0] === 0x00) return JSON.parse(plain.subarray(1).toString('utf8'));
    if (plain[0] !== 0x01 || plain.length < 6) throw Error('平文の形が不正です');
    const id = plain.readUInt32BE(1);
    const last = plain[5];
    if (last > 1) throw Error('分割の印が不正です');
    let entry = this.parts.get(id);
    if (!entry) {
      if (this.parts.size >= 4) throw Error('分割中のメッセージが多すぎます');
      entry = { chunks: [], size: 0 }; this.parts.set(id, entry);
    }
    entry.size += plain.length - 6;
    if (entry.size > MAX_MESSAGE) throw Error('メッセージが 128 MB を超えています');
    entry.chunks.push(plain.subarray(6));
    if (!last) return undefined;
    this.parts.delete(id);
    return JSON.parse(Buffer.concat(entry.chunks).toString('utf8'));
  }
}
