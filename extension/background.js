import { fillAndSubmit } from './page-action.js';
import { loadSettings, validateRelayUrl } from './settings.js';

const TARGET_ORIGIN = 'https://taoli.tools';
const HEARTBEAT_MS = 20_000;
const MAX_RECONNECT_MS = 30_000;

let socket = null;
let reconnectTimer = null;
let heartbeatTimer = null;
let reconnectAttempt = 0;
let generation = 0;
let authRejected = false;

let status = {
  state: 'unconfigured',
  relayUrl: '',
  message: '请先配置插件连接 Token',
  lastConnectedAt: null
};

function publishStatus(next) {
  status = { ...status, ...next };
  chrome.runtime.sendMessage({ type: 'status_changed', status }).catch(() => {});
}

function clearTimers() {
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (heartbeatTimer !== null) {
    clearInterval(heartbeatTimer);
    heartbeatTimer = null;
  }
}

function closeSocket() {
  if (socket) {
    const current = socket;
    socket = null;
    try {
      current.close(1000, 'reconnecting');
    } catch {
      // The socket may still be in CONNECTING state.
    }
  }
}

async function restartConnection() {
  generation += 1;
  const currentGeneration = generation;
  clearTimers();
  closeSocket();
  reconnectAttempt = 0;
  authRejected = false;

  const settings = await loadSettings();
  if (currentGeneration !== generation) return;

  const validation = validateRelayUrl(settings.relayUrl);
  if (!validation.ok) {
    publishStatus({ state: 'unconfigured', relayUrl: settings.relayUrl, message: validation.message });
    return;
  }
  if (typeof settings.token !== 'string' || settings.token.trim().length < 32) {
    publishStatus({ state: 'unconfigured', relayUrl: validation.url, message: '请配置服务生成的插件连接 Token' });
    return;
  }

  openConnection(currentGeneration, validation.url, settings.token.trim());
}

function openConnection(currentGeneration, relayUrl, token) {
  if (currentGeneration !== generation) return;

  publishStatus({ state: 'connecting', relayUrl, message: '正在连接本机服务' });
  const ws = new WebSocket(relayUrl);
  socket = ws;

  ws.addEventListener('open', () => {
    if (currentGeneration !== generation || socket !== ws) return;
    ws.send(JSON.stringify({ type: 'auth', token }));
  });

  ws.addEventListener('message', (event) => {
    if (currentGeneration !== generation || socket !== ws) return;
    let message;
    try {
      message = JSON.parse(event.data);
    } catch {
      return;
    }

    if (message.type === 'auth_result') {
      if (!message.ok) {
        authRejected = true;
        publishStatus({ state: 'auth_error', relayUrl, message: '插件 Token 验证失败' });
        ws.close(1008, 'authentication failed');
        return;
      }
      reconnectAttempt = 0;
      publishStatus({
        state: 'connected',
        relayUrl,
        message: '已连接',
        lastConnectedAt: new Date().toISOString()
      });
      heartbeatTimer = setInterval(() => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'ping' }));
        }
      }, HEARTBEAT_MS);
      return;
    }

    if (message.type === 'trigger') {
      void handleTrigger(message).then((result) => {
        if (currentGeneration === generation && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'result', id: message.id, ...result }));
        }
      });
    }
  });

  ws.addEventListener('error', () => {
    if (currentGeneration === generation && socket === ws && !authRejected) {
      publishStatus({ state: 'disconnected', relayUrl, message: '无法连接本机服务' });
    }
  });

  ws.addEventListener('close', () => {
    if (currentGeneration !== generation || socket !== ws) return;
    socket = null;
    if (heartbeatTimer !== null) {
      clearInterval(heartbeatTimer);
      heartbeatTimer = null;
    }
    if (authRejected) return;
    publishStatus({ state: 'disconnected', relayUrl, message: '连接已断开，正在重试' });
    scheduleReconnect(currentGeneration, relayUrl, token);
  });
}

function scheduleReconnect(currentGeneration, relayUrl, token) {
  if (currentGeneration !== generation || reconnectTimer !== null) return;
  const delay = Math.min(1000 * (2 ** reconnectAttempt), MAX_RECONNECT_MS);
  reconnectAttempt += 1;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    openConnection(currentGeneration, relayUrl, token);
  }, delay);
}

export function isTargetUrl(value) {
  try {
    const url = new URL(value);
    return url.origin === TARGET_ORIGIN;
  } catch {
    return false;
  }
}

async function handleTrigger(message) {
  if (
    typeof message.id !== 'string'
    || typeof message.symbol !== 'string'
    || message.symbol.length === 0
    || (message.addPair !== undefined && typeof message.addPair !== 'boolean')
  ) {
    return { ok: false, code: 'INVALID_MESSAGE', message: '触发消息格式无效' };
  }

  try {
    const tabs = await chrome.tabs.query({ active: true, lastFocusedWindow: true });
    const tab = tabs[0];
    if (!tab || typeof tab.id !== 'number' || !isTargetUrl(tab.url)) {
      return { ok: false, code: 'NO_TARGET_TAB', message: '当前活动标签页不是 taoli.tools' };
    }

    const injectionResults = await chrome.scripting.executeScript({
      target: { tabId: tab.id, frameIds: [0] },
      world: 'MAIN',
      func: fillAndSubmit,
      args: [message.symbol, message.addPair === true]
    });
    const result = injectionResults?.[0]?.result;
    if (!result || typeof result.ok !== 'boolean') {
      return { ok: false, code: 'EXECUTION_ERROR', message: '页面脚本没有返回结果' };
    }
    return result;
  } catch (error) {
    return {
      ok: false,
      code: 'EXECUTION_ERROR',
      message: error instanceof Error ? error.message : '无法执行页面脚本'
    };
  }
}

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  if (message?.type === 'get_status') {
    sendResponse(status);
    return false;
  }
  if (message?.type === 'reconnect') {
    void restartConnection().then(() => sendResponse({ ok: true }));
    return true;
  }
  return false;
});

chrome.runtime.onStartup.addListener(() => void restartConnection());
chrome.runtime.onInstalled.addListener(() => void restartConnection());

void restartConnection();
