import { DurableObject } from "cloudflare:workers";

export interface Env {
  ENROLLMENT_REGISTRY: DurableObjectNamespace<EnrollmentRegistry>;
  DEVICE_RELAY: DurableObjectNamespace<DeviceRelay>;
  ENROLLMENT_MODE?: string;
  OPEN_ENROLLMENT?: string;
  ADMIN_TOKEN?: string;
  REQUEST_TIMEOUT_MS?: string;
  RATE_LIMIT_PER_MINUTE?: string;
  RATE_LIMIT_BURST?: string;
  MAX_CLIENTS_PER_DEVICE?: string;
}

const MAX_BODY_BYTES = 8192;
const ID_RE = /^[A-Za-z0-9_-]{16,64}$/;
const KEY_RE = /^[A-Za-z0-9._-]{1,64}$/;

type JsonObject = Record<string, unknown>;

function json(value: unknown, status = 200, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json; charset=utf-8", "cache-control": "no-store", ...headers }
  });
}

function error(status: number, code: string, message: string): Response {
  return json({ ok: false, code, message }, status);
}

async function readJson(request: Request): Promise<JsonObject> {
  const length = Number(request.headers.get("content-length") || "0");
  if (length > MAX_BODY_BYTES) throw new Error("BODY_TOO_LARGE");
  const text = await request.text();
  if (new TextEncoder().encode(text).byteLength > MAX_BODY_BYTES) throw new Error("BODY_TOO_LARGE");
  const value: unknown = JSON.parse(text);
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("INVALID_JSON");
  return value as JsonObject;
}

function randomToken(bytes = 32): string {
  const value = crypto.getRandomValues(new Uint8Array(bytes));
  return base64url(value);
}

function base64url(value: Uint8Array): string {
  let binary = "";
  for (const byte of value) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

async function digest(value: string): Promise<string> {
  return base64url(new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(value))));
}

function bearer(request: Request): string {
  const header = request.headers.get("authorization") || "";
  return header.startsWith("Bearer ") ? header.slice(7).trim() : "";
}

function deviceStub(env: Env, deviceId: string): DurableObjectStub<DeviceRelay> {
  return env.DEVICE_RELAY.get(env.DEVICE_RELAY.idFromName(deviceId));
}

function enrollmentStub(env: Env): DurableObjectStub<EnrollmentRegistry> {
  return env.ENROLLMENT_REGISTRY.get(env.ENROLLMENT_REGISTRY.idFromName("global"));
}

async function internalFetch(stub: DurableObjectStub, path: string, init: RequestInit): Promise<Response> {
  return stub.fetch(new Request(`https://internal${path}`, init));
}

function adminAuthorized(request: Request, env: Env): boolean {
  return Boolean(env.ADMIN_TOKEN && bearer(request) === env.ADMIN_TOKEN);
}

async function handleRegister(request: Request, env: Env): Promise<Response> {
  let body: JsonObject;
  try { body = await readJson(request); } catch (cause) {
    return error(cause instanceof Error && cause.message === "BODY_TOO_LARGE" ? 413 : 400, "INVALID_REQUEST", "registration request is invalid");
  }
  const deviceId = typeof body.deviceId === "string" ? body.deviceId : "";
  const deviceToken = typeof body.deviceToken === "string" ? body.deviceToken : "";
  const deviceGrant = typeof body.deviceGrant === "string" ? body.deviceGrant : "";
  const activationCode = typeof body.activationCode === "string" ? body.activationCode : "";
  if (!ID_RE.test(deviceId) || deviceToken.length < 32 || deviceGrant.length < 32) return error(400, "INVALID_REQUEST", "device registration fields are invalid");

  const registration = { deviceTokenHash: await digest(deviceToken), deviceGrantHash: await digest(deviceGrant) };
  const existing = await internalFetch(deviceStub(env, deviceId), "/register-status", {
    method: "POST", body: JSON.stringify(registration)
  });
  if (existing.status === 200) return json({ ok: true, deviceId, deviceGrant });
  if (existing.status === 409) return existing;

  const mode = env.OPEN_ENROLLMENT === "true" ? "open" : (env.ENROLLMENT_MODE || "activation_required");
  const authorization = await internalFetch(enrollmentStub(env), "/authorize", {
    method: "POST",
    body: JSON.stringify({ mode, activationCodeHash: activationCode ? await digest(activationCode) : "" })
  });
  if (!authorization.ok) return authorization;

  const registered = await internalFetch(deviceStub(env, deviceId), "/register", {
    method: "POST",
    body: JSON.stringify(registration)
  });
  if (!registered.ok) return registered;
  return json({ ok: true, deviceId, deviceGrant });
}

