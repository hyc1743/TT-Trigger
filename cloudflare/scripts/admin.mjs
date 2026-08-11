#!/usr/bin/env node
const [command = "codes", ...args] = process.argv.slice(2);
const relay = (process.env.TT_RELAY_URL || "").replace(/\/$/, "");
const token = process.env.TT_ADMIN_TOKEN || "";
if (!relay || !token) {
  console.error("Set TT_RELAY_URL and TT_ADMIN_TOKEN first.");
  process.exit(2);
}
let relayUrl;
try { relayUrl = new URL(relay); } catch { relayUrl = null; }
if (!relayUrl || relayUrl.protocol !== "https:" || relayUrl.pathname !== "/" || relayUrl.search || relayUrl.hash || relayUrl.username || relayUrl.password) {
  console.error("TT_RELAY_URL must be a clean HTTPS origin.");
  process.exit(2);
}
if (!["codes", "revoke"].includes(command)) {
  console.error("Usage: npm run admin -- codes [count] [ttlSeconds] | revoke <deviceId>");
  process.exit(2);
}
const path = command === "codes"
  ? "/v1/admin/activation-codes"
  : `/v1/admin/devices/${encodeURIComponent(args[0] || "")}/revoke`;
const count = Number(args[0] || 1);
const ttlSeconds = Number(args[1] || 86400);
if (command === "codes" && (!Number.isInteger(count) || count < 1 || count > 100 || !Number.isInteger(ttlSeconds) || ttlSeconds < 60 || ttlSeconds > 30 * 86400)) {
  console.error("count must be 1..100 and ttlSeconds must be 60..2592000.");
  process.exit(2);
}
const response = await fetch(`${relay}${path}`, {
  method: "POST",
  headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
  body: command === "codes" ? JSON.stringify({ count, ttlSeconds }) : undefined
});
console.log(await response.text());
process.exit(response.ok ? 0 : 1);
