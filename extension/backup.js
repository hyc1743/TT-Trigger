import { base64urlDecode, base64urlEncode, randomBase64url } from './e2ee.js';

const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });
const AAD = encoder.encode('TT-Trigger device backup v1');
const ITERATIONS = 310_000;

async function deriveBackupKey(password, salt, usages) {
  if (typeof password !== 'string' || password.length < 12) throw new Error('备份密码至少需要12个字符');
  const material = await crypto.subtle.importKey('raw', encoder.encode(password), 'PBKDF2', false, ['deriveKey']);
  return crypto.subtle.deriveKey(
    { name: 'PBKDF2', hash: 'SHA-256', salt, iterations: ITERATIONS },
    material,
    { name: 'AES-GCM', length: 256 },
    false,
    usages
  );
}

export async function exportDeviceBackup(cloud, password) {
  if (!cloud || cloud.registered !== true || typeof cloud.deviceId !== 'string') throw new Error('没有可导出的云端设备配置');
  const saltText = randomBase64url(16);
  const ivText = randomBase64url(12);
  const key = await deriveBackupKey(password, base64urlDecode(saltText, 16), ['encrypt']);
  const ciphertext = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: base64urlDecode(ivText, 12), additionalData: AAD, tagLength: 128 },
    key,
    encoder.encode(JSON.stringify({ mode: 'cloud_e2ee', cloud }))
  );
  return JSON.stringify({
    version: 1,
    type: 'tt-trigger-device-backup',
    kdf: { name: 'PBKDF2-SHA256', iterations: ITERATIONS },
    salt: saltText,
    iv: ivText,
    ciphertext: base64urlEncode(new Uint8Array(ciphertext))
  }, null, 2) + '\n';
}

export async function importDeviceBackup(text, password) {
  if (typeof text !== 'string' || text.length > 10 * 1024 * 1024) throw new Error('设备备份文件过大');
  let envelope;
  try { envelope = JSON.parse(text); } catch { throw new Error('备份文件不是有效JSON'); }
  if (!envelope || envelope.version !== 1 || envelope.type !== 'tt-trigger-device-backup'
    || envelope.kdf?.name !== 'PBKDF2-SHA256' || envelope.kdf?.iterations !== ITERATIONS) {
    throw new Error('不支持的设备备份格式');
  }
  const key = await deriveBackupKey(password, base64urlDecode(envelope.salt, 16), ['decrypt']);
  let plaintext;
  try {
    plaintext = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: base64urlDecode(envelope.iv, 12), additionalData: AAD, tagLength: 128 },
      key,
      base64urlDecode(envelope.ciphertext)
    );
  } catch { throw new Error('备份密码错误或文件已损坏'); }
  let value;
  try { value = JSON.parse(decoder.decode(plaintext)); } catch { throw new Error('备份内容无效'); }
  const cloud = value?.cloud;
  if (value?.mode !== 'cloud_e2ee' || cloud?.registered !== true
    || !cloud.relayUrl || !cloud.deviceId || !cloud.deviceToken || !cloud.deviceGrant || !Array.isArray(cloud.clients)) {
    throw new Error('备份缺少设备凭据');
  }
  return { mode: 'cloud_e2ee', cloud };
}
