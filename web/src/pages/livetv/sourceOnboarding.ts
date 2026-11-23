const M3U_HEADER_ATTR = /([a-zA-Z0-9_-]+)=(?:"([^"]*)"|'([^']*)')/g;
const REDACTED_SECRET = "••••";
const XTREAM_PATH_FAMILIES = new Set(["live", "movie", "series", "timeshift"]);

export interface XtreamCredentials {
  server: string;
  username: string;
  password: string;
  output?: "ts" | "m3u8";
}

function isHttpUrl(raw: string): boolean {
  try {
    const u = new URL(raw);
    return (u.protocol === "http:" || u.protocol === "https:") && Boolean(u.hostname);
  } catch {
    return false;
  }
}

function decodeSeg(seg: string | undefined): string | null {
  if (seg === undefined) return null;
  try {
    return decodeURIComponent(seg);
  } catch {
    return null;
  }
}

function dropPhpTail(segs: string[]): string[] {
  const last = segs[segs.length - 1];
  return last && last.toLowerCase().endsWith(".php") ? segs.slice(0, -1) : segs;
}

function validCreds(username: string | null, password: string | null): username is string {
  return Boolean(
    username && password && username !== REDACTED_SECRET && password !== REDACTED_SECRET,
  );
}

function outputFromParam(raw: string | null): "ts" | "m3u8" | undefined {
  switch ((raw ?? "").trim().toLowerCase()) {
    case "m3u8":
    case "hls":
      return "m3u8";
    case "ts":
    case "mpegts":
      return "ts";
    default:
      return undefined;
  }
}

function outputFromSegment(seg: string | undefined): "ts" | "m3u8" | undefined {
  const lower = (seg ?? "").toLowerCase();
  if (lower.endsWith(".m3u8")) return "m3u8";
  if (lower.endsWith(".ts")) return "ts";
  return undefined;
}

function queryParam(params: URLSearchParams, name: string): string | null {
  const exact = params.get(name);
  if (exact !== null) return exact;
  const target = name.toLowerCase();
  for (const [k, v] of params.entries()) {
    if (k.toLowerCase() === target) return v;
  }
  return null;
}

function joinServer(origin: string, segs: string[]): string {
  return segs.length ? `${origin}/${segs.join("/")}` : origin;
}

/**
 * Splits a full Xtream provider URL (get.php, player_api.php, xmltv.php,
 * /live/U/P/…, or /U/P/…) into clean server origin/base path, username,
 * password, and optional stream output format.
 */
export function parseXtreamUrl(raw: string): XtreamCredentials | null {
  const trimmed = raw.trim();
  if (!trimmed) return null;
  let u: URL;
  try {
    u = new URL(trimmed);
  } catch {
    return null;
  }
  if ((u.protocol !== "http:" && u.protocol !== "https:") || !u.hostname) return null;

  const origin = `${u.protocol.toLowerCase()}//${u.host}`;
  const rawSegs = u.pathname.split("/").filter(Boolean);
  const qUser = queryParam(u.searchParams, "username");
  const qPass = queryParam(u.searchParams, "password");
  const qOut = outputFromParam(queryParam(u.searchParams, "output"));

  if (validCreds(qUser, qPass) && qPass) {
    return {
      server: joinServer(origin, dropPhpTail(rawSegs)),
      username: qUser,
      password: qPass,
      ...(qOut ? { output: qOut } : {}),
    };
  }

  for (let i = 0; i + 3 <= rawSegs.length; i++) {
    const family = rawSegs[i];
    if (!family || !XTREAM_PATH_FAMILIES.has(family.toLowerCase())) continue;
    const username = decodeSeg(rawSegs[i + 1]);
    const password = decodeSeg(rawSegs[i + 2]);
    if (!validCreds(username, password) || !password) return null;
    const out = qOut ?? outputFromSegment(rawSegs[i + 3] ? rawSegs[rawSegs.length - 1] : undefined);
    return {
      server: joinServer(origin, rawSegs.slice(0, i)),
      username,
      password,
      ...(out ? { output: out } : {}),
    };
  }

  const lastSeg = rawSegs[rawSegs.length - 1];
  if (rawSegs.length >= 3 && lastSeg && !lastSeg.toLowerCase().endsWith(".php")) {
    const username = decodeSeg(rawSegs[0]);
    const password = decodeSeg(rawSegs[1]);
    if (validCreds(username, password) && password) {
      const out = qOut ?? outputFromSegment(lastSeg);
      return {
        server: origin,
        username,
        password,
        ...(out ? { output: out } : {}),
      };
    }
  }

  if (u.username && u.password) {
    const username = decodeSeg(u.username);
    const password = decodeSeg(u.password);
    if (validCreds(username, password) && password) {
      return {
        server: joinServer(origin, dropPhpTail(rawSegs)),
        username,
        password,
        ...(qOut ? { output: qOut } : {}),
      };
    }
  }

  return null;
}

/** Strips query parameters, fragments, userinfo, and trailing .php endpoints from an Xtream server URL. */
export function stripXtreamServerUrl(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return "";
  let u: URL;
  try {
    u = new URL(trimmed);
  } catch {
    return trimmed;
  }
  if ((u.protocol !== "http:" && u.protocol !== "https:") || !u.hostname) return trimmed;
  const origin = `${u.protocol.toLowerCase()}//${u.host}`;
  const rawSegs = u.pathname.split("/").filter(Boolean);
  return joinServer(origin, dropPhpTail(rawSegs));
}

/** Reports whether an M3U document's #EXTM3U header carries x-tvg-url or url-tvg. */
export function hasM3UHeaderGuide(raw: string): boolean {
  for (const rawLine of raw.split(/\r?\n/)) {
    const line = rawLine.replace(/^\ufeff/, "").trim();
    if (!line) continue;
    if (!line.startsWith("#EXTM3U")) return false;
    M3U_HEADER_ATTR.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = M3U_HEADER_ATTR.exec(line)) !== null) {
      const key = (m[1] ?? "").toLowerCase();
      if (key !== "x-tvg-url" && key !== "url-tvg") continue;
      const val = m[2] ?? m[3] ?? "";
      for (const part of val.split(",")) {
        const candidate = part.trim();
        if (candidate && isHttpUrl(candidate)) return true;
      }
    }
    return false;
  }
  return false;
}