async function handleAdminCodes(request: Request, env: Env): Promise<Response> {
  if (!adminAuthorized(request, env)) return error(401, "UNAUTHORIZED", "administrator authentication failed");
  let body: JsonObject;
  try { body = await readJson(request); } catch { return error(400, "INVALID_REQUEST", "request is invalid"); }
  const count = Math.max(1, Math.min(100, Number(body.count || 1)));
  const ttlSeconds = Math.max(60, Math.min(30 * 86400, Number(body.ttlSeconds || 86400)));
  const codes = Array.from({ length: count }, () => randomToken(24));
  const response = await internalFetch(enrollmentStub(env), "/codes", {
    method: "POST",
    body: JSON.stringify({ hashes: await Promise.all(codes.map(digest)), expiresAt: Date.now() + ttlSeconds * 1000 })
  });
  if (!response.ok) return response;
  return json({ ok: true, codes, expiresAt: new Date(Date.now() + ttlSeconds * 1000).toISOString() });
}

async function route(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);
  if (url.pathname === "/health" && request.method === "GET") return json({ ok: true, service: "tt-trigger-relay", version: 1 });
  if (url.pathname === "/v1/register" && request.method === "POST") return handleRegister(request, env);
  if (url.pathname === "/v1/admin/activation-codes" && request.method === "POST") return handleAdminCodes(request, env);
  const adminRevoke = url.pathname.match(/^\/v1\/admin\/devices\/([A-Za-z0-9_-]{16,64})\/revoke$/);
  if (adminRevoke && request.method === "POST") {
    if (!adminAuthorized(request, env)) return error(401, "UNAUTHORIZED", "administrator authentication failed");
    return internalFetch(deviceStub(env, adminRevoke[1]), "/admin-revoke", { method: "POST" });
  }

  const match = url.pathname.match(/^\/v1\/devices\/([A-Za-z0-9_-]{16,64})(\/.*)$/);
  if (!match) return error(404, "NOT_FOUND", "endpoint not found");
  const [, deviceId, suffix] = match;
  const stub = deviceStub(env, deviceId);

  if (suffix === "/extension" && request.method === "GET" && request.headers.get("upgrade")?.toLowerCase() === "websocket") {
    return stub.fetch(new Request("https://internal/extension", request));
  }
  if (suffix === "/clients" && request.method === "POST") {
    return internalFetch(stub, "/clients", { method: "POST", headers: request.headers, body: await request.text() });
  }
  const clientMatch = suffix.match(/^\/clients\/([A-Za-z0-9._-]{1,64})$/);
  if (clientMatch && request.method === "DELETE") {
    return internalFetch(stub, `/clients/${encodeURIComponent(clientMatch[1])}`, { method: "DELETE", headers: request.headers });
  }
  if (suffix === "/device" && request.method === "DELETE") {
    return internalFetch(stub, "/device", { method: "DELETE", headers: request.headers });
  }
  if (suffix === "/trigger" && request.method === "POST") {
    const body = await request.text();
    if (new TextEncoder().encode(body).byteLength > MAX_BODY_BYTES) return error(413, "BODY_TOO_LARGE", "request body is too large");
    const headers = new Headers(request.headers);
    headers.set("x-tt-device-id", deviceId);
    headers.set("x-tt-request-timeout-ms", env.REQUEST_TIMEOUT_MS || "10000");
    headers.set("x-tt-rate-limit", env.RATE_LIMIT_PER_MINUTE || "60");
    headers.set("x-tt-rate-burst", env.RATE_LIMIT_BURST || "10");
    return internalFetch(stub, "/trigger", { method: "POST", headers, body });
  }
  return error(404, "NOT_FOUND", "endpoint not found");
}

