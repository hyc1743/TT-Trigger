export const DEFAULT_LOCAL_RELAY_URL = 'ws://127.0.0.1:8787/extension';
export const DEFAULT_CLOUD_RELAY_URL = '';

export function validateLocalRelayUrl(value) {
  let url;
  try { url = new URL(value); } catch { return { ok: false, message: '请输入有效的本地 WebSocket 地址' }; }
  const loopback = url.hostname === '127.0.0.1' || url.hostname === 'localhost';
  if (url.protocol !== 'ws:' || !loopback || url.pathname !== '/extension' || url.username || url.password || url.search || url.hash) {
    return { ok: false, message: '本地地址必须为 ws://127.0.0.1 或 ws://localhost 的 /extension' };
  }
  return { ok: true, url: url.toString() };
}

// Backward-compatible export used by older tests and integrations.
export const DEFAULT_RELAY_URL = DEFAULT_LOCAL_RELAY_URL;
export const validateRelayUrl = validateLocalRelayUrl;

export function validateCloudRelayUrl(value) {
  let url;
  try { url = new URL(value); } catch { return { ok: false, message: '请输入有效的 HTTPS 中继地址' }; }
  if (url.protocol !== 'https:' || !url.hostname || url.pathname !== '/' || url.username || url.password || url.search || url.hash) {
    return { ok: false, message: '云端中继必须是无路径、账号和参数的 https:// 地址' };
  }
  return { ok: true, url: url.toString().replace(/\/$/, '') };
}

export async function loadSettings() {
  const stored = await chrome.storage.local.get({
    mode: 'local',
    relayUrl: DEFAULT_LOCAL_RELAY_URL,
    token: '',
    cloud: null
  });
  return {
    mode: stored.mode === 'cloud_e2ee' ? 'cloud_e2ee' : 'local',
    relayUrl: stored.relayUrl || DEFAULT_LOCAL_RELAY_URL,
    token: typeof stored.token === 'string' ? stored.token : '',
    cloud: stored.cloud && typeof stored.cloud === 'object' ? stored.cloud : null
  };
}

export async function saveCloud(cloud) {
  await chrome.storage.local.set({ cloud });
}
