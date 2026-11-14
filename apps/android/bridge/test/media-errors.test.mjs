// 画像・動画を読めなかったわけを、Mac のパネルと同じ文で Android へ渡すことを確かめる。
import test from 'node:test';
import assert from 'node:assert/strict';
import { DecisionRegistry } from '../registry.mjs';

test('読めなかった画像・動画のわけを判断待ちの media_errors に載せる', async () => {
  let notified = 0;
  const self = { notify: () => { notified++; } };
  const entry = { media: { images: [], cards: [], videos: [], errors: [] }, mediaAbort: new AbortController() };
  await DecisionRegistry.prototype.recordMedia.call(self, entry, { status: 'rich_error', message: '画像を表示できません: ENOENT' });
  await DecisionRegistry.prototype.recordMedia.call(self, entry, { status: 'rich_error', source: '/a.mp4', message: '動画を表示できません: SSH exit 1' });
  assert.deepEqual(entry.media.errors, ['画像を表示できません: ENOENT', '動画を表示できません: SSH exit 1']);
  assert.equal(notified, 2);
  await DecisionRegistry.prototype.recordMedia.call(self, entry, { status: 'rich_error', message: '画像を表示できません: ENOENT' });
  assert.equal(entry.media.errors.length, 2, '同じわけは1回だけ');
  for (const n of [3, 4, 5, 6]) await DecisionRegistry.prototype.recordMedia.call(self, entry, { status: 'rich_error', message: `わけ ${n}` });
  assert.deepEqual(entry.media.errors.slice(2), ['わけ 3', 'わけ 4'], '4件まで');
  entry.mediaAbort.abort();
  await DecisionRegistry.prototype.recordMedia.call(self, entry, { status: 'rich_error', message: '取り消し後' });
  assert.equal(entry.media.errors.length, 4);
});
