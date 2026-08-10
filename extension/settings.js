export const DEFAULT_RELAY_URL = 'ws://127.0.0.1:8787/extension';

export function validateRelayUrl(value) {
  let url;
  try {
    url = new URL(value);
  } catch {
    return { ok: false, message: '请输入有效的 WebSocket 地址' };
  }

  const isLoopback = url.hostname === '127.0.0.1' || url.hostname === 'localhost';
  if (url.protocol !== 'ws:' || !isLoopback) {
    return { ok: false, message: '地址必须使用 ws://127.0.0.1 或 ws://localhost' };
  }
  if (url.username || url.password || url.search || url.hash) {
    return { ok: false, message: '地址不能包含账号、查询参数或片段' };
  }
  if (url.pathname !== '/extension') {
    return { ok: false, message: '地址路径必须是 /extension' };
  }
  return { ok: true, url: url.toString() };
}

export async function loadSettings() {
  const stored = await chrome.storage.local.get({
    relayUrl: DEFAULT_RELAY_URL,
    token: ''
  });
  return {
    relayUrl: stored.relayUrl,
    token: stored.token
  };
}
