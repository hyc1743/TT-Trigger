#!/usr/bin/env python3
"""TT-Trigger local HMAC and Cloudflare E2EE webhook client."""

from __future__ import annotations

import argparse
import base64
import hashlib
import hmac
import json
import os
import pathlib
import secrets
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from typing import Optional


VERSION = "4.0.0"
DEFAULT_CONFIG = pathlib.Path(__file__).resolve().with_name("config.json")
E2EE_PREAMBLE = "TT-TRIGGER-E2EE-V1"
E2EE_SALT = b"TT-Trigger E2EE v1"
PADDED_BYTES = 2048


def base64url_encode(value: bytes) -> str:
    return base64.urlsafe_b64encode(value).rstrip(b"=").decode("ascii")


def base64url_decode(value: str) -> bytes:
    padded = value + "=" * (-len(value) % 4)
    try:
        decoded = base64.b64decode(padded, altchars=b"-_", validate=True)
    except (ValueError, base64.binascii.Error) as exc:
        raise ValueError("HMAC secret 必须是无填充 Base64URL") from exc
    if len(decoded) != 32:
        raise ValueError("HMAC secret 解码后必须恰好为 32 字节")
    return decoded


def decode_fixed_base64url(value: str, length: int, label: str) -> bytes:
    padded = value + "=" * (-len(value) % 4)
    try:
        decoded = base64.b64decode(padded, altchars=b"-_", validate=True)
    except (ValueError, base64.binascii.Error) as exc:
        raise ValueError(f"{label} 必须是无填充 Base64URL") from exc
    if len(decoded) != length:
        raise ValueError(f"{label} 解码后必须恰好为 {length} 字节")
    return decoded


def webhook_url(base_url: str) -> str:
    parsed = urllib.parse.urlsplit(base_url.strip())
    if (
        parsed.scheme not in {"http", "https"}
        or not parsed.hostname
        or parsed.username
        or parsed.password
        or parsed.query
        or parsed.fragment
        or parsed.path not in {"", "/"}
    ):
        raise ValueError("Base URL 必须是 http(s)://主机[:端口]，不能包含路径、账号或查询参数")
    return urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, "/webhook", "", ""))


def load_config(path: str) -> dict:
    try:
        with open(path, "r", encoding="utf-8-sig") as stream:
            value = json.load(stream)
    except FileNotFoundError as exc:
        raise ValueError(f"找不到配置文件：{path}") from exc
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"无法读取配置文件 {path}：{exc}") from exc
    if not isinstance(value, dict):
        raise ValueError("config.json 顶层必须是JSON对象")
    return value


def infer_base_url(config: dict) -> str:
    deployment = config.get("deployment")
    if not isinstance(deployment, dict):
        raise ValueError("config.json 尚未包含deployment，请先运行start.bat或configure.bat")
    mode = deployment.get("mode")
    if mode == "local_tailscale":
        listen = config.get("api_listen", "127.0.0.1:8788")
        if not isinstance(listen, str) or not listen:
            raise ValueError("config.json 中的api_listen无效")
        return f"http://{listen}"
    raise ValueError("config.json 中的deployment.mode无效")


def cloud_trigger_url(relay_url: str, device_id: str) -> str:
    parsed = urllib.parse.urlsplit(relay_url.strip())
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.path not in {"", "/"}:
        raise ValueError("relay_url 必须是无路径、账号和参数的 https:// 地址")
    if not device_id or not all(char.isalnum() or char in "_-" for char in device_id):
        raise ValueError("device_id 无效")
    return urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, f"/v1/devices/{device_id}/trigger", "", ""))


def require_crypto():
    try:
        from cryptography.hazmat.primitives import hashes
        from cryptography.hazmat.primitives.ciphers.aead import AESGCM
        from cryptography.hazmat.primitives.kdf.hkdf import HKDF
    except ImportError as exc:
        raise ValueError("云端模式需要 cryptography，请运行：python -m pip install cryptography") from exc
    return hashes, AESGCM, HKDF


def derive_cloud_keys(secret_text: str) -> dict[str, bytes]:
    hashes, _aesgcm, hkdf = require_crypto()
    secret = decode_fixed_base64url(secret_text, 32, "secret")
    result = {}
    for name in ("request-encryption", "request-hmac", "response-encryption", "response-hmac"):
        result[name] = hkdf(algorithm=hashes.SHA256(), length=32, salt=E2EE_SALT, info=name.encode("ascii")).derive(secret)
    return result


