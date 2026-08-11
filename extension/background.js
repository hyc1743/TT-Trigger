import { fillAndSubmit } from './page-action.js';
import { encryptResponse, verifyAndDecryptRequest } from './e2ee.js';
import { loadSettings, validateCloudRelayUrl, validateLocalRelayUrl } from './settings.js';

const TARGET_ORIGIN = 'https://taoli.tools';
const HEARTBEAT_MS = 20_000;
const MAX_RECONNECT_MS = 30_000;
const IDEMPOTENCY_MS = 10 * 60_000;

let socket = null;
let reconnectTimer = null;
let heartbeatTimer = null;
let reconnectAttempt = 0;
let generation = 0;
let authRejected = false;
let runtimeCache = { nonces: {}, responses: {} };

let status = {
  state: 'unconfigured', mode: 'local', relayUrl: '', message: '请先配置连接方式', lastConnectedAt: null
};

function publishStatus(next) {
  status = { ...status, ...next };
  chrome.runtime.sendMessage({ type: 'status_changed', status }).catch(() => {});
}

function clearConnection() {
  if (reconnectTimer !== null) clearTimeout(reconnectTimer);
  if (heartbeatTimer !== null) clearInterval(heartbeatTimer);
  reconnectTimer = null;
  heartbeatTimer = null;
  if (socket) {
    const current = socket;
    socket = null;
    try { current.close(1000, 'reconnecting'); } catch { /* already closed */ }
  }
}

function cloudSocketUrl(relayUrl, deviceId) {
  const url = new URL(relayUrl);
  url.protocol = 'wss:';
  url.pathname = `/v1/devices/${encodeURIComponent(deviceId)}/extension`;
  return url.toString();
}

async function restartConnection() {
  generation += 1;
  const currentGeneration = generation;
  clearConnection();
  reconnectAttempt = 0;
  authRejected = false;
  const settings = await loadSettings();
  if (currentGeneration !== generation) return;

  if (settings.mode === 'cloud_e2ee') {
    const cloud = settings.cloud;
    const validation = validateCloudRelayUrl(cloud?.relayUrl || '');
    if (!validation.ok || cloud?.registered !== true || !cloud?.deviceId || !cloud?.deviceToken || !cloud?.deviceGrant) {
      publishStatus({ state: 'unconfigured', mode: 'cloud_e2ee', relayUrl: cloud?.relayUrl || '', message: validation.ok ? '请先注册云端设备' : validation.message });
      return;
    }
    openConnection(currentGeneration, {
      mode: 'cloud_e2ee', relayUrl: validation.url,
      wsUrl: cloudSocketUrl(validation.url, cloud.deviceId),
      auth: { token: cloud.deviceToken, grant: cloud.deviceGrant }, cloud
    });
    return;
  }

  const validation = validateLocalRelayUrl(settings.relayUrl);
  if (!validation.ok || settings.token.trim().length < 32) {
    publishStatus({ state: 'unconfigured', mode: 'local', relayUrl: settings.relayUrl, message: validation.ok ? '请配置服务生成的插件连接 Token' : validation.message });
    return;
  }
  openConnection(currentGeneration, {
    mode: 'local', relayUrl: validation.url, wsUrl: validation.url,
    auth: { token: settings.token.trim() }
  });
}

function openConnection(currentGeneration, config) {
  if (currentGeneration !== generation) return;
  publishStatus({ state: 'connecting', mode: config.mode, relayUrl: config.relayUrl, message: config.mode === 'local' ? '正在连接本机服务' : '正在连接云端 E2EE 中继' });
  const ws = new WebSocket(config.wsUrl);
  socket = ws;
  ws.addEventListener('open', () => {
    if (currentGeneration !== generation || socket !== ws) return;
    ws.send(JSON.stringify({ type: 'auth', ...config.auth }));
  });
  ws.addEventListener('message', (event) => void onSocketMessage(event, ws, currentGeneration, config));
  ws.addEventListener('error', () => {
    if (currentGeneration === generation && socket === ws && !authRejected) publishStatus({ state: 'disconnected', message: '无法连接中继服务' });
  });
  ws.addEventListener('close', () => {
    if (currentGeneration !== generation || socket !== ws) return;
    socket = null;
    if (heartbeatTimer !== null) clearInterval(heartbeatTimer);
    heartbeatTimer = null;
    if (authRejected) return;
    publishStatus({ state: 'disconnected', message: '连接已断开，正在重试' });
    scheduleReconnect(currentGeneration, config);
  });
}

async function onSocketMessage(event, ws, currentGeneration, config) {
  if (currentGeneration !== generation || socket !== ws) return;
  let message;
  try { message = JSON.parse(event.data); } catch { return; }
  if (message.type === 'auth_result') {
    if (!message.ok) {
      authRejected = true;
      publishStatus({ state: 'auth_error', message: config.mode === 'local' ? '插件 Token 验证失败' : '云端设备凭据验证失败' });
      ws.close(1008, 'authentication failed');
      return;
    }
    reconnectAttempt = 0;
    publishStatus({ state: 'connected', message: '已连接', lastConnectedAt: new Date().toISOString() });
    heartbeatTimer = setInterval(() => {
      if (ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'ping' }));
    }, HEARTBEAT_MS);
    return;
  }
  if (message.type !== 'trigger') return;
  const response = config.mode === 'cloud_e2ee'
    ? await handleCloudTrigger(message, config.cloud)
    : await handleLocalTrigger(message);
  if (currentGeneration === generation && ws.readyState === WebSocket.OPEN) {
    if (config.mode === 'cloud_e2ee') ws.send(JSON.stringify({ type: 'result', id: message.id, response }));
    else ws.send(JSON.stringify({ type: 'result', id: message.id, ...response }));
  }
}

