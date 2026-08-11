import { loadSettings } from './settings.js';

const labels = {
  connected: '已连接',
  connecting: '正在连接',
  disconnected: '连接断开',
  auth_error: '插件 Token 错误',
  unconfigured: '尚未配置'
};

const indicator = document.querySelector('#status-indicator');
const statusLabel = document.querySelector('#status-label');
const statusMessage = document.querySelector('#status-message');
const relayUrl = document.querySelector('#relay-url');
const reconnectButton = document.querySelector('#reconnect');
const tokenForm = document.querySelector('#token-form');
const tokenInput = document.querySelector('#token');
const toggleToken = document.querySelector('#toggle-token');
const saveStatus = document.querySelector('#save-status');

function render(status) {
  const label = labels[status.state] ?? '状态未知';
  const detail = status.message ?? '';
  const hideDetail = !detail || detail === label || status.state === 'connected';
  statusLabel.textContent = label;
  statusMessage.textContent = hideDetail ? '' : detail;
  statusMessage.hidden = hideDetail;
  relayUrl.textContent = status.relayUrl || '未设置';
  relayUrl.title = status.relayUrl || '';
  indicator.classList.toggle('connected', status.state === 'connected');
  if (status.state === 'connected' || status.state === 'auth_error' || status.state === 'disconnected') {
    saveStatus.textContent = '';
  }
}

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === 'status_changed') render(message.status);
});

toggleToken.addEventListener('click', () => {
  const show = tokenInput.type === 'password';
  tokenInput.type = show ? 'text' : 'password';
  toggleToken.textContent = show ? '隐藏' : '显示';
});

tokenForm.addEventListener('submit', async (event) => {
  event.preventDefault();
  const token = tokenInput.value.trim();
  if (token.length < 32) {
    saveStatus.textContent = '插件 Token 至少需要 32 个字符';
    tokenInput.focus();
    return;
  }
  await chrome.storage.local.set({ token });
  saveStatus.textContent = '已保存，正在连接';
  await chrome.runtime.sendMessage({ type: 'reconnect' }).catch(() => {});
});

reconnectButton.addEventListener('click', async () => {
  reconnectButton.disabled = true;
  await chrome.runtime.sendMessage({ type: 'reconnect' }).catch(() => {});
  setTimeout(() => { reconnectButton.disabled = false; }, 500);
});

chrome.runtime.sendMessage({ type: 'get_status' }).then(render).catch(() => {
  render({ state: 'disconnected', message: '后台服务未响应', relayUrl: '' });
});

loadSettings().then((settings) => {
  tokenInput.value = settings.token;
});
