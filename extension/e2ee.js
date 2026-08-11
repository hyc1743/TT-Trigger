const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });
const PREAMBLE = 'TT-TRIGGER-E2EE-V1';
const HKDF_SALT = encoder.encode('TT-Trigger E2EE v1');
const PADDED_BYTES = 2048;

export function base64urlEncode(value) {
  let binary = '';
  for (const byte of value) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '');
}

export function base64urlDecode(value, expectedLength) {
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]+$/.test(value)) throw protocolError('INVALID_ENCODING');
  const padded = value.replace(/-/g, '+').replace(/_/g, '/') + '='.repeat((4 - value.length % 4) % 4);
  let bytes;
  try { bytes = Uint8Array.from(atob(padded), (char) => char.charCodeAt(0)); } catch { throw protocolError('INVALID_ENCODING'); }
  if (expectedLength !== undefined && bytes.length !== expectedLength) throw protocolError('INVALID_ENCODING');
  return bytes;
}

export function randomBase64url(bytes = 32) {
  return base64urlEncode(crypto.getRandomValues(new Uint8Array(bytes)));
}

function protocolError(code, message = code) {
  const error = new Error(message);
  error.code = code;
  return error;
}

function hex(value) {
  return [...value].map((byte) => byte.toString(16).padStart(2, '0')).join('');
}

function unhex(value) {
  if (typeof value !== 'string' || !/^[0-9a-f]{64}$/.test(value)) throw protocolError('INVALID_SIGNATURE');
  return Uint8Array.from(value.match(/../g), (part) => Number.parseInt(part, 16));
}

function equal(a, b) {
  if (a.length !== b.length) return false;
  let result = 0;
  for (let i = 0; i < a.length; i += 1) result |= a[i] ^ b[i];
  return result === 0;
}

async function sha256(value) {
  return new Uint8Array(await crypto.subtle.digest('SHA-256', value));
}

async function derive(secretText) {
  const secret = base64urlDecode(secretText, 32);
  const material = await crypto.subtle.importKey('raw', secret, 'HKDF', false, ['deriveKey']);
  const deriveKey = (info, algorithm, usages) => crypto.subtle.deriveKey(
    { name: 'HKDF', hash: 'SHA-256', salt: HKDF_SALT, info: encoder.encode(info) },
    material,
    algorithm,
    false,
    usages
  );
  return {
    requestEncryption: await deriveKey('request-encryption', { name: 'AES-GCM', length: 256 }, ['encrypt', 'decrypt']),
    requestHmac: await deriveKey('request-hmac', { name: 'HMAC', hash: 'SHA-256', length: 256 }, ['sign', 'verify']),
    responseEncryption: await deriveKey('response-encryption', { name: 'AES-GCM', length: 256 }, ['encrypt', 'decrypt']),
    responseHmac: await deriveKey('response-hmac', { name: 'HMAC', hash: 'SHA-256', length: 256 }, ['sign', 'verify'])
  };
}

function encodePadded(value) {
  const json = encoder.encode(JSON.stringify(value));
  if (json.length > PADDED_BYTES - 2) throw protocolError('PAYLOAD_TOO_LARGE');
  const output = crypto.getRandomValues(new Uint8Array(PADDED_BYTES));
  new DataView(output.buffer).setUint16(0, json.length, false);
  output.set(json, 2);
  return output;
}

function decodePadded(value) {
  if (value.length !== PADDED_BYTES) throw protocolError('INVALID_PAYLOAD');
  const length = new DataView(value.buffer, value.byteOffset, value.byteLength).getUint16(0, false);
  if (length > PADDED_BYTES - 2) throw protocolError('INVALID_PAYLOAD');
  try { return JSON.parse(decoder.decode(value.subarray(2, 2 + length))); } catch { throw protocolError('INVALID_PAYLOAD'); }
}

function requestPath(deviceId) { return `/v1/devices/${deviceId}/trigger`; }

function canonical(kind, deviceId, timestamp, nonce, bodyHash) {
  const method = kind === 'REQUEST' ? 'POST' : 'RESPONSE';
  return [PREAMBLE, method, requestPath(deviceId), timestamp, nonce, bodyHash].join('\n');
}

function aad(kind, deviceId, keyId, requestId, timestamp, nonce) {
  return encoder.encode([PREAMBLE, kind, deviceId, keyId, requestId, timestamp, nonce].join('\n'));
}

async function sign(key, kind, deviceId, timestamp, nonce, rawBody) {
  const bodyHash = hex(await sha256(encoder.encode(rawBody)));
  return hex(new Uint8Array(await crypto.subtle.sign('HMAC', key, encoder.encode(canonical(kind, deviceId, timestamp, nonce, bodyHash)))));
}

export async function verifyAndDecryptRequest({ deviceId, client, headers, rawBody, now = Date.now() }) {
  const keyId = headers['x-tt-key-id'];
  const timestamp = headers['x-tt-timestamp'];
  const nonce = headers['x-tt-nonce'];
  const signature = headers['x-tt-signature'];
  if (keyId !== client.keyId || !/^\d{10}$/.test(timestamp || '')) throw protocolError('INVALID_SIGNATURE');
  if (Math.abs(Math.floor(now / 1000) - Number(timestamp)) > 30) throw protocolError('STALE_REQUEST');
  base64urlDecode(nonce, 16);
  const body = JSON.parse(rawBody);
  if (!body || body.v !== 1 || typeof body.requestId !== 'string' || typeof body.iv !== 'string' || typeof body.ciphertext !== 'string') throw protocolError('INVALID_PAYLOAD');
  const keys = await derive(client.secret);
  const expected = await sign(keys.requestHmac, 'REQUEST', deviceId, timestamp, nonce, rawBody);
  if (!equal(unhex(signature), unhex(expected))) throw protocolError('INVALID_SIGNATURE');
  const plain = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: base64urlDecode(body.iv, 12), additionalData: aad('REQUEST', deviceId, keyId, body.requestId, timestamp, nonce), tagLength: 128 },
    keys.requestEncryption,
    base64urlDecode(body.ciphertext, PADDED_BYTES + 16)
  ).catch(() => { throw protocolError('DECRYPTION_FAILED'); });
  const payload = decodePadded(new Uint8Array(plain));
  return { payload, requestId: body.requestId, nonce, fingerprint: hex(await sha256(encoder.encode(rawBody))) };
}

export async function encryptResponse({ deviceId, client, requestId, result, timestamp = Math.floor(Date.now() / 1000), nonce = randomBase64url(16), iv = randomBase64url(12) }) {
  const timestampText = String(timestamp);
  const keys = await derive(client.secret);
  const ciphertext = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: base64urlDecode(iv, 12), additionalData: aad('RESPONSE', deviceId, client.keyId, requestId, timestampText, nonce), tagLength: 128 },
    keys.responseEncryption,
    encodePadded({ requestId, ...result })
  );
  const body = JSON.stringify({ v: 1, requestId, iv, ciphertext: base64urlEncode(new Uint8Array(ciphertext)) });
  return {
    headers: {
      'x-tt-key-id': client.keyId,
      'x-tt-timestamp': timestampText,
      'x-tt-nonce': nonce,
      'x-tt-signature': await sign(keys.responseHmac, 'RESPONSE', deviceId, timestampText, nonce, body)
    },
    body
  };
}

export const protocol = { PREAMBLE, PADDED_BYTES };