def encode_padded(value: dict) -> bytes:
    raw = json.dumps(value, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    if len(raw) > PADDED_BYTES - 2:
        raise ValueError("E2EE 明文超过固定填充容量")
    return len(raw).to_bytes(2, "big") + raw + secrets.token_bytes(PADDED_BYTES - 2 - len(raw))


def decode_padded(value: bytes) -> dict:
    if len(value) != PADDED_BYTES:
        raise ValueError("E2EE 响应长度无效")
    length = int.from_bytes(value[:2], "big")
    if length > PADDED_BYTES - 2:
        raise ValueError("E2EE 响应填充无效")
    try:
        result = json.loads(value[2:2 + length].decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError("E2EE 响应明文无效") from exc
    if not isinstance(result, dict):
        raise ValueError("E2EE 响应明文必须是JSON对象")
    return result


def e2ee_canonical(kind: str, device_id: str, timestamp: str, nonce: str, body: bytes) -> bytes:
    method = "POST" if kind == "REQUEST" else "RESPONSE"
    path = f"/v1/devices/{device_id}/trigger"
    return "\n".join((E2EE_PREAMBLE, method, path, timestamp, nonce, hashlib.sha256(body).hexdigest())).encode("ascii")


def e2ee_aad(kind: str, device_id: str, key_id: str, request_id: str, timestamp: str, nonce: str) -> bytes:
    return "\n".join((E2EE_PREAMBLE, kind, device_id, key_id, request_id, timestamp, nonce)).encode("ascii")


def validate_cloud_config(config: dict) -> None:
    required = ("relay_url", "device_id", "key_id", "relay_token", "relay_grant", "secret")
    if config.get("version") != 1 or config.get("mode") != "cloud_e2ee" or any(not isinstance(config.get(name), str) or not config[name] for name in required):
        raise ValueError("client.json 云端配置格式无效")
    decode_fixed_base64url(config["secret"], 32, "secret")


def build_cloud_request(
    config: dict,
    symbol: str,
    add_pair: bool,
    request_id: Optional[str] = None,
    timestamp: Optional[int] = None,
    nonce: Optional[str] = None,
    iv: Optional[str] = None,
) -> tuple[urllib.request.Request, str]:
    validate_cloud_config(config)
    _hashes, aesgcm, _hkdf = require_crypto()
    keys = derive_cloud_keys(config["secret"])
    request_id = request_id or str(uuid.uuid4())
    timestamp_text = str(int(time.time()) if timestamp is None else timestamp)
    nonce = nonce or base64url_encode(secrets.token_bytes(16))
    iv = iv or base64url_encode(secrets.token_bytes(12))
    ciphertext = aesgcm(keys["request-encryption"]).encrypt(
        decode_fixed_base64url(iv, 12, "iv"),
        encode_padded({"symbol": symbol, "addPair": bool(add_pair)}),
        e2ee_aad("REQUEST", config["device_id"], config["key_id"], request_id, timestamp_text, nonce),
    )
    body = json.dumps({"v": 1, "requestId": request_id, "iv": iv, "ciphertext": base64url_encode(ciphertext)}, separators=(",", ":")).encode("utf-8")
    signature = hmac.new(keys["request-hmac"], e2ee_canonical("REQUEST", config["device_id"], timestamp_text, nonce, body), hashlib.sha256).hexdigest()
    return urllib.request.Request(
        cloud_trigger_url(config["relay_url"], config["device_id"]),
        data=body,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {config['relay_token']}",
            "X-TT-Relay-Grant": config["relay_grant"],
            "X-TT-Key-Id": config["key_id"],
            "X-TT-Timestamp": timestamp_text,
            "X-TT-Nonce": nonce,
            "X-TT-Signature": signature,
            "User-Agent": f"TT-Trigger-Python/{VERSION}",
        },
    ), request_id


def decrypt_cloud_response(config: dict, request_id: str, headers, body: bytes, now: Optional[int] = None) -> dict:
    _hashes, aesgcm, _hkdf = require_crypto()
    keys = derive_cloud_keys(config["secret"])
    key_id = headers.get("X-TT-Key-Id", "")
    timestamp = headers.get("X-TT-Timestamp", "")
    nonce = headers.get("X-TT-Nonce", "")
    signature = headers.get("X-TT-Signature", "")
    if key_id != config["key_id"] or not timestamp.isdigit() or abs((int(time.time()) if now is None else now) - int(timestamp)) > 30:
        raise ValueError("E2EE 响应时间或keyId无效")
    decode_fixed_base64url(nonce, 16, "response nonce")
    expected = hmac.new(keys["response-hmac"], e2ee_canonical("RESPONSE", config["device_id"], timestamp, nonce, body), hashlib.sha256).hexdigest()
    if not hmac.compare_digest(expected, signature):
        raise ValueError("E2EE 响应签名无效")
    try:
        envelope = json.loads(body.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError("E2EE 响应JSON无效") from exc
    if not isinstance(envelope, dict) or envelope.get("v") != 1 or envelope.get("requestId") != request_id:
        raise ValueError("E2EE 响应requestId无效")
    iv = decode_fixed_base64url(str(envelope.get("iv", "")), 12, "response iv")
    ciphertext = decode_fixed_base64url(str(envelope.get("ciphertext", "")), PADDED_BYTES + 16, "response ciphertext")
    try:
        plaintext = aesgcm(keys["response-encryption"]).decrypt(
            iv, ciphertext, e2ee_aad("RESPONSE", config["device_id"], config["key_id"], request_id, timestamp, nonce)
        )
    except Exception as exc:
        raise ValueError("E2EE 响应解密失败") from exc
    result = decode_padded(plaintext)
    if result.get("requestId") != request_id or not isinstance(result.get("ok"), bool):
        raise ValueError("E2EE 响应内容无效")
    return result


def resolve_credentials(config: dict, key_id: Optional[str], secret: Optional[str]) -> tuple[str, str]:
    keys = config.get("hmac_keys")
    if not isinstance(keys, list) or not keys:
        keys = []
    selected_id = key_id.strip() if key_id else ""
    if not selected_id and keys and isinstance(keys[0], dict):
        selected_id = str(keys[0].get("id", "")).strip()
    if not selected_id:
        raise ValueError("没有可用的keyId，请检查config.json或提供--key-id")
    if secret:
        return selected_id, secret.strip()
    for key in keys:
        if isinstance(key, dict) and key.get("id") == selected_id and isinstance(key.get("secret"), str):
            return selected_id, key["secret"].strip()
    raise ValueError(f"config.json 中找不到keyId {selected_id!r} 的secret")


def build_signed_request(
    *,
    base_url: str,
    key_id: str,
    secret: str,
    symbol: str,
    add_pair: bool,
    request_id: Optional[str] = None,
    timestamp: Optional[int] = None,
    nonce: Optional[str] = None,
) -> urllib.request.Request:
    if not key_id.strip():
        raise ValueError("keyId 不能为空")
    key = base64url_decode(secret.strip())
    request_id = request_id or str(uuid.uuid4())
    timestamp_text = str(int(time.time()) if timestamp is None else timestamp)
    nonce = nonce or base64url_encode(secrets.token_bytes(16))

    payload = {
        "requestId": request_id,
        "symbol": symbol,
        "addPair": bool(add_pair),
    }
    body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode("utf-8")
    body_hash = hashlib.sha256(body).hexdigest()
    canonical = "\n".join(
        ("TT-TRIGGER-V1", "POST", "/webhook", timestamp_text, nonce, body_hash)
    )
    signature = hmac.new(key, canonical.encode("ascii"), hashlib.sha256).hexdigest()

    return urllib.request.Request(
        webhook_url(base_url),
        data=body,
        method="POST",
        headers={
            "Content-Type": "application/json",
            "X-TT-Key-Id": key_id.strip(),
            "X-TT-Timestamp": timestamp_text,
            "X-TT-Nonce": nonce,
            "X-TT-Signature": signature,
            "User-Agent": f"TT-Trigger-Python/{VERSION}",
        },
    )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="调用 TT-Trigger HMAC webhook")
    parser.add_argument("--config", default=str(DEFAULT_CONFIG), help="默认读取脚本同目录的config.json")
    parser.add_argument("--base-url", help="覆盖config.json推断的地址，例如Tailscale地址")
    parser.add_argument("--symbol", required=True, help="要填写的 symbol")
    parser.add_argument("--add-pair", action="store_true", help="填写后等待并点击添加交易对")
    parser.add_argument("--key-id", default=os.getenv("TT_KEY_ID"), help="默认读取 TT_KEY_ID")
    parser.add_argument("--secret", default=os.getenv("TT_HMAC_SECRET"), help="默认读取 TT_HMAC_SECRET")
    parser.add_argument("--timeout", type=float, default=70, help="HTTP超时秒数，默认70")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        needs_config = not (args.base_url and args.key_id and args.secret)
        config = load_config(args.config) if needs_config else {}
        if config.get("mode") == "cloud_e2ee":
            request, request_id = build_cloud_request(config, args.symbol, args.add_pair)
            try:
                response = urllib.request.urlopen(request, timeout=args.timeout)
            except urllib.error.HTTPError as exc:
                response = exc
            with response:
                status = response.status
                body = response.read()
                headers = response.headers
            print(f"HTTP {status}")
            if status != 200:
                print(body.decode("utf-8", errors="replace"))
                return 1
            result = decrypt_cloud_response(config, request_id, headers, body)
            print(json.dumps(result, ensure_ascii=False))
            return 0 if result.get("ok") is True else 1
        key_id, secret = resolve_credentials(config, args.key_id, args.secret)
        base_url = args.base_url or infer_base_url(config)
        request = build_signed_request(
            base_url=base_url,
            key_id=key_id,
            secret=secret,
            symbol=args.symbol,
            add_pair=args.add_pair,
        )
        try:
            response = urllib.request.urlopen(request, timeout=args.timeout)
        except urllib.error.HTTPError as exc:
            response = exc
        with response:
            status = response.status
            text = response.read().decode("utf-8", errors="replace")
        print(f"HTTP {status}")
        print(text)
        return 0 if 200 <= status < 300 else 1
    except (ValueError, OSError, urllib.error.URLError) as exc:
        print(f"错误：{exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
