import { SELF } from "cloudflare:test";
import { describe, expect, it } from "vitest";

const deviceId = "MDEyMzQ1Njc4OWFiY2RlZg";
const deviceToken = "device-token-that-is-at-least-thirty-two-characters";
const deviceGrant = "device-grant-that-is-at-least-thirty-two-characters";

async function register() {
  const response = await SELF.fetch("https://relay.test/v1/register", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ deviceId, deviceToken, deviceGrant, activationCode: "" })
  });
  return { response, body: await response.json<Record<string, unknown>>() };
}

describe("TT-Trigger relay", () => {
  it("reports health without exposing configuration", async () => {
    const response = await SELF.fetch("https://relay.test/health");
    expect(response.status).toBe(200);
    expect(await response.json()).toEqual({ ok: true, service: "tt-trigger-relay", version: 1 });
  });

  it("registers one device, authenticates a caller, and reports an offline extension", async () => {
    const registration = await register();
    expect(registration.response.status).toBe(200);
    expect(registration.body.deviceGrant).toBe(deviceGrant);
    const keyId = "caller-1";
    const relayToken = "caller-token-that-is-at-least-thirty-two-characters";
    const created = await SELF.fetch(`https://relay.test/v1/devices/${deviceId}/clients`, {
      method: "POST",
      headers: {
        authorization: `Bearer ${deviceToken}`,
        "x-tt-device-grant": deviceGrant,
        "content-type": "application/json"
      },
      body: JSON.stringify({ keyId, clientToken: relayToken })
    });
    expect(created.status).toBe(200);
    const relayGrant = String((await created.json<Record<string, unknown>>()).relayGrant);
    const response = await SELF.fetch(`https://relay.test/v1/devices/${deviceId}/trigger`, {
      method: "POST",
      headers: {
        authorization: `Bearer ${relayToken}`,
        "x-tt-relay-grant": relayGrant,
        "x-tt-key-id": keyId,
        "x-tt-timestamp": "1786400000",
        "x-tt-nonce": "MDEyMzQ1Njc4OWFiY2RlZg",
        "x-tt-signature": "0".repeat(64),
        "content-type": "application/json"
      },
      body: JSON.stringify({ v: 1, requestId: "550e8400-e29b-41d4-a716-446655440000", iv: "a", ciphertext: "b" })
    });
    expect(response.status).toBe(503);
    expect((await response.json<Record<string, unknown>>()).code).toBe("DEVICE_OFFLINE");

    const second = await SELF.fetch("https://relay.test/v1/register", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ deviceId: "YWJjZGVmZ2hpamtsbW5vcA", deviceToken, deviceGrant, activationCode: "" })
    });
    expect(second.status).toBe(403);
  });

  it("creates one-time activation codes through the administrator endpoint", async () => {
    const response = await SELF.fetch("https://relay.test/v1/admin/activation-codes", {
      method: "POST",
      headers: { authorization: "Bearer test-admin-token", "content-type": "application/json" },
      body: JSON.stringify({ count: 2, ttlSeconds: 600 })
    });
    expect(response.status).toBe(200);
    const value = await response.json<{ codes: string[] }>();
    expect(value.codes).toHaveLength(2);
    expect(value.codes[0]).not.toBe(value.codes[1]);
  });
});
