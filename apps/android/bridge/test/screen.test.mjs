// The screen client gets the owner's config with nesting allowed, and nothing else changes.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { nestedClientConfig } from '../screen.mjs';

test('allow_nested を [experimental] に足す。前からある allow_nested は置き換え、ほかの行は残す', () => {
  assert.equal(nestedClientConfig('prefix = "ctrl+t"\n[experimental]\nallow_nested = false\nfoo = 1\n[other]\nallow = 2\n'),
    'prefix = "ctrl+t"\n[experimental]\nallow_nested = true\nfoo = 1\n[other]\nallow = 2\n\n');
});

test('[experimental] が無ければ最後に足す。空の設定でも足す', () => {
  assert.match(nestedClientConfig('prefix = "ctrl+t"\n'), /^prefix = "ctrl\+t"\n[\s\S]*\[experimental\]\nallow_nested = true\n$/);
  assert.match(nestedClientConfig(''), /\[experimental\]\nallow_nested = true\n$/);
});

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { execFileSync } from 'node:child_process';
import { safeFileName, saveIncomingFile, shellQuote } from '../screen.mjs';

test('送られたファイル名からフォルダや制御文字を消す', () => {
  assert.equal(safeFileName('../../etc/passwd'), 'passwd');
  assert.equal(safeFileName('a\nb:c.png'), 'a_b_c.png');
  assert.equal(safeFileName('...hidden'), 'hidden');
  assert.equal(safeFileName(''), 'file');
});

test('ファイルは Downloads/ikesu-phone に 0600 で保存し、同じ名前でも上書きしない。パスはシェルで1語になる', () => {
  const home = fs.mkdtempSync(path.join(os.tmpdir(), 'ikesu-file-'));
  try {
    const now = new Date('2026-11-16T13:00:00Z');
    const a = saveIncomingFile("it's a photo.png", Buffer.from('one'), { home, now });
    const b = saveIncomingFile("it's a photo.png", Buffer.from('two'), { home, now });
    assert.notEqual(a, b);
    assert.equal(path.dirname(a), path.join(home, 'Downloads', 'ikesu-phone'));
    assert.equal(fs.readFileSync(a, 'utf8'), 'one');
    assert.equal(fs.statSync(a).mode & 0o777, 0o600);
    assert.equal(execFileSync('/bin/sh', ['-c', `printf %s ${shellQuote(b)}`]).toString(), b);
  } finally { fs.rmSync(home, { recursive: true, force: true }); }
});
