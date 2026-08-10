import { loadSettings, validateRelayUrl } from './settings.js';

const form = document.querySelector('#settings-form');
const relayUrlInput = document.querySelector('#relay-url');
const tokenInput = document.querySelector('#token');
const formStatus = document.querySelector('#form-status');
const toggleToken = document.querySelector('#toggle-token');

function showStatus(message) {
  formStatus.textContent = message;
}

toggleToken.addEventListener('click', () => {
  const show = tokenInput.type === 'password';
  tokenInput.type = show ? 'text' : 'password';
  toggleToken.textContent = show ? '隐藏' : '显示';
});

form.addEventListener('submit', async (event) => {
  event.preventDefault();
  const validation = validateRelayUrl(relayUrlInput.value.trim());
  if (!validation.ok) {
    showStatus(validation.message);
    relayUrlInput.focus();
    return;
  }
  const token = tokenInput.value.trim();
  if (token.length < 32) {
    showStatus('Token 至少需要 32 个字符');
    tokenInput.focus();
    return;
  }

  await chrome.storage.local.set({ relayUrl: validation.url, token });
  showStatus('设置已保存，插件正在重新连接');
});

const settings = await loadSettings();
relayUrlInput.value = settings.relayUrl;
tokenInput.value = settings.token;
