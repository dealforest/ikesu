// The bridge's relay connection: keepalive pings keep a healthy link, and a
// link that stops answering pings is dropped and reconnected.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { randomBytes } from 'node:crypto';
import { WebSocketServer } from 'ws';
import { RelayClient } from '../relay-client.mjs';
import { b64u } from '../devices.mjs';

async function relayServer({ autoPong }) {
  const server = new WebSocketServer({ port: 0, host: '127.0.0.1', autoPong });
  await new Promise(resolve => server.once('listening', resolve));
  const connections = [];
  server.on('connection', ws => connections.push(ws));
  const relay = { url: `ws://127.0.0.1:${server.address().port}`, room: b64u(randomBytes(32)), relayKey: randomBytes(32) };
  return { server, connections, relay };
}

const until = async (check, ms = 5000) => {
  const deadline = Date.now() + ms;
  while (!check()) { if (Date.now() > deadline) throw Error('timeout'); await new Promise(r => setTimeout(r, 10)); }
};

test('a relay that answers pings keeps one connection', async () => {
  const { server, connections, relay } = await relayServer({ autoPong: true });
  const logs = [];
  // 150ms: the other test files run at the same time, and a busy machine can take over 40ms to answer a local ping.
  const client = new RelayClient({ relay, onOpen() {}, onData() {}, onClose() {}, log: m => logs.push(m), pingMs: 150, minDelay: 20 });
  client.start();
  try {
    await new Promise(r => setTimeout(r, 650));
    assert.equal(connections.length, 1);
    assert.ok(!logs.some(m => m.includes('pong')), logs.join('\n'));
  } finally { client.stop(); server.close(); }
});

test('a relay that stops answering pings is dropped and reconnected', async () => {
  const { server, connections, relay } = await relayServer({ autoPong: false });
  const logs = [];
  const client = new RelayClient({ relay, onOpen() {}, onData() {}, onClose() {}, log: m => logs.push(m), pingMs: 40, minDelay: 20 });
  client.start();
  try {
    await until(() => connections.length >= 2);
    assert.ok(logs.some(m => m.includes('pong')), logs.join('\n'));
  } finally { client.stop(); server.close(); }
});
