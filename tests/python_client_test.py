import base64
import hashlib
import hmac
import importlib.util
import json
import pathlib
import tempfile
import time
import unittest


SCRIPT = pathlib.Path(__file__).parents[1] / "windows" / "invoke-trigger.py"
SPEC = importlib.util.spec_from_file_location("invoke_trigger", SCRIPT)
CLIENT = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(CLIENT)


class PythonClientTests(unittest.TestCase):
    def test_builds_exact_utf8_hmac_request(self):
        key = b"abcdefghijklmnopqrstuvwxyzABCDEF"
        secret = base64.urlsafe_b64encode(key).rstrip(b"=").decode()
        request = CLIENT.build_signed_request(
            base_url="https://trigger.example.com/",
            key_id="caller-1",
            secret=secret,
            symbol="BG-P:SIREN/USDT+OD-S:SIREN/USDT",
            add_pair=True,
            request_id="550e8400-e29b-41d4-a716-446655440000",
            timestamp=1786400000,
            nonce="MDEyMzQ1Njc4OWFiY2RlZg",
        )
        self.assertEqual(request.full_url, "https://trigger.example.com/webhook")
        body = request.data
        self.assertEqual(
            json.loads(body),
            {
                "requestId": "550e8400-e29b-41d4-a716-446655440000",
                "symbol": "BG-P:SIREN/USDT+OD-S:SIREN/USDT",
                "addPair": True,
            },
        )
        canonical = "\n".join(
            (
                "TT-TRIGGER-V1",
                "POST",
                "/webhook",
                "1786400000",
                "MDEyMzQ1Njc4OWFiY2RlZg",
                hashlib.sha256(body).hexdigest(),
            )
        )
        expected = hmac.new(key, canonical.encode("ascii"), hashlib.sha256).hexdigest()
        self.assertEqual(request.get_header("X-tt-signature"), expected)

    def test_rejects_invalid_base_url_and_secret(self):
        valid_secret = base64.urlsafe_b64encode(b"x" * 32).rstrip(b"=").decode()
        with self.assertRaises(ValueError):
            CLIENT.webhook_url("https://example.com/path")
        with self.assertRaises(ValueError):
            CLIENT.build_signed_request(
                base_url="https://example.com",
                key_id="default",
                secret="short",
                symbol="BTC",
                add_pair=False,
            )
        self.assertEqual(CLIENT.webhook_url("http://127.0.0.1:8788"), "http://127.0.0.1:8788/webhook")
        self.assertEqual(len(CLIENT.base64url_decode(valid_secret)), 32)

    def test_reuses_integrated_config(self):
        secret = base64.urlsafe_b64encode(b"x" * 32).rstrip(b"=").decode()
        config = {
            "api_listen": "127.0.0.1:8788",
            "hmac_keys": [{"id": "default", "secret": secret}],
            "deployment": {"mode": "local_tailscale"},
        }
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "config.json"
            path.write_text(json.dumps(config), encoding="utf-8")
            loaded = CLIENT.load_config(str(path))
        self.assertEqual(CLIENT.infer_base_url(loaded), "http://127.0.0.1:8788")
        self.assertEqual(CLIENT.resolve_credentials(loaded, None, None), ("default", secret))

        loaded["deployment"] = {"mode": "public_caddy", "domain": "trigger.example.com"}
        with self.assertRaises(ValueError):
            CLIENT.infer_base_url(loaded)
        with self.assertRaises(ValueError):
            CLIENT.resolve_credentials(loaded, "missing", None)

    def test_cloud_request_and_encrypted_response(self):
        try:
            from cryptography.hazmat.primitives.ciphers.aead import AESGCM
        except ImportError:
            self.skipTest("cryptography is not installed")
        config = {
            "version": 1,
            "mode": "cloud_e2ee",
            "relay_url": "https://relay.example.workers.dev",
            "device_id": "MDEyMzQ1Njc4OWFiY2RlZg",
            "key_id": "caller-1",
            "relay_token": "r" * 43,
            "relay_grant": "g" * 43,
            "secret": CLIENT.base64url_encode(b"s" * 32),
        }
        request_id = "550e8400-e29b-41d4-a716-446655440000"
        timestamp = int(time.time())
        request, returned_id = CLIENT.build_cloud_request(
            config, "BG-P:SIREN/USDT+OD-S:SIREN/USDT", True,
            request_id=request_id, timestamp=timestamp,
            nonce=CLIENT.base64url_encode(b"n" * 16), iv=CLIENT.base64url_encode(b"i" * 12),
        )
        self.assertEqual(returned_id, request_id)
        self.assertEqual(request.full_url, f"https://relay.example.workers.dev/v1/devices/{config['device_id']}/trigger")
        envelope = json.loads(request.data)
        keys = CLIENT.derive_cloud_keys(config["secret"])
        plain = AESGCM(keys["request-encryption"]).decrypt(
            b"i" * 12,
            CLIENT.decode_fixed_base64url(envelope["ciphertext"], CLIENT.PADDED_BYTES + 16, "ciphertext"),
            CLIENT.e2ee_aad("REQUEST", config["device_id"], config["key_id"], request_id, str(timestamp), CLIENT.base64url_encode(b"n" * 16)),
        )
        self.assertEqual(CLIENT.decode_padded(plain)["symbol"], "BG-P:SIREN/USDT+OD-S:SIREN/USDT")

        response_nonce = CLIENT.base64url_encode(b"o" * 16)
        response_iv = CLIENT.base64url_encode(b"j" * 12)
        result = {"requestId": request_id, "ok": True}
        ciphertext = AESGCM(keys["response-encryption"]).encrypt(
            b"j" * 12,
            CLIENT.encode_padded(result),
            CLIENT.e2ee_aad("RESPONSE", config["device_id"], config["key_id"], request_id, str(timestamp), response_nonce),
        )
        response_body = json.dumps({"v": 1, "requestId": request_id, "iv": response_iv, "ciphertext": CLIENT.base64url_encode(ciphertext)}, separators=(",", ":")).encode()
        signature = hmac.new(keys["response-hmac"], CLIENT.e2ee_canonical("RESPONSE", config["device_id"], str(timestamp), response_nonce, response_body), hashlib.sha256).hexdigest()
        headers = {"X-TT-Key-Id": config["key_id"], "X-TT-Timestamp": str(timestamp), "X-TT-Nonce": response_nonce, "X-TT-Signature": signature}
        self.assertTrue(CLIENT.decrypt_cloud_response(config, request_id, headers, response_body, now=timestamp)["ok"])


if __name__ == "__main__":
    unittest.main()
