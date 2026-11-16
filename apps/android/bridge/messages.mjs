// スマホから届くメッセージの形の検査（中継の経路で使う）。
const ONLY = (object, keys) => object && typeof object === 'object' && !Array.isArray(object) && Object.keys(object).every(key => keys.includes(key));

export function validateAnswer(message) {
  if (!ONLY(message, ['type', 'id', 'options', 'text', 'images', 'sequence']) || message.type !== 'answer' ||
      typeof message.id !== 'string' || !message.id || typeof message.sequence !== 'string' || !message.sequence ||
      !Array.isArray(message.options) || !message.options.every(value => typeof value === 'string') ||
      typeof message.text !== 'string' || !Array.isArray(message.images) || message.images.length > 4 ||
      !message.images.every(image => typeof image === 'string' && image.length <= Math.ceil(20 * 1024 * 1024 / 3) * 4 && /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(image))) {
    throw Error('回答の形式が不正です');
  }
  return message;
}

export const MAX_SHOWN_IDS = 200;
export function validateShown(message) {
  if (!ONLY(message, ['type', 'ids']) || message.type !== 'shown' || !Array.isArray(message.ids) || message.ids.length > MAX_SHOWN_IDS ||
      !message.ids.every(value => typeof value === 'string' && value.length > 0 && value.length <= 128)) {
    throw Error('通知済みの形式が不正です');
  }
  return message;
}
