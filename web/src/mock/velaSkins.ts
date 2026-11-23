export type MockVelaSkin = {
  id: string;
  name: string;
  version: string;
  kind: "pack" | "document";
  builtin: boolean;
  preview?: string;
  swatches: string[];
  installedAt?: number;
};

function previewDataUri(canvas: string, surface: string, accent: string, ink: string): string {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 320 180"><rect width="320" height="180" fill="${canvas}"/><rect x="18" y="16" width="284" height="72" rx="10" fill="${surface}"/><rect x="30" y="30" width="96" height="12" rx="4" fill="${ink}" fill-opacity="0.9"/><rect x="30" y="48" width="148" height="8" rx="4" fill="${ink}" fill-opacity="0.42"/><rect x="30" y="64" width="46" height="14" rx="7" fill="${accent}"/><rect x="18" y="102" width="64" height="62" rx="8" fill="${surface}"/><rect x="91" y="102" width="64" height="62" rx="8" fill="${surface}"/><rect x="164" y="102" width="64" height="62" rx="8" fill="${surface}"/><rect x="237" y="102" width="65" height="62" rx="8" fill="${surface}"/><rect x="18" y="102" width="64" height="4" rx="2" fill="${accent}"/></svg>`;
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`;
}

const INITIAL_SKINS: MockVelaSkin[] = [
  {
    id: "cinematic-doc",
    name: "Cinematic",
    version: "1.0.0",
    kind: "document",
    builtin: true,
    swatches: ["#0b0d12", "#151923", "#e5a00d", "#f4f6fb"],
  },
  {
    id: "mochi",
    name: "Mochi",
    version: "1.0.0",
    kind: "document",
    builtin: true,
    swatches: ["#faf6f2", "#ffffff", "#e86a92", "#26222b"],
  },
  {
    id: "marquee",
    name: "Marquee",
    version: "1.0.0",
    kind: "document",
    builtin: true,
    swatches: ["#120c10", "#22151c", "#f0544f", "#f9f1ed"],
  },
  {
    id: "jellytwin",
    name: "Jelly Twin",
    version: "1.0.0",
    kind: "document",
    builtin: true,
    swatches: ["#101018", "#1c1d2b", "#00a4dc", "#eef1f8"],
  },
  {
    id: "index",
    name: "Index",
    version: "1.0.0",
    kind: "document",
    builtin: true,
    swatches: ["#f4f3ef", "#ffffff", "#1d4ed8", "#161618"],
  },
  {
    id: "lounge",
    name: "Lounge",
    version: "1.0.0",
    kind: "document",
    builtin: true,
    swatches: ["#161311", "#241f1b", "#d99b52", "#f5efe8"],
  },
  {
    id: "latenight",
    name: "Late Night",
    version: "1.0.0",
    kind: "document",
    builtin: true,
    swatches: ["#07090f", "#101420", "#7c6cf6", "#e6eaf5"],
  },
  {
    id: "kids",
    name: "Kids",
    version: "1.0.0",
    kind: "document",
    builtin: true,
    swatches: ["#eef7ff", "#ffffff", "#ff8a3d", "#182438"],
  },
  {
    id: "broadcast",
    name: "Broadcast",
    version: "1.0.0",
    kind: "document",
    builtin: true,
    swatches: ["#0c1118", "#151e2b", "#2dd4bf", "#edf2f7"],
  },
  {
    id: "harbor",
    name: "Harbor",
    version: "1.0.0",
    kind: "pack",
    builtin: false,
    preview: previewDataUri("#0b1320", "#132238", "#4cc9f0", "#eef4fb"),
    swatches: ["#0b1320", "#132238", "#4cc9f0", "#eef4fb"],
    installedAt: 1731900000,
  },
  {
    id: "studio-grid",
    name: "Studio Grid",
    version: "1.2.0",
    kind: "document",
    builtin: false,
    preview: previewDataUri("#121316", "#1d2026", "#f59e0b", "#f3f4f6"),
    swatches: ["#121316", "#1d2026", "#f59e0b", "#f3f4f6"],
    installedAt: 1731903600,
  },
];

function isPrivateHost(host: string): boolean {
  const h = host.toLowerCase().replace(/^\[|\]$/g, "");
  if (h === "localhost" || h === "127.0.0.1" || h === "::1" || h.endsWith(".local")) return true;
  if (/^10\.\d+\.\d+\.\d+$/.test(h)) return true;
  if (/^192\.168\.\d+\.\d+$/.test(h)) return true;
  const m172 = /^172\.(\d+)\.\d+\.\d+$/.exec(h);
  if (m172 && Number(m172[1]) >= 16 && Number(m172[1]) <= 31) return true;
  return false;
}

function isLinkLocalHost(host: string): boolean {
  const h = host.toLowerCase().replace(/^\[|\]$/g, "");
  return /^169\.254\.\d+\.\d+$/.test(h) || h.startsWith("fe80:") || h === "0.0.0.0";
}

function deriveSkinFromSlug(rawName: string): { id: string; name: string } | null {
  const base = rawName.replace(/\.vskin$/i, "").trim();
  const slug = base
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "");
  if (slug.length < 2) return null;
  const name = slug
    .split("-")
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
  return { id: slug.slice(0, 48), name: name || slug };
}

export function createVelaSkinsMock(onRemoved?: (id: string) => void) {
  let skins: MockVelaSkin[] = INITIAL_SKINS.map((s) => ({ ...s, swatches: [...s.swatches] }));

  function upsertInstalled(id: string, name: string): { status: number; body: unknown } {
    if (skins.some((s) => s.builtin && (s.id === id || id === "cinematic"))) {
      return {
        status: 400,
        body: {
          error: {
            code: "builtin_skin",
            message: "Built-in skins cannot be modified or removed.",
          },
        },
      };
    }
    const existing = skins.find((s) => s.id === id);
    const nextVersion = existing ? "1.1.0" : "1.0.0";
    const entry: MockVelaSkin = {
      id,
      name,
      version: nextVersion,
      kind: "pack",
      builtin: false,
      preview: previewDataUri("#101826", "#1a2740", "#38bdf8", "#f1f5f9"),
      swatches: ["#101826", "#1a2740", "#38bdf8", "#f1f5f9"],
      installedAt: Math.floor(Date.now() / 1000),
    };
    skins = [...skins.filter((s) => s.id !== id), entry];
    return { status: 201, body: entry };
  }

  return {
    handle(
      method: string,
      path: string,
      body: unknown,
    ): { status: number; body: unknown } | undefined {
      if (path === "/vela/skins" && method === "GET") {
        return { status: 200, body: { skins } };
      }
      if (path === "/vela/skins" && method === "POST") {
        if (body instanceof FormData) {
          const part = body.get("file") ?? body.get("package");
          if (!(part instanceof File)) {
            return {
              status: 400,
              body: { error: { code: "bad_upload", message: "Select a .vskin file to upload." } },
            };
          }
          if (part.size > 8 * 1024 * 1024) {
            return {
              status: 400,
              body: {
                error: { code: "too_large", message: "Skin archive exceeds the 8 MB limit." },
              },
            };
          }
          if (!/\.vskin$/i.test(part.name)) {
            return {
              status: 400,
              body: {
                error: {
                  code: "invalid_skin",
                  message: "Skin package must be a valid .vskin archive.",
                },
              },
            };
          }
          const derived = deriveSkinFromSlug(part.name);
          if (!derived) {
            return {
              status: 400,
              body: { error: { code: "invalid_skin", message: "Invalid skin identifier." } },
            };
          }
          return upsertInstalled(derived.id, derived.name);
        }
        const payload = (body ?? {}) as { url?: unknown; lan?: unknown };
        const rawUrl = typeof payload.url === "string" ? payload.url.trim() : "";
        const lan = payload.lan === true;
        let parsed: URL;
        try {
          parsed = new URL(rawUrl);
        } catch {
          return {
            status: 400,
            body: {
              error: {
                code: "invalid_url",
                message: "Enter a valid http:// or https:// .vskin URL.",
              },
            },
          };
        }
        if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
          return {
            status: 400,
            body: {
              error: {
                code: "invalid_url",
                message: "Enter a valid http:// or https:// .vskin URL.",
              },
            },
          };
        }
        if (isLinkLocalHost(parsed.hostname)) {
          return {
            status: 400,
            body: {
              error: {
                code: "ssrf_blocked",
                message: "Link-local and metadata addresses are not allowed.",
              },
            },
          };
        }
        if (isPrivateHost(parsed.hostname) && !lan) {
          return {
            status: 400,
            body: {
              error: {
                code: "ssrf_blocked",
                message: "Private-network URLs are blocked unless local network access is allowed.",
              },
            },
          };
        }
        const fileSegment = parsed.pathname.split("/").filter(Boolean).pop() ?? "";
        if (!/\.vskin$/i.test(fileSegment)) {
          return {
            status: 400,
            body: {
              error: {
                code: "invalid_skin",
                message: "skin.json is missing from the .vskin archive.",
              },
            },
          };
        }
        const derived = deriveSkinFromSlug(fileSegment);
        if (!derived) {
          return {
            status: 400,
            body: { error: { code: "invalid_skin", message: "Invalid skin identifier." } },
          };
        }
        return upsertInstalled(derived.id, derived.name);
      }
      if (path.startsWith("/vela/skins/") && method === "DELETE") {
        const id = decodeURIComponent(path.slice("/vela/skins/".length));
        const target = skins.find((s) => s.id === id);
        if (id === "cinematic" || target?.builtin) {
          return {
            status: 400,
            body: {
              error: {
                code: "builtin_skin",
                message: "Built-in skins cannot be modified or removed.",
              },
            },
          };
        }
        if (!target) {
          return {
            status: 404,
            body: { error: { code: "not_found", message: "Skin not found." } },
          };
        }
        skins = skins.filter((s) => s.id !== id);
        onRemoved?.(id);
        return { status: 200, body: { ok: true } };
      }
      return undefined;
    },
  };
}
