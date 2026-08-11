import { randomBase64url } from './e2ee.js';
import { DEFAULT_CLOUD_RELAY_URL, loadSettings, saveCloud, validateCloudRelayUrl } from './settings.js';

const $ = (selector) => document.querySelector(selector);
const labels = { connected: '已连接', connecting: '正在连接', disconnected: '连接断开', auth_error: '凭据错误', unconfigured: '尚未配置' };
let settings;

function renderStatus(status) {
  const label = labels[status.state] ?? '状态未知';
  $('#status-label').textContent = label;
  const detail = status.message || '';
  $('#status-message').textContent = detail === label || status.state === 'connected' ? '' : detail;
  $('#status-message').hidden = !$('#status-message').textContent;
  $('#relay-url').textContent = status.relayUrl || '未设置';
  $('#relay-url').title = status.relayUrl || '';
  $('#status-indicator').classList.toggle('connected', status.state === 'connected');
  if (status.state === 'connected') document.querySelectorAll('.form-status').forEach((node) => { node.textContent = ''; });
}

function renderMode() {
  const cloudMode = settings.mode === 'cloud_e2ee';
  $('#mode-local').classList.toggle('active', !cloudMode);
  $('#mode-cloud').classList.toggle('active', cloudMode);
  $('#local-form').hidden = cloudMode;
  $('#cloud-panel').hidden = !cloudMode;
  const configured = settings.cloud?.registered === true;
  $('#cloud-register-form').hidden = configured;
  $('#cloud-device').hidden = !configured;
  $('#cloud-relay').value = settings.cloud?.relayUrl || DEFAULT_CLOUD_RELAY_URL;
  $('#device-id').textContent = settings.cloud?.deviceId || '—';
  renderClients();
}

function renderClients() {
  const list = $('#client-list');
  list.replaceChildren();
  for (const client of settings.cloud?.clients || []) {
    const row = document.createElement('div');
    row.className = 'client-item';
    const name = document.createElement('span');
    name.className = 'client-name';
    name.textContent = client.name || client.keyId;
    name.title = client.keyId;
    row.append(name, clientButton('导出', () => downloadClient(client)), clientButton('轮换', () => createClient(`${client.name || '调用方'}（轮换）`)), clientButton('吊销', () => revokeClient(client)));
    list.append(row);
  }
}

function clientButton(text, handler) {
  const button = document.createElement('button');
  button.type = 'button';
  button.textContent = text;
  button.addEventListener('click', async () => {
    button.disabled = true;
    try { await handler(); } catch (error) { $('#cloud-status').textContent = error.message; }
    finally { button.disabled = false; }
  });
  return button;
}

async function reconnect() {
  await chrome.runtime.sendMessage({ type: 'reconnect' }).catch(() => {});
}

async function chooseMode(mode) {
  settings.mode = mode;
  await chrome.storage.local.set({ mode });
  renderMode();
  await reconnect();
}

async function ensureOriginPermission(relayUrl) {
  const origin = `${new URL(relayUrl).origin}/*`;
  if (await chrome.permissions.contains({ origins: [origin] })) return true;
  return chrome.permissions.request({ origins: [origin] });
}

async function relayFetch(path, options = {}) {
  const relay = settings.cloud.relayUrl.replace(/\/$/, '');
  const response = await fetch(`${relay}${path}`, options);
  let value;
  try { value = await response.json(); } catch { value = { message: `HTTP ${response.status}` }; }
  if (!response.ok) throw new Error(value.message || value.code || `HTTP ${response.status}`);
  return value;
}

async function createClient(name) {
  if (!name.trim()) throw new Error('请输入调用方名称');
  const keyId = randomBase64url(16);
  const clientToken = randomBase64url(32);
  const secret = randomBase64url(32);
  const value = await relayFetch(`/v1/devices/${encodeURIComponent(settings.cloud.deviceId)}/clients`, {
    method: 'POST',
    headers: {
      authorization: `Bearer ${settings.cloud.deviceToken}`,
      'x-tt-device-grant': settings.cloud.deviceGrant,
      'content-type': 'application/json'
    },
    body: JSON.stringify({ keyId, clientToken })
  });
  const client = { name: name.trim(), keyId, relayToken: clientToken, relayGrant: value.relayGrant, secret, createdAt: new Date().toISOString() };
  settings.cloud.clients = [...(settings.cloud.clients || []), client];
  await saveCloud(settings.cloud);
  renderClients();
  downloadClient(client);
  $('#cloud-status').textContent = '调用方已创建并导出';
}

