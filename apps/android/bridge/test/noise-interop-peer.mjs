// Interop peer for the Android noise-java test: an IK responder over TCP
// with 2-byte big-endian length framing. Prints "PORT <n> PUB <hex>" and,
// after the handshake, echoes each transport message back reversed.
import net from 'node:net';
import Noise from 'noise-handshake';
import Cipher from 'noise-handshake/cipher.js';

const prologue = Buffer.from(process.argv[2] || 'ikesu-mobile/1');
const responder = new Noise('IK', false);
responder.initialise(prologue);
const server = net.createServer(socket => {
  let buffer = Buffer.alloc(0);
  let rx = null; let tx = null;
  const sendFrame = data => { const head = Buffer.alloc(2); head.writeUInt16BE(data.length); socket.write(Buffer.concat([head, data])); };
  socket.on('data', chunk => {
    buffer = Buffer.concat([buffer, chunk]);
    while (buffer.length >= 2 && buffer.length >= 2 + buffer.readUInt16BE(0)) {
      const frame = buffer.subarray(2, 2 + buffer.readUInt16BE(0));
      buffer = buffer.subarray(2 + frame.length);
      try {
        if (!rx) {
          responder.recv(frame);
          sendFrame(responder.send());
          rx = new Cipher(responder.rx); tx = new Cipher(responder.tx); // rx decrypts what the initiator sent (README example)
          process.stdout.write(`HANDSHAKE ${Buffer.from(responder.hash).toString('hex')} REMOTE ${Buffer.from(responder.rs).toString('hex')}\n`);
        } else {
          const plain = rx.decrypt(frame);
          sendFrame(tx.encrypt(Buffer.from([...plain].reverse())));
        }
      } catch (error) { process.stdout.write(`REJECT ${error.message}\n`); socket.destroy(); }
    }
  });
});
server.listen(0, '127.0.0.1', () => process.stdout.write(`PORT ${server.address().port} PUB ${Buffer.from(responder.s.publicKey).toString('hex')}\n`));
