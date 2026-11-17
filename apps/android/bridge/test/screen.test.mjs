// The screen client gets the owner's config with nesting allowed, and nothing else changes.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { nestedClientConfig } from '../screen.mjs';

test('allow_nested を [experimental] に、native_menus とサイドバーを隠す設定を [ui] に足す。前からある同じ行は置き換え、ほかの行は残す', () => {
  assert.equal(nestedClientConfig('prefix = "ctrl+t"\n[experimental]\nallow_nested = false\nfoo = 1\n[ui]\nnative_menus = false\nsidebar_start_collapsed = false\n[keys]\ntoggle_sidebar = "prefix+s"\n[other]\nallow = 2\n'),
    'prefix = "ctrl+t"\n[experimental]\nallow_nested = true\nfoo = 1\n[ui]\nsidebar_collapsed_mode = "hidden"\nsidebar_start_collapsed = true\nnative_menus = true\n[keys]\ntoggle_sidebar = ["prefix+s", "f12"]\n[other]\nallow = 2\n\n');
});

test('サイドバーの出し入れに F12 を足す。持ち主の割り当ては残し、無ければ既定の prefix+b を残す', () => {
  assert.match(nestedClientConfig(''), /\[keys\]\ntoggle_sidebar = \["prefix\+b", "f12"\]\n/);
  assert.match(nestedClientConfig('[keys]\ntoggle_sidebar = ["ctrl+b", "f12"] # mine\n'), /toggle_sidebar = \["ctrl\+b", "f12"\]\n/);
  assert.match(nestedClientConfig('[keys]\ntoggle_sidebar = ""\n'), /toggle_sidebar = \["f12"\]\n/);
});

test('表が無ければ最後に足す。空の設定でも足す', () => {
  const out = nestedClientConfig('prefix = "ctrl+t"\n');
  assert.match(out, /^prefix = "ctrl\+t"\n/);
  assert.match(out, /\[experimental\]\nallow_nested = true\n/);
  assert.match(out, /\[ui\]\n(.*\n)*native_menus = true\n/);
  assert.match(nestedClientConfig(''), /\[ui\]\nsidebar_collapsed_mode = "hidden"\nsidebar_start_collapsed = true\nnative_menus = true\n/);
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
