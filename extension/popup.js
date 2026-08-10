const labels = {
  connected: '已连接',
  connecting: '正在连接',
  disconnected: '连接断开',
  auth_error: 'Token 错误',
  unconfigured: '尚未配置'
};

const indicator = document.querySelector('#status-indicator');
const statusLabel = document.querySelector('#status-label');
const statusMessage = document.querySelector('#status-message');
const relayUrl = document.querySelector('#relay-url');
const reconnectButton = document.querySelector('#reconnect');

function render(status) {
  statusLabel.textContent = labels[status.state] ?? '状态未知';
  statusMessage.textContent = status.message ?? '';
  relayUrl.textContent = status.relayUrl || '未设置';
  relayUrl.title = status.relayUrl || '';
  indicator.classList.toggle('connected', status.state === 'connected');
}

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === 'status_changed') render(message.status);
});

document.querySelector('#open-options').addEventListener('click', () => {
  chrome.runtime.openOptionsPage();
});

reconnectButton.addEventListener('click', async () => {
  reconnectButton.disabled = true;
  await chrome.runtime.sendMessage({ type: 'reconnect' }).catch(() => {});
  setTimeout(() => { reconnectButton.disabled = false; }, 500);
});

chrome.runtime.sendMessage({ type: 'get_status' }).then(render).catch(() => {
  render({ state: 'disconnected', message: '后台服务未响应', relayUrl: '' });
});