function scheduleReconnect(currentGeneration, config) {
  if (currentGeneration !== generation || reconnectTimer !== null) return;
  const delay = Math.min(1000 * (2 ** reconnectAttempt), MAX_RECONNECT_MS);
  reconnectAttempt += 1;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    openConnection(currentGeneration, config);
  }, delay);
}

export function isTargetUrl(value) {
  try { return new URL(value).origin === TARGET_ORIGIN; } catch { return false; }
}

async function executePage(symbol, addPair) {
  try {
    const tabs = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
    const tab = tabs[0];
    if (!tab || typeof tab.id !== 'number' || !isTargetUrl(tab.url)) return { ok: false, code: 'NO_TARGET_TAB', message: '当前活动标签页不是 taoli.tools' };
    const injectionResults = await chrome.scripting.executeScript({
      target: { tabId: tab.id, frameIds: [0] }, world: 'MAIN', func: fillAndSubmit, args: [symbol, addPair === true]
    });
    const result = injectionResults?.[0]?.result;
    return result && typeof result.ok === 'boolean' ? result : { ok: false, code: 'EXECUTION_ERROR', message: '页面脚本没有返回结果' };
  } catch (error) {
    return { ok: false, code: 'EXECUTION_ERROR', message: error instanceof Error ? error.message : '无法执行页面脚本' };
  }
}

async function handleLocalTrigger(message) {
  if (typeof message.id !== 'string' || typeof message.symbol !== 'string' || message.symbol.length === 0 || (message.addPair !== undefined && typeof message.addPair !== 'boolean')) {
    return { ok: false, code: 'INVALID_MESSAGE', message: '触发消息格式无效' };
  }
  return executePage(message.symbol, message.addPair === true);
}

async function loadRuntimeCache() {
  if (!chrome.storage.session) return runtimeCache;
  const stored = await chrome.storage.session.get({ cloudRuntimeCache: runtimeCache });
  runtimeCache = stored.cloudRuntimeCache || runtimeCache;
  return runtimeCache;
}

async function saveRuntimeCache() {
  if (chrome.storage.session) await chrome.storage.session.set({ cloudRuntimeCache: runtimeCache });
}

function cleanRuntime(now) {
  for (const [key, value] of Object.entries(runtimeCache.nonces)) if (value <= now) delete runtimeCache.nonces[key];
  for (const [key, value] of Object.entries(runtimeCache.responses)) if (value.expiresAt <= now) delete runtimeCache.responses[key];
}

async function handleCloudTrigger(message, cloud) {
  const headers = message?.headers && typeof message.headers === 'object' ? message.headers : {};
  const keyId = headers['x-tt-key-id'];
  const client = Array.isArray(cloud.clients) ? cloud.clients.find((item) => item.keyId === keyId) : null;
  if (!client || typeof message.body !== 'string') return { headers: {}, body: JSON.stringify({ ok: false, code: 'UNKNOWN_CLIENT' }) };
  let requestId = typeof message.id === 'string' ? message.id : '';
  try {
    const verified = await verifyAndDecryptRequest({ deviceId: cloud.deviceId, client, headers, rawBody: message.body });
    requestId = verified.requestId;
    const now = Date.now();
    await loadRuntimeCache();
    cleanRuntime(now);
    const cacheKey = `${client.keyId}:${requestId}`;
    const cached = runtimeCache.responses[cacheKey];
    if (cached) {
      if (cached.fingerprint === verified.fingerprint) return cached.response;
      return encryptResponse({ deviceId: cloud.deviceId, client, requestId, result: { ok: false, code: 'REQUEST_ID_CONFLICT', message: 'requestId 已用于其他请求' } });
    }
    const nonceKey = `${client.keyId}:${verified.nonce}`;
    if (runtimeCache.nonces[nonceKey]) {
      return encryptResponse({ deviceId: cloud.deviceId, client, requestId, result: { ok: false, code: 'REPLAYED_REQUEST', message: '请求已被使用' } });
    }
    runtimeCache.nonces[nonceKey] = now + 120_000;
    const payload = verified.payload;
    let result;
    if (!payload || typeof payload.symbol !== 'string' || [...payload.symbol].length < 1 || [...payload.symbol].length > 256 || (payload.addPair !== undefined && typeof payload.addPair !== 'boolean')) {
      result = { ok: false, code: 'INVALID_PAYLOAD', message: 'symbol 必须包含 1 到 256 个字符' };
    } else {
      result = await executePage(payload.symbol, payload.addPair === true);
    }
    const response = await encryptResponse({ deviceId: cloud.deviceId, client, requestId, result });
    runtimeCache.responses[cacheKey] = { fingerprint: verified.fingerprint, response, expiresAt: now + IDEMPOTENCY_MS };
    await saveRuntimeCache();
    return response;
  } catch (error) {
    const code = typeof error?.code === 'string' ? error.code : 'INVALID_REQUEST';
    return encryptResponse({ deviceId: cloud.deviceId, client, requestId, result: { ok: false, code, message: 'E2EE 请求验证失败' } });
  }
}

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (message?.type === 'get_status') { sendResponse(status); return false; }
  if (message?.type === 'reconnect') { void restartConnection().then(() => sendResponse({ ok: true })); return true; }
  return false;
});

chrome.runtime.onStartup.addListener(() => void restartConnection());
chrome.runtime.onInstalled.addListener(() => void restartConnection());
void restartConnection();