function downloadClient(client) {
  const config = {
    version: 1,
    mode: 'cloud_e2ee',
    relay_url: settings.cloud.relayUrl,
    device_id: settings.cloud.deviceId,
    key_id: client.keyId,
    relay_token: client.relayToken,
    relay_grant: client.relayGrant,
    secret: client.secret
  };
  const blob = new Blob([`${JSON.stringify(config, null, 2)}\n`], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = `tt-trigger-${(client.name || client.keyId).replace(/[^\p{L}\p{N}._-]+/gu, '-')}.json`;
  anchor.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

async function revokeClient(client) {
  await relayFetch(`/v1/devices/${encodeURIComponent(settings.cloud.deviceId)}/clients/${encodeURIComponent(client.keyId)}`, {
    method: 'DELETE',
    headers: { authorization: `Bearer ${settings.cloud.deviceToken}`, 'x-tt-device-grant': settings.cloud.deviceGrant }
  });
  settings.cloud.clients = settings.cloud.clients.filter((item) => item.keyId !== client.keyId);
  await saveCloud(settings.cloud);
  renderClients();
  $('#cloud-status').textContent = '调用方已吊销';
}

chrome.runtime.onMessage.addListener((message) => { if (message?.type === 'status_changed') renderStatus(message.status); });
$('#mode-local').addEventListener('click', () => void chooseMode('local'));
$('#mode-cloud').addEventListener('click', () => void chooseMode('cloud_e2ee'));
$('#toggle-token').addEventListener('click', () => {
  const input = $('#token');
  input.type = input.type === 'password' ? 'text' : 'password';
  $('#toggle-token').textContent = input.type === 'password' ? '显示' : '隐藏';
});
$('#local-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const token = $('#token').value.trim();
  if (token.length < 32) { $('#local-status').textContent = 'Token 至少需要 32 个字符'; return; }
  settings.token = token;
  settings.mode = 'local';
  await chrome.storage.local.set({ token, mode: 'local' });
  $('#local-status').textContent = '正在连接';
  await reconnect();
});
$('#cloud-register-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const validation = validateCloudRelayUrl($('#cloud-relay').value.trim());
  if (!validation.ok) { $('#cloud-status').textContent = validation.message; return; }
  if (!await ensureOriginPermission(validation.url)) { $('#cloud-status').textContent = '需要授予该中继地址的访问权限'; return; }
  $('#cloud-status').textContent = '正在注册';
  const pending = settings.cloud?.registered === false && settings.cloud.relayUrl === validation.url ? settings.cloud : null;
  const deviceId = pending?.deviceId || randomBase64url(16);
  const deviceToken = pending?.deviceToken || randomBase64url(32);
  const deviceGrant = pending?.deviceGrant || randomBase64url(32);
  settings.cloud = { relayUrl: validation.url, deviceId, deviceToken, deviceGrant, registered: false, clients: pending?.clients || [] };
  await saveCloud(settings.cloud);
  try {
    const value = await relayFetch('/v1/register', {
      method: 'POST', headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ deviceId, deviceToken, deviceGrant, activationCode: $('#activation-code').value.trim() })
    });
    settings.cloud.deviceGrant = value.deviceGrant;
    settings.cloud.registered = true;
    settings.mode = 'cloud_e2ee';
    await chrome.storage.local.set({ mode: 'cloud_e2ee', cloud: settings.cloud });
    $('#activation-code').value = '';
    renderMode();
    await reconnect();
  } catch (error) { $('#cloud-status').textContent = error.message; }
});
$('#client-form').addEventListener('submit', async (event) => {
  event.preventDefault();
  const input = $('#client-name');
  try { await createClient(input.value); input.value = ''; } catch (error) { $('#cloud-status').textContent = error.message; }
});
$('#reconnect').addEventListener('click', async (event) => {
  event.currentTarget.disabled = true;
  await reconnect();
  setTimeout(() => { event.currentTarget.disabled = false; }, 500);
});

settings = await loadSettings();
$('#token').value = settings.token;
renderMode();
chrome.runtime.sendMessage({ type: 'get_status' }).then(renderStatus).catch(() => renderStatus({ state: 'disconnected', message: '后台服务未响应', relayUrl: '' }));
