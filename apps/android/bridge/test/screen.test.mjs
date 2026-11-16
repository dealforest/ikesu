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