export default { fetch: route } satisfies ExportedHandler<Env>;

export class EnrollmentRegistry extends DurableObject<Env> {
  async fetch(request: Request): Promise<Response> {
    const path = new URL(request.url).pathname;
    const body = request.method === "POST" ? await request.json<JsonObject>() : {};
    if (path === "/codes") {
      const hashes = Array.isArray(body.hashes) ? body.hashes.filter((v): v is string => typeof v === "string") : [];
      const expiresAt = Number(body.expiresAt || 0);
      await Promise.all(hashes.map((hash) => this.ctx.storage.put(`code:${hash}`, expiresAt)));
      return json({ ok: true });
    }
    if (path === "/authorize") {
      const mode = String(body.mode || "activation_required");
      if (mode === "open") return json({ ok: true });
      if (mode === "first_device") {
        let claimed = false;
        await this.ctx.storage.transaction(async (txn) => {
          if (!await txn.get<boolean>("first-device-claimed")) {
            await txn.put("first-device-claimed", true);
            claimed = true;
          }
        });
        return claimed ? json({ ok: true }) : error(403, "ENROLLMENT_CLOSED", "the first device has already registered");
      }
      const hash = String(body.activationCodeHash || "");
      let accepted = false;
      await this.ctx.storage.transaction(async (txn) => {
        const expiresAt = await txn.get<number>(`code:${hash}`);
        if (expiresAt && expiresAt > Date.now()) {
          await txn.delete(`code:${hash}`);
          accepted = true;
        }
      });
      return accepted ? json({ ok: true }) : error(403, "INVALID_ACTIVATION_CODE", "activation code is invalid or already used");
    }
    return error(404, "NOT_FOUND", "endpoint not found");
  }
}

interface PendingRequest {
  resolve: (response: Response) => void;
  timer: ReturnType<typeof setTimeout>;
}

interface SocketAttachment { authenticated?: boolean; }

export class DeviceRelay extends DurableObject<Env> {
  private pending = new Map<string, PendingRequest>();

  private async record(): Promise<{ deviceTokenHash: string; deviceGrantHash: string; revoked?: boolean } | undefined> {
    return this.ctx.storage.get("device");
  }

  private async ownerAuthorized(request: Request): Promise<boolean> {
    const record = await this.record();
    if (!record || record.revoked) return false;
    return await digest(bearer(request)) === record.deviceTokenHash
      && await digest(request.headers.get("x-tt-device-grant") || "") === record.deviceGrantHash;
  }

  async fetch(request: Request): Promise<Response> {
    const path = new URL(request.url).pathname;
    if (path === "/register" && request.method === "POST") {
      if (await this.record()) return error(409, "DEVICE_EXISTS", "device is already registered");
      const body = await request.json<{ deviceTokenHash: string; deviceGrantHash: string }>();
      await this.ctx.storage.put("device", body);
      return json({ ok: true });
    }
    if (path === "/register-status" && request.method === "POST") {
      const record = await this.record();
      if (!record) return error(404, "DEVICE_NOT_FOUND", "device is not registered");
      const body = await request.json<{ deviceTokenHash: string; deviceGrantHash: string }>();
      return !record.revoked && body.deviceTokenHash === record.deviceTokenHash && body.deviceGrantHash === record.deviceGrantHash
        ? json({ ok: true })
        : error(409, "DEVICE_EXISTS", "device is already registered with different credentials");
    }
    if (path === "/extension") return this.openWebSocket();
    if (path === "/clients" && request.method === "POST") return this.createClient(request);
    if (path.startsWith("/clients/") && request.method === "DELETE") return this.revokeClient(request, decodeURIComponent(path.slice(9)));
    if (path === "/device" && request.method === "DELETE") return this.revokeDevice(request);
    if (path === "/admin-revoke" && request.method === "POST") return this.revokeDeviceRecord();
    if (path === "/trigger" && request.method === "POST") return this.trigger(request);
    return error(404, "NOT_FOUND", "endpoint not found");
  }

