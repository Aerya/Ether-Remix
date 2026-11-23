// Smart lights example plugin for Ether Flows.
//
// Demonstrates:
//   - Subscribing to canonical Ether events (playback.session.started / stopped)
//   - Publishing schema-validated custom events (scene.changed) via ether.events.publish
//   - Contributing an idempotent Flow action (set_scene) that respects X-Ether-Idempotency-Key and dryRun

interface PluginRequest {
  method?: string;
  path: string;
  headers?: Record<string, string>;
  body?: unknown;
}

interface PluginResponse {
  status: number;
  headers?: Record<string, string>;
  body: unknown;
}

interface FlowActionBody {
  flowId?: string;
  runId?: string;
  nodeId?: string;
  idempotencyKey?: string;
  dryRun?: boolean;
  params?: Record<string, unknown>;
}

interface RoomState {
  room: string;
  scene: string;
  brightness: number;
}

interface HostedEther {
  kv: {
    get(key: string): string | null;
    put(key: string, value: string): void;
  };
  settings: {
    get(): Record<string, unknown> | null | undefined;
  };
  events: {
    publish(
      key: string,
      payload?: Record<string, unknown>,
      correlation?: Record<string, unknown>,
    ): void;
  };
}

declare const ether: HostedEther;

const VALID_SCENES: Record<string, boolean> = {
  cinema: true,
  warm: true,
  bright: true,
  off: true,
};

function parseBody(raw: unknown): Record<string, unknown> {
  if (!raw) return {};
  if (typeof raw === "string") {
    try {
      const parsed = JSON.parse(raw);
      return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : {};
    } catch {
      return {};
    }
  }
  return typeof raw === "object" ? (raw as Record<string, unknown>) : {};
}

function getHeader(headers: Record<string, string> | undefined, name: string): string {
  if (!headers) return "";
  const target = name.toLowerCase();
  for (const key of Object.keys(headers)) {
    if (key.toLowerCase() === target) {
      return String(headers[key] ?? "");
    }
  }
  return "";
}

function readSettings(): { defaultRoom: string; defaultBrightness: number } {
  const s = ether.settings.get() ?? {};
  const room = typeof s.defaultRoom === "string" && s.defaultRoom.trim() ? s.defaultRoom.trim() : "Living room";
  const rawBrightness = Number(s.defaultBrightness);
  const brightness = Number.isFinite(rawBrightness) ? Math.max(0, Math.min(100, Math.round(rawBrightness))) : 25;
  return { defaultRoom: room, defaultBrightness: brightness };
}

function readRoomState(room: string): RoomState {
  const saved = ether.kv.get("room:" + room.toLowerCase());
  if (saved) {
    try {
      const parsed = JSON.parse(saved) as Partial<RoomState>;
      if (parsed && typeof parsed.scene === "string") {
        return {
          room,
          scene: parsed.scene,
          brightness: typeof parsed.brightness === "number" ? parsed.brightness : 100,
        };
      }
    } catch {
      // Ignore corrupt KV value and fall back to default.
    }
  }
  return { room, scene: "bright", brightness: 100 };
}

function applyScene(
  room: string,
  scene: string,
  brightness: number,
  dryRun: boolean,
  emitEvent: boolean,
  correlation?: Record<string, unknown>,
): Record<string, unknown> {
  const prev = readRoomState(room);
  const clamped = scene === "off" ? 0 : Math.max(0, Math.min(100, Math.round(brightness)));
  if (!dryRun) {
    ether.kv.put(
      "room:" + room.toLowerCase(),
      JSON.stringify({ room, scene, brightness: clamped }),
    );
    if (emitEvent) {
      ether.events.publish(
        "scene.changed",
        {
          room,
          scene,
          brightness: clamped,
          previousScene: prev.scene,
        },
        correlation,
      );
    }
  }
  return {
    room,
    scene,
    previousScene: prev.scene,
    brightness: clamped,
    applied: !dryRun,
    deduplicated: false,
  };
}

function handleSetScene(req: PluginRequest): PluginResponse {
  const body = parseBody(req.body) as FlowActionBody;
  const params = body.params && typeof body.params === "object" ? body.params : {};
  const defaults = readSettings();

  const room =
    typeof params.room === "string" && params.room.trim()
      ? params.room.trim()
      : defaults.defaultRoom;
  const scene = typeof params.scene === "string" ? params.scene.trim().toLowerCase() : "";
  if (!VALID_SCENES[scene]) {
    return {
      status: 400,
      body: { error: { code: "invalid_scene", message: "Scene must be cinema, warm, bright, or off." } },
    };
  }
  const brightness =
    params.brightness !== undefined && Number.isFinite(Number(params.brightness))
      ? Number(params.brightness)
      : defaults.defaultBrightness;
  const dryRun = Boolean(body.dryRun);
  const idemKey = (getHeader(req.headers, "X-Ether-Idempotency-Key") || String(body.idempotencyKey ?? "")).trim();

  if (!dryRun && idemKey) {
    const cached = ether.kv.get("idem:" + idemKey);
    if (cached) {
      try {
        const prevOutput = JSON.parse(cached) as Record<string, unknown>;
        return {
          status: 200,
          body: { output: { ...prevOutput, deduplicated: true } },
        };
      } catch {
        // Re-execute if cached value cannot be parsed.
      }
    }
  }

  const output = applyScene(room, scene, brightness, dryRun, true, { key: "room:" + room.toLowerCase() });
  if (!dryRun && idemKey) {
    ether.kv.put("idem:" + idemKey, JSON.stringify(output));
  }
  return {
    status: 200,
    body: { output },
  };
}

function handleEventBatch(req: PluginRequest): PluginResponse {
  const body = parseBody(req.body);
  const events = Array.isArray(body.events) ? (body.events as Array<Record<string, unknown>>) : [];
  const defaults = readSettings();
  const ack: string[] = [];

  for (const ev of events) {
    const id = typeof ev.id === "string" ? ev.id : "";
    if (id) ack.push(id);
    const name = typeof ev.name === "string" ? ev.name : "";
    if (name === "playback.session.started") {
      applyScene(defaults.defaultRoom, "cinema", defaults.defaultBrightness, false, true);
    } else if (name === "playback.session.stopped") {
      applyScene(defaults.defaultRoom, "warm", 70, false, true);
    }
  }

  return { status: 200, body: { ack } };
}

function handle(req: PluginRequest): PluginResponse {
  const method = (req.method ?? "GET").toUpperCase();
  if (method === "GET" && req.path === "/ether/health") {
    return { status: 200, body: { status: "ok" } };
  }
  if (method === "POST" && req.path === "/ether/configure") {
    return { status: 200, body: { errors: {} } };
  }
  if (method === "POST" && req.path === "/ether/event") {
    return handleEventBatch(req);
  }
  if (method === "POST" && req.path === "/ether/v1/flow-actions/set_scene") {
    return handleSetScene(req);
  }
  return {
    status: 404,
    body: { error: { code: "not_found", message: "Route not found." } },
  };
}

(globalThis as unknown as { handle: typeof handle }).handle = handle;
