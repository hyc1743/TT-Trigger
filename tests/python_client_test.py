import base64
import hashlib
import hmac
import importlib.util
import json
import pathlib
import tempfile
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
        self.assertEqual(CLIENT.infer_base_url(loaded), "https://trigger.example.com")
        with self.assertRaises(ValueError):
            CLIENT.resolve_credentials(loaded, "missing", None)


if __name__ == "__main__":
    unittest.main()