  private openWebSocket(): Response {
    for (const existing of this.ctx.getWebSockets("extension")) {
      try {
        if (!(existing.deserializeAttachment() as SocketAttachment | null)?.authenticated) existing.close(1008, "replaced unauthenticated connection");
      } catch { existing.close(1008, "invalid connection"); }
    }
    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);
    this.ctx.acceptWebSocket(server, ["extension"]);
    server.serializeAttachment({ authenticated: false } satisfies SocketAttachment);
    return new Response(null, { status: 101, webSocket: client });
  }

  private async createClient(request: Request): Promise<Response> {
    if (!await this.ownerAuthorized(request)) return error(401, "UNAUTHORIZED", "device authentication failed");
    const body = await request.json<JsonObject>();
    const keyId = typeof body.keyId === "string" ? body.keyId : "";
    const clientToken = typeof body.clientToken === "string" ? body.clientToken : "";
    if (!KEY_RE.test(keyId) || clientToken.length < 32) return error(400, "INVALID_CLIENT", "client fields are invalid");
    const clients = await this.ctx.storage.list({ prefix: "client:" });
    const limit = Math.max(1, Number(this.env.MAX_CLIENTS_PER_DEVICE || 20));
    if (!clients.has(`client:${keyId}`) && clients.size >= limit) return error(409, "CLIENT_LIMIT", "client limit reached");
    if (clients.has(`client:${keyId}`)) return error(409, "CLIENT_EXISTS", "client already exists");
    const relayGrant = randomToken();
    await this.ctx.storage.put(`client:${keyId}`, {
      tokenHash: await digest(clientToken), grantHash: await digest(relayGrant), revoked: false
    });
    return json({ ok: true, keyId, relayGrant });
  }

  private async revokeClient(request: Request, keyId: string): Promise<Response> {
    if (!await this.ownerAuthorized(request)) return error(401, "UNAUTHORIZED", "device authentication failed");
    await this.ctx.storage.delete(`client:${keyId}`);
    return json({ ok: true });
  }

  private async revokeDevice(request: Request): Promise<Response> {
    if (!await this.ownerAuthorized(request)) return error(401, "UNAUTHORIZED", "device authentication failed");
    return this.revokeDeviceRecord();
  }

  private async revokeDeviceRecord(): Promise<Response> {
    const record = await this.record();
    if (record) await this.ctx.storage.put("device", { ...record, revoked: true });
    for (const ws of this.ctx.getWebSockets("extension")) ws.close(1008, "device revoked");
    return json({ ok: true });
  }

  private async clientAuthorized(request: Request, keyId: string): Promise<boolean> {
    const client = await this.ctx.storage.get<{ tokenHash: string; grantHash: string; revoked?: boolean }>(`client:${keyId}`);
    if (!client || client.revoked) return false;
    return await digest(bearer(request)) === client.tokenHash
      && await digest(request.headers.get("x-tt-relay-grant") || "") === client.grantHash;
  }

  private async rateAllowed(): Promise<boolean> {
    const now = Date.now();
    const window = await this.ctx.storage.get<{ start: number; count: number }>("rate");
    const limit = Math.max(1, Number(this.env.RATE_LIMIT_PER_MINUTE || 60));
    const burst = Math.max(0, Number(this.env.RATE_LIMIT_BURST || 10));
    const current = !window || now - window.start >= 60_000 ? { start: now, count: 0 } : window;
    if (current.count >= limit + burst) return false;
    current.count += 1;
    await this.ctx.storage.put("rate", current);
    return true;
  }

  private async trigger(request: Request): Promise<Response> {
    const keyId = request.headers.get("x-tt-key-id") || "";
    if (!KEY_RE.test(keyId) || !await this.clientAuthorized(request, keyId)) return error(401, "UNAUTHORIZED", "client authentication failed");
    if (!await this.rateAllowed()) return error(429, "RATE_LIMITED", "request rate limit exceeded", );
    if (this.pending.size > 0) return error(429, "DEVICE_BUSY", "device is executing another request");
    const socket = this.ctx.getWebSockets("extension").find((ws) => {
      try { return (ws.deserializeAttachment() as SocketAttachment | null)?.authenticated === true; } catch { return false; }
    });
    if (!socket) return error(503, "DEVICE_OFFLINE", "browser extension is offline");

    const rawBody = await request.text();
    let requestId = "";
    try {
      const body = JSON.parse(rawBody) as JsonObject;
      requestId = typeof body.requestId === "string" ? body.requestId : "";
    } catch { return error(400, "INVALID_JSON", "request body is invalid"); }
    if (!ID_RE.test(requestId.replace(/-/g, "")) && !/^[0-9a-f-]{36}$/i.test(requestId)) return error(400, "INVALID_REQUEST_ID", "requestId is invalid");

    const forwardedHeaders: Record<string, string> = {};
    for (const name of ["x-tt-key-id", "x-tt-timestamp", "x-tt-nonce", "x-tt-signature", "x-tt-device-id"]) {
      forwardedHeaders[name] = request.headers.get(name) || "";
    }
    const timeoutMs = Math.max(1000, Math.min(60_000, Number(request.headers.get("x-tt-request-timeout-ms") || 10000)));
    return new Promise<Response>((resolve) => {
      const timer = setTimeout(() => {
        this.pending.delete(requestId);
        resolve(error(504, "DEVICE_TIMEOUT", "browser extension did not respond in time"));
      }, timeoutMs);
      this.pending.set(requestId, { resolve, timer });
      try {
        socket.send(JSON.stringify({ type: "trigger", id: requestId, headers: forwardedHeaders, body: rawBody }));
      } catch {
        clearTimeout(timer);
        this.pending.delete(requestId);
        resolve(error(503, "DEVICE_OFFLINE", "browser extension is offline"));
      }
    });
  }

  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    if (typeof message !== "string") return;
    let value: JsonObject;
    try { value = JSON.parse(message) as JsonObject; } catch { return; }
    if (value.type === "ping") { ws.send(JSON.stringify({ type: "pong" })); return; }
    const attachment = (ws.deserializeAttachment() as SocketAttachment | null) || {};
    if (!attachment.authenticated) {
      if (value.type !== "auth" || typeof value.token !== "string" || typeof value.grant !== "string") {
        ws.close(1008, "authentication required"); return;
      }
      const record = await this.record();
      const ok = Boolean(record && !record.revoked
        && await digest(value.token) === record.deviceTokenHash
        && await digest(value.grant) === record.deviceGrantHash);
      if (!ok) { ws.send(JSON.stringify({ type: "auth_result", ok: false })); ws.close(1008, "authentication failed"); return; }
      for (const existing of this.ctx.getWebSockets("extension")) {
        if (existing !== ws) existing.close(1000, "replaced by a new connection");
      }
      ws.serializeAttachment({ authenticated: true } satisfies SocketAttachment);
      ws.send(JSON.stringify({ type: "auth_result", ok: true }));
      return;
    }
    if (value.type === "result" && typeof value.id === "string") {
      const pending = this.pending.get(value.id);
      if (!pending) return;
      clearTimeout(pending.timer);
      this.pending.delete(value.id);
      const response = value.response as JsonObject | undefined;
      if (!response || typeof response.body !== "string" || !response.headers || typeof response.headers !== "object") {
        pending.resolve(error(502, "INVALID_EXTENSION_RESPONSE", "browser extension returned an invalid response")); return;
      }
      const headers = new Headers({ "content-type": "application/json", "cache-control": "no-store" });
      for (const [name, headerValue] of Object.entries(response.headers as Record<string, unknown>)) {
        if (["x-tt-key-id", "x-tt-timestamp", "x-tt-nonce", "x-tt-signature"].includes(name.toLowerCase()) && typeof headerValue === "string") headers.set(name, headerValue);
      }
      pending.resolve(new Response(response.body, { status: 200, headers }));
    }
  }

  async webSocketClose(_ws: WebSocket): Promise<void> {
    for (const [id, pending] of this.pending) {
      clearTimeout(pending.timer);
      pending.resolve(error(503, "DEVICE_OFFLINE", "browser extension disconnected"));
      this.pending.delete(id);
    }
  }
}
