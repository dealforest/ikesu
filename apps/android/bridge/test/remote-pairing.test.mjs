import assert from 'node:assert/strict';
import { mkdtemp, rm } from 'node:fs/promises';
import net from 'node:net';
import test from 'node:test';
import { remotePairing } from '../mobile-runtime.mjs';
import { startDisconnectingPairControl } from './mobile-harness.mjs';

test('B18 制御ソケットが切れた後の cancel は書き込まずに完了する', { timeout: 1000 }, async t => {
  const stateDir = await mkdtemp('/tmp/ikesu-remote-pair-');
  t.after(() => rm(stateDir, { recursive: true, force: true }));
  const requests = await startDisconnectingPairControl(t, stateDir, 'qr');
  const pairing = await remotePairing(stateDir);
  assert.ok(pairing);
  t.after(() => pairing.close());
  assert.equal(await pairing.start({ name: 'pair-test', permissions: ['notify'] }), 'qr');
  await assert.rejects(pairing.confirm('123456'), { message: '接続が切れました' });

  // 閉じたソケットへの write は相手側に届かないため、呼び出し自体を観測する。
  const write = t.mock.method(net.Socket.prototype, 'write');
  await pairing.cancel();
  assert.ok(!write.mock.calls.some(({ arguments: [data] }) => data === '{"op":"pair_cancel"}\n'));
  assert.deepEqual(requests.map(request => request.op), ['pair_start', 'pair_confirm']);
});
