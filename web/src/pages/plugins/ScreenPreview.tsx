import { Badge } from "@braedonsaunders/appkit-ui";
import { Segmented } from "../../components/fields";
import { useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";

/**
 * Studio's live preview of a draft's screens and surfaces: the real Vela web
 * app, muted, in a device frame, drawing the working copy's documents on real
 * data. Ether compiles the draft into a preview token on every save; Vela
 * (signed in with a one-time web code) sends the token on its screen calls
 * and reloads the document when told.
 */

interface PreviewInfo {
  token: string;
  expires: string;
  screens?: { id: string; title: string; pages: string[] }[];
  surfaces?: { id: string; presentation: string }[];
}

interface Diagnostic {
  field: string;
  message: string;
}

type Device = "tv" | "tablet" | "phone";

const DEVICES: Record<Device, { width: number; height: number }> = {
  tv: { width: 1920, height: 1080 },
  tablet: { width: 1180, height: 820 },
  phone: { width: 390, height: 844 },
};

export function usesScreens(manifest?: string): boolean {
  if (!manifest) return false;
  try {
    const m = JSON.parse(manifest) as {
      ether?: { contributes?: { screens?: { pages?: unknown }[]; surfaces?: unknown[] } };
    };
    const c = m.ether?.contributes;
    return Boolean(c?.screens?.some((s) => s.pages) || c?.surfaces?.length);
  } catch {
    return false;
  }
}

export function ScreenPreview({ projectId, epoch }: { projectId: string; epoch?: number }) {
  const [info, setInfo] = useState<PreviewInfo | null>(null);
  const [diagnostics, setDiagnostics] = useState<Diagnostic[]>([]);
  const [device, setDevice] = useState<Device>("tv");
  const [target, setTarget] = useState("");
  const [src, setSrc] = useState("");
  const token = useRef("");
  const frame = useRef<HTMLIFrameElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const [scale, setScale] = useState(0.4);

  // Compile on open and on every save; keep one token so the open preview
  // only reloads its documents.
  useEffect(() => {
    let live = true;
    apiFetch<PreviewInfo>(
      `/plugins/studio/projects/${encodeURIComponent(projectId)}/screen-preview`,
      {
        method: "POST",
        body: JSON.stringify({ token: token.current }),
      },
    )
      .then((res) => {
        if (!live) return;
        setDiagnostics([]);
        const reused = token.current === res.token;
        token.current = res.token;
        setInfo(res);
        if (reused) {
          const src = frame.current?.src;
          if (src) {
            frame.current?.contentWindow?.postMessage(
              { type: "vela:screen-preview:reload" },
              new URL(src, window.location.href).origin,
            );
          }
        } else {
          setTarget((cur) => cur || (res.screens?.[0] ? `screen:${res.screens[0].id}` : ""));
        }
      })
      .catch((err: unknown) => {
        if (!live) return;
        const body = (err as { body?: { diagnostics?: Diagnostic[] } }).body;
        setDiagnostics(
          body?.diagnostics ?? [{ field: "", message: t("The preview could not start.") }],
        );
      });
    return () => {
      live = false;
    };
  }, [projectId, epoch]);

  // Sign the frame in once per target with a one-time web code.
  useEffect(() => {
    if (!info || !target) return;
    let live = true;
    apiFetch<{ url: string }>("/web-player?app=vela", { method: "POST", body: "{}" })
      .then(({ url }) => {
        if (!live) return;
        const [base, code] = url.split("#code=");
        const root = (base ?? "").replace(/\/+$/, "");
        const [kind, id] = target.split(":");
        const q = `screenPreview=${encodeURIComponent(info.token)}`;
        const path = kind === "screen" ? `/screen/${encodeURIComponent(id ?? "")}?${q}` : `/?${q}`;
        setSrc(`${root}${path}${code ? `#code=${code}` : ""}`);
      })
      .catch(() => live && setSrc(""));
    return () => {
      live = false;
    };
    // The token stays the same across saves; only a new target re-signs.
  }, [info?.token, target]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const fit = () => setScale(Math.min(1, el.clientWidth / DEVICES[device].width));
    fit();
    const ro = new ResizeObserver(fit);
    ro.observe(el);
    return () => ro.disconnect();
  }, [device]);

  const size = DEVICES[device];
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="info">{t("Live preview")}</Badge>
          {info?.screens && info.screens.length > 1 ? (
            <select
              aria-label={t("Screen")}
              className="rounded-md border border-border bg-surface px-2 py-1 text-sm"
              value={target}
              onChange={(e) => setTarget(e.target.value)}
            >
              {info.screens.map((s) => (
                <option key={s.id} value={`screen:${s.id}`}>
                  {s.title || s.id}
                </option>
              ))}
            </select>
          ) : null}
        </div>
        <Segmented
          size="sm"
          ariaLabel={t("Device")}
          value={device}
          onChange={(v) => setDevice(v as Device)}
          options={[
            { value: "tv", label: t("TV") },
            { value: "tablet", label: t("Tablet") },
            { value: "phone", label: t("Phone") },
          ]}
        />
      </div>
      {diagnostics.length > 0 ? (
        <ul className="space-y-1 rounded-lg border border-danger/40 bg-danger/5 px-3 py-2 text-sm">
          {diagnostics.map((d, i) => (
            <li key={i}>
              {d.field ? <code className="mr-2 text-xs text-fg-muted">{d.field}</code> : null}
              {d.message}
            </li>
          ))}
        </ul>
      ) : null}
      <div ref={box} className="w-full overflow-hidden rounded-xl border border-border bg-black">
        <div style={{ width: size.width * scale, height: size.height * scale }} className="mx-auto">
          {src ? (
            <iframe
              ref={frame}
              title={t("Vela preview")}
              src={src}
              allow="autoplay"
              style={{
                width: size.width,
                height: size.height,
                transform: `scale(${scale})`,
                transformOrigin: "0 0",
                border: 0,
              }}
            />
          ) : null}
        </div>
      </div>
    </div>
  );
}
