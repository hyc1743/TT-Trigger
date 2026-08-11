#!/usr/bin/env node
const [command = "codes", ...args] = process.argv.slice(2);
const relay = (process.env.TT_RELAY_URL || "").replace(/\/$/, "");
const token = process.env.TT_ADMIN_TOKEN || "";
if (!relay || !token) {
  console.error("Set TT_RELAY_URL and TT_ADMIN_TOKEN first.");
  process.exit(2);
}
if (!["codes", "revoke"].includes(command)) {
  console.error("Usage: npm run admin -- codes [count] [ttlSeconds] | revoke <deviceId>");
  process.exit(2);
}
const path = command === "codes"
  ? "/v1/admin/activation-codes"
  : `/v1/admin/devices/${encodeURIComponent(args[0] || "")}/revoke`;
const response = await fetch(`${relay}${path}`, {
  method: "POST",
  headers: { authorization: `Bearer ${token}`, "content-type": "application/json" },
  body: command === "codes" ? JSON.stringify({ count: Number(args[0] || 1), ttlSeconds: Number(args[1] || 86400) }) : undefined
});
console.log(await response.text());
process.exit(response.ok ? 0 : 1);
