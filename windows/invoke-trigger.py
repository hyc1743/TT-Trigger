#!/usr/bin/env python3
"""TT-Trigger HMAC webhook client using only the Python standard library."""

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


VERSION = "3.1.0"
DEFAULT_CONFIG = pathlib.Path(__file__).resolve().with_name("config.json")


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
    if mode == "public_caddy":
        domain = deployment.get("domain")
        if not isinstance(domain, str) or not domain:
            raise ValueError("公网模式缺少deployment.domain")
        return f"https://{domain}"
    if mode == "local_tailscale":
        listen = config.get("api_listen", "127.0.0.1:8788")
        if not isinstance(listen, str) or not listen:
            raise ValueError("config.json 中的api_listen无效")
        return f"http://{listen}"
    raise ValueError("config.json 中的deployment.mode无效")


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
