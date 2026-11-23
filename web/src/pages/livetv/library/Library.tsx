import { useEffect, useId, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Button, Checkbox, Input, cn, confirmDialog } from "@braedonsaunders/appkit-ui";
import {
  AudioLines,
  Film,
  Image,
  LayoutGrid,
  Type,
  Play,
  Pause,
  RotateCcw,
  Trash2,
  Volume2,
} from "lucide-react";
import { SubtabNav } from "../../../components/layout";
import { Drawer } from "../../../components/Drawer";
import { ToolbarPicker } from "../../../components/ToolbarPicker";
import { Segmented } from "../../../components/fields";
import { t, tn } from "../../../i18n";
import { apiBlob, apiFetch, sessionXhrSetup, errorMessage } from "../../../lib/api";
import { formatBytes } from "../../../lib/format";
import { studio, type LibraryAsset, type AssetKind } from "../onair/studio-model";
import { onair } from "../onair/model";
import { LinkImport } from "../onair/LinkImport";
import { friendlyMediaError } from "../onair/mediaErrors";
import { useMediaURL } from "../onair/useMediaURL";

const LIBRARY_CHANGED_EVENT = "ether:library-changed";

type GridAsset = LibraryAsset & {
  readOnly?: boolean;
  url?: string;
  thumbnail?: string;
  reelId?: string;
  clipId?: string;
  link?: boolean;
  cached?: boolean;
};

function usePosterURL(url: string | undefined, enabled: boolean) {
  const [state, setState] = useState<{ src: string; failed: boolean }>({ src: "", failed: false });
  useEffect(() => {
    setState({ src: "", failed: false });
    if (!enabled || !url) return;
    if (!url.startsWith("/ether/api/")) {
      setState({ src: url, failed: false });
      return;
    }
    const abort = new AbortController();
    let objectURL = "";
    void apiBlob(url, abort.signal)
      .then((blob) => {
        if (abort.signal.aborted) return;
        if (!blob.size) {
          setState({ src: "", failed: true });
          return;
        }
        objectURL = URL.createObjectURL(blob);
        setState({ src: objectURL, failed: false });
      })
      .catch(() => {
        if (!abort.signal.aborted) setState({ src: "", failed: true });
      });
    return () => {
      abort.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
    };
  }, [url, enabled]);
  return state;
}

export function Waveform({ asset, progress = 0 }: { asset: LibraryAsset; progress?: number }) {
  const clip = useId().replace(/:/g, "");
  const [peaks, setPeaks] = useState<number[]>([]);
  useEffect(() => {
    let live = true;
    void studio
      .waveform(asset.digest)
      .then((v) => {
        if (live) setPeaks(v.peaks);
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, [asset.digest]);
  return (
    <svg
      viewBox="0 0 1000 160"
      className="h-24 w-full text-primary"
      role="img"
      aria-label={t("Audio waveform")}
    >
      <defs>
        <clipPath id={clip}>
          <rect width={Math.max(0, Math.min(1, progress)) * 1000} height="160" />
        </clipPath>
      </defs>
      <g opacity=".3">
        {peaks.map((p, i) => (
          <path
            key={i}
            d={`M${(i * 1000) / Math.max(1, peaks.length)} ${80 - p * 78}v${Math.max(1, p * 156)}`}
            stroke="currentColor"
          />
        ))}
      </g>
      <g clipPath={`url(#${clip})`}>
        {peaks.map((p, i) => (
          <path
            key={i}
            d={`M${(i * 1000) / Math.max(1, peaks.length)} ${80 - p * 78}v${Math.max(1, p * 156)}`}
            stroke="currentColor"
          />
        ))}
      </g>
    </svg>
  );
}
let activeAudio: HTMLAudioElement | null = null;
function AudioPreview({
  asset,
  source,
  full,
}: {
  asset: LibraryAsset;
  source: string;
  full: boolean;
}) {
  const audio = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false),
    [time, setTime] = useState(0),
    [duration, setDuration] = useState((asset.durationMs ?? 0) / 1000),
    [error, setError] = useState(false);
  useEffect(() => {
    const node = audio.current;
    return () => {
      node?.pause();
      if (activeAudio === node) activeAudio = null;
    };
  }, []);
  const seek = (next: number) => {
    if (audio.current && duration > 0) {
      audio.current.currentTime = Math.max(0, Math.min(duration, next));
      setTime(audio.current.currentTime);
    }
  };
  const toggle = () => {
    const node = audio.current;
    if (!node) return;
    if (playing) node.pause();
    else {
      if (activeAudio && activeAudio !== node) activeAudio.pause();
      activeAudio = node;
      if (node.ended) node.currentTime = 0;
      void node.play().catch(() => {
        setError(true);
        setPlaying(false);
      });
    }
  };
  const clock = (value: number) =>
    `${Math.floor(value / 60)}:${String(Math.floor(value % 60)).padStart(2, "0")}`;
  return (
    <div
      className="flex h-full min-h-32 flex-col justify-center gap-2 bg-gradient-to-br from-primary/5 to-transparent px-4 py-3"
      onClick={(e) => e.stopPropagation()}
    >
      <audio
        ref={audio}
        src={source || undefined}
        preload="metadata"
        className="hidden"
        aria-label={asset.name}
        onLoadedMetadata={(e) => {
          if (Number.isFinite(e.currentTarget.duration)) setDuration(e.currentTarget.duration);
        }}
        onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
        onPlay={() => {
          setPlaying(true);
          setError(false);
        }}
        onPause={() => setPlaying(false)}
        onEnded={() => setPlaying(false)}
      />
      <div className="relative overflow-hidden rounded-lg bg-primary/5">
        <div
          role="slider"
          tabIndex={duration > 0 ? 0 : -1}
          aria-label={t("Seek audio")}
          aria-valuemin={0}
          aria-valuemax={duration}
          aria-valuenow={Math.min(time, duration)}
          aria-valuetext={t("{elapsed} of {duration}", {
            elapsed: clock(time),
            duration: clock(duration),
          })}
          className="relative cursor-pointer px-2 outline-none focus-visible:ring-2 focus-visible:ring-primary"
          onPointerDown={(e) => {
            e.stopPropagation();
            const rect = e.currentTarget.getBoundingClientRect();
            seek(((e.clientX - rect.left) / rect.width) * duration);
            e.currentTarget.setPointerCapture(e.pointerId);
          }}
          onPointerMove={(e) => {
            if (e.currentTarget.hasPointerCapture(e.pointerId)) {
              const rect = e.currentTarget.getBoundingClientRect();
              seek(((e.clientX - rect.left) / rect.width) * duration);
            }
          }}
          onKeyDown={(e) => {
            if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) {
              e.preventDefault();
              seek(
                e.key === "Home"
                  ? 0
                  : e.key === "End"
                    ? duration
                    : time + (e.key === "ArrowRight" ? 5 : -5),
              );
            }
          }}
        >
          <div
            className="pointer-events-none absolute inset-y-0 left-0 bg-primary/10 transition-[width] duration-100"
            style={{ width: `${duration ? (time / duration) * 100 : 0}%` }}
          />
          <Waveform asset={asset} progress={duration ? time / duration : 0} />
        </div>
        <Button
          size="sm"
          className="absolute left-3 top-1/2 h-10 w-10 -translate-y-1/2 rounded-full p-0 shadow-lg"
          disabled={!source}
          aria-label={
            playing
              ? t("Pause {name}", { name: asset.name })
              : t("Play {name}", { name: asset.name })
          }
          onClick={toggle}
        >
          {playing ? <Pause size={18} /> : <Play size={18} fill="currentColor" />}
        </Button>
      </div>
      <div className="flex items-center justify-between gap-4 text-[10px] tabular-nums text-fg-muted">
        <span>{playing ? t("Playing") : t("Audio preview")}</span>
        <span>
          {t("{elapsed} / {duration}", { elapsed: clock(time), duration: clock(duration) })}
        </span>
      </div>
      {full && (
        <label className="flex items-center gap-3 text-xs text-fg-muted">
          <Volume2 size={15} />
          {t("Volume")}
          <input
            className="flex-1 accent-primary"
            type="range"
            min="0"
            max="1"
            step=".01"
            defaultValue="1"
            aria-label={t("Audio preview volume")}
            onChange={(e) => {
              if (audio.current) audio.current.volume = Number(e.target.value);
            }}
          />
        </label>
      )}
      {error && (
        <p role="alert" className="text-xs text-danger">
          {t("Couldn’t play this audio.")}
        </p>
      )}
    </div>
  );
}
function Preview({ asset: a, full = false }: { asset: GridAsset; full?: boolean }) {
  const [sample, setSample] = useState(t("Aa"));
  const [hovered, setHovered] = useState(false);
  const [posterBroken, setPosterBroken] = useState(false);
  const ready = !a.status || a.status === "ready";
  const localPlayable = !a.link || Boolean(a.cached);
  const mediaHref = a.url ?? studio.media(a.digest);
  const thumbHref = a.thumbnail ?? studio.thumb(a.digest);
  const { src: thumbnail, failed: thumbFailed } = usePosterURL(
    thumbHref,
    ready && (a.kind === "image" || a.kind === "video"),
  );
  useEffect(() => {
    setPosterBroken(false);
  }, [thumbnail]);
  const wantVideoSource =
    ready &&
    (a.kind === "image"
      ? full
      : a.kind !== "video"
        ? true
        : localPlayable && (full || hovered || thumbFailed || posterBroken));
  const preview = useMediaURL(mediaHref, wantVideoSource);
  useEffect(() => {
    if (a.kind !== "font" || !preview) return;
    const face = new FontFace(`library-${a.digest}`, `url(${preview})`);
    let live = true;
    void face
      .load()
      .then((f) => {
        if (live) document.fonts.add(f);
      })
      .catch(() => {});
    return () => {
      live = false;
      document.fonts.delete(face);
    };
  }, [a.digest, a.kind, preview]);
  if (!ready) {
    const Icon = { audio: AudioLines, video: Film, image: Image, font: Type }[a.kind];
    return (
      <div className="flex h-full min-h-32 flex-col items-center justify-center gap-2 bg-bg-subtle p-4 text-center text-fg-muted">
        <Icon size={24} aria-hidden />
        <span className="text-xs">
          {a.status === "error"
            ? t("Import failed")
            : t("Preparing media… {progress}%", { progress: Math.round(a.progress ?? 0) })}
        </span>
      </div>
    );
  }
  if (a.kind === "video") {
    const hasPoster = Boolean(thumbnail && !posterBroken);
    if (full) {
      if (!localPlayable) {
        return (
          <div className="relative aspect-video overflow-hidden rounded-lg bg-bg-subtle">
            {hasPoster ? (
              <img
                src={thumbnail}
                alt=""
                onError={() => setPosterBroken(true)}
                className="h-full w-full object-cover"
              />
            ) : (
              <div className="flex h-full flex-col items-center justify-center gap-2 p-4 text-center text-fg-muted">
                <Film size={24} aria-hidden />
                <span className="text-xs">{t("Resolves when aired")}</span>
              </div>
            )}
          </div>
        );
      }
      return (
        <video
          className="aspect-video w-full rounded-lg bg-bg-subtle object-cover"
          src={preview || undefined}
          poster={hasPoster ? thumbnail : undefined}
          controls
          playsInline
          preload="metadata"
        />
      );
    }
    return (
      <div
        className="relative h-full w-full overflow-hidden bg-bg-subtle"
        tabIndex={0}
        onMouseEnter={() => {
          if (localPlayable) setHovered(true);
        }}
        onMouseLeave={() => setHovered(false)}
        onFocus={() => {
          if (localPlayable) setHovered(true);
        }}
        onBlur={() => setHovered(false)}
      >
        <div
          aria-hidden
          className="absolute inset-0 flex flex-col items-center justify-center gap-1.5 p-3 text-center text-fg-subtle"
        >
          <Film size={20} />
          <span className="line-clamp-2 max-w-[14rem] text-[11px] font-medium text-fg-muted">
            {a.name}
          </span>
        </div>
        {hasPoster ? (
          <img
            src={thumbnail}
            alt=""
            loading="lazy"
            onError={() => setPosterBroken(true)}
            className="relative h-full w-full object-cover"
          />
        ) : null}
        {localPlayable && preview && (hovered || !hasPoster) ? (
          <video
            className="absolute inset-0 h-full w-full object-cover"
            src={hovered ? preview : `${preview}#t=0.5`}
            poster={hasPoster ? thumbnail : undefined}
            muted
            loop
            autoPlay={hovered}
            playsInline
            preload="metadata"
          />
        ) : null}
      </div>
    );
  }
  if (a.kind === "audio") return <AudioPreview asset={a} source={preview} full={full} />;
  if (a.kind === "font")
    return (
      <div className="flex h-full flex-col items-center justify-center gap-4 p-4 text-center">
        {full && (
          <Input
            aria-label={t("Sample text")}
            value={sample}
            onChange={(e) => setSample(e.target.value)}
          />
        )}
        <p className="break-words text-6xl" style={{ fontFamily: `library-${a.digest}` }}>
          {sample}
        </p>
        {full && (
          <p className="mt-6 break-words text-2xl" style={{ fontFamily: `library-${a.digest}` }}>
            {t("ABCDEFGHIJKLMNOPQRSTUVWXYZ abcdefghijklmnopqrstuvwxyz 0123456789 !?@#$%&*()")}
          </p>
        )}
      </div>
    );
  return full ? (
    <a href={preview} target="_blank" rel="noreferrer" aria-label={t("Open full size image")}>
      <img src={preview || undefined} alt={a.name} className="max-h-[60vh] w-full object-contain" />
    </a>
  ) : (
    <img
      src={thumbnail || undefined}
      alt=""
      loading="lazy"
      className="h-full w-full object-cover"
    />
  );
}
const kinds: AssetKind[] = ["video", "audio", "image", "font"];
export function LibraryGrid({
  allowed = kinds,
  onPick,
  filters = true,
}: {
  allowed?: AssetKind[];
  onPick?: (a: LibraryAsset) => void;
  /** The Live TV page shows kind and search in its lineup panel instead. */
  filters?: boolean;
}) {
  const [params, setParams] = useSearchParams();
  const uploadInput = useRef<HTMLInputElement>(null);
  const uploadOpened = useRef(false);
  useEffect(() => {
    if (params.get("upload") !== "1") {
      uploadOpened.current = false;
      return;
    }
    if (uploadOpened.current) return;
    uploadOpened.current = true;
    uploadInput.current?.click();
    setParams(
      (previous) => {
        const next = new URLSearchParams(previous);
        next.delete("upload");
        return next;
      },
      { replace: true },
    );
  }, [params, setParams]);
  const [loading, setLoading] = useState(true);
  const [assets, setAssets] = useState<GridAsset[]>([]),
    [error, setError] = useState("");
  const [selected, setSelected] = useState<string[]>([]),
    [pending, setPending] = useState<File[]>([]),
    [license, setLicense] = useState("OFL");
  const [uploads, setUploads] = useState<
    { id: string; name: string; progress: number; status: string }[]
  >([]);
  const kind = params.get("kind") ?? "",
    query = params.get("q") ?? "",
    sort = params.get("sort") ?? "newest";
  const change = (key: string, value: string) =>
    setParams((p) => {
      const next = new URLSearchParams(p);
      if (value) next.set(key, value);
      else next.delete(key);
      return next;
    });
  const reload = async () => {
    try {
      const library: GridAsset[] = await studio.library();
      if (!onPick) {
        const reels = await onair.reels();
        for (const reel of reels)
          for (const clip of reel.clips) {
            library.push({
              ...clip,
              digest: `reel-${reel.id}-${clip.id}`,
              reelId: reel.id,
              clipId: clip.id,
              kind: "video",
              mime: "video/mp4",
              size: clip.size ?? 0,
              createdAt: 0,
              usedBy: [{ kind: "reel", id: reel.id, name: reel.name }],
              url: clip.preview,
              thumbnail:
                clip.thumbnail ?? (clip.sourceDigest ? studio.thumb(clip.sourceDigest) : undefined),
              readOnly: true,
            });
          }
      }
      setAssets(library);
      setError("");
      if (typeof window !== "undefined") {
        window.dispatchEvent(new Event(LIBRARY_CHANGED_EVENT));
      }
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setLoading(false);
    }
  };
  useEffect(() => {
    void reload();
  }, []);
  useEffect(() => {
    if (!assets.some((a) => a.status && !["ready", "error"].includes(a.status))) return;
    const id = setInterval(() => void reload(), 1500);
    return () => clearInterval(id);
  }, [assets]);
  const upload = async (file: File, lic = "") => {
    const assetKind: AssetKind = /\.(ttf|otf)$/i.test(file.name)
      ? "font"
      : file.type.startsWith("video/")
        ? "video"
        : file.type.startsWith("audio/")
          ? "audio"
          : "image";
    if (assetKind === "font" && !lic) {
      setPending((p) => [...p, file]);
      return;
    }
    if (!allowed.includes(assetKind)) {
      setError(t("This file type is not available here."));
      return;
    }
    // Rows are keyed per attempt: a retried file must not share its progress.
    const uploadId = `${file.name}:${Date.now()}:${Math.random()}`;
    setUploads((p) => [
      ...p,
      { id: uploadId, name: file.name, progress: 0, status: t("Uploading…") },
    ]);
    try {
      if (import.meta.env.MODE === "mock") {
        const data = new FormData();
        data.append("file", file);
        data.append("kind", assetKind);
        data.append("license", lic);
        await apiFetch("/onair/assets", { method: "POST", body: data });
      } else
        await new Promise<void>((resolve, reject) => {
          const xhr = new XMLHttpRequest();
          xhr.open("POST", "/ether/api/onair/assets");
          // XHR bypasses apiFetch, so it sets the CSRF header itself.
          xhr.setRequestHeader("X-Ether-Request", "1");
          sessionXhrSetup(xhr, "/ether/api/onair/assets");
          xhr.upload.onprogress = (e) => {
            if (e.lengthComputable)
              setUploads((p) =>
                p.map((x) =>
                  x.id === uploadId
                    ? { ...x, progress: Math.round((e.loaded / e.total) * 100) }
                    : x,
                ),
              );
          };
          xhr.upload.onload = () =>
            setUploads((p) =>
              p.map((x) => (x.id === uploadId ? { ...x, status: t("Processing media…") } : x)),
            );
          xhr.onload = () => {
            if (xhr.status >= 200 && xhr.status < 300) return resolve();
            let reason = "";
            try {
              reason =
                (JSON.parse(xhr.responseText) as { error?: { message?: string } }).error?.message ??
                "";
            } catch {
              /* not JSON: keep the generic message */
            }
            reject(
              new Error(
                reason
                  ? `${t("Upload failed")}: ${friendlyMediaError(reason)}`
                  : t("Upload failed"),
              ),
            );
          };
          xhr.onerror = () => reject(new Error(t("Upload failed")));
          const data = new FormData();
          data.append("file", file);
          data.append("kind", assetKind);
          data.append("license", lic);
          xhr.send(data);
        });
      setUploads((p) => p.filter((x) => x.id !== uploadId));
      await reload();
    } catch (e) {
      setError(errorMessage(e));
      setUploads((p) =>
        p.map((x) => (x.id === uploadId ? { ...x, status: t("Upload failed") } : x)),
      );
    }
  };
  const remove = async (list: LibraryAsset[]) => {
    if (!list.length) return;
    if (
      !(await confirmDialog({
        title: tn(list.length, "Delete {count} asset?", "Delete {count} assets?"),
        message: t("These files will be permanently removed from the Library."),
        confirmLabel: t("Delete"),
        tone: "danger",
      }))
    )
      return false;
    try {
      for (const a of list) await onair.deleteAsset(a.digest);
      setSelected([]);
      await reload();
      return true;
    } catch (e) {
      setError(errorMessage(e));
      return false;
    }
  };
  const cancelOrDismissImport = async (digest: string) => {
    try {
      await onair.deleteAsset(digest);
      await reload();
    } catch (e) {
      setError(errorMessage(e));
    }
  };
  const visible = assets
    .filter(
      (a) =>
        allowed.includes(a.kind) &&
        (!kind || a.kind === kind) &&
        a.name.toLocaleLowerCase().includes(query.toLocaleLowerCase()),
    )
    .sort((a, b) =>
      sort === "name"
        ? a.name.localeCompare(b.name)
        : sort === "size"
          ? b.size - a.size
          : sort === "used"
            ? (b.usedBy?.length ?? 0) - (a.usedBy?.length ?? 0)
            : (b.createdAt ?? 0) - (a.createdAt ?? 0),
    );
  const active = assets.find((a) => a.digest === params.get("asset"));
  const sortPicker = (
    <ToolbarPicker
      ariaLabel={t("Sort assets")}
      value={sort}
      onChange={(value) => change("sort", value === "newest" ? "" : value)}
      options={[
        { value: "newest", label: t("Newest") },
        { value: "name", label: t("Name") },
        { value: "size", label: t("Size") },
        { value: "used", label: t("Most used") },
      ]}
      menuClassName="w-48"
    />
  );
  return (
    <section
      className="space-y-5"
      onDragOver={(e) => e.preventDefault()}
      onDrop={(e) => {
        e.preventDefault();
        for (const file of e.dataTransfer.files) void upload(file);
      }}
    >
      <div className="flex flex-wrap items-center gap-2.5">
        <div className="min-w-0 flex-1">
          <h2 className="text-xl font-semibold">{t("Your station’s creative library")}</h2>
          <p className="text-sm text-fg-muted">
            {t("Bumpers, stings, logos and type. Make every moment yours.")}
          </p>
        </div>
        {!filters ? sortPicker : null}
        <label className="inline-flex h-8 cursor-pointer items-center rounded-md border border-border bg-surface px-3 text-xs font-medium text-fg transition-colors hover:bg-surface-hover focus-within:ring-2 focus-within:ring-primary">
          {t("Upload files")}
          <input
            className="sr-only"
            aria-label={t("Upload files")}
            ref={uploadInput}
            type="file"
            multiple
            accept="image/*,audio/*,video/*,.ttf,.otf"
            onChange={(e) => {
              for (const f of e.target.files ?? []) void upload(f);
              e.target.value = "";
            }}
          />
        </label>
        <LinkImport onImported={reload} />
      </div>
      {filters ? (
        <div className="flex flex-wrap items-center gap-2">
          {[
            ["", t("All")],
            ["video", t("Video")],
            ["audio", t("Audio")],
            ["image", t("Images")],
            ["font", t("Fonts")],
          ]
            .filter(([k]) => !k || allowed.includes(k as AssetKind))
            .map(([k, label]) => (
              <Button
                key={k}
                size="sm"
                variant={kind === k ? "default" : "outline"}
                aria-pressed={kind === k}
                onClick={() => change("kind", k!)}
              >
                {label}
              </Button>
            ))}
          <Input
            aria-label={t("Search Library")}
            placeholder={t("Search Library")}
            value={query}
            onChange={(e) => change("q", e.target.value)}
            className="min-w-40 flex-1"
          />
          {sortPicker}
        </div>
      ) : null}
      {error && (
        <p role="alert" className="text-danger">
          {error}
        </p>
      )}
      {uploads.map((u) => (
        <div key={u.id} role="status">
          <p>
            {t("{name}: {status} ({progress}%)", {
              name: u.name,
              status: u.status,
              progress: u.progress,
            })}
          </p>
          <progress className="w-full" max={100} value={u.progress} />
        </div>
      ))}
      {pending.length > 0 && (
        <div className="space-y-3 rounded-xl border border-border p-4">
          <p>{t("Choose a licence before uploading fonts.")}</p>
          <Segmented
            ariaLabel={t("Font licence")}
            value={license}
            onChange={setLicense}
            options={[
              { value: "OFL", label: t("OFL") },
              { value: "Apache", label: t("Apache") },
              { value: "Owned by me", label: t("Owned by me") },
              { value: "Other", label: t("Other") },
            ]}
          />
          {pending.map((f) => (
            <p key={f.name}>{f.name}</p>
          ))}
          <div className="flex items-center gap-2">
            <Button
              disabled={!license}
              onClick={() => {
                for (const f of pending) void upload(f, license);
                setPending([]);
              }}
            >
              {t("Upload fonts")}
            </Button>
            <Button variant="ghost" onClick={() => setPending([])}>
              {t("Cancel")}
            </Button>
          </div>
        </div>
      )}
      {!onPick && selected.length > 0 && (
        <div className="flex items-center gap-3">
          <span>{tn(selected.length, "{count} selected", "{count} selected")}</span>
          <Button
            variant="outline"
            disabled={
              !assets.some((a) => selected.includes(a.digest) && !a.usedBy.length && !a.readOnly)
            }
            onClick={() =>
              void remove(assets.filter((a) => selected.includes(a.digest) && !a.usedBy.length))
            }
          >
            {t("Delete unused selected")}
          </Button>
          <Button variant="ghost" onClick={() => setSelected([])}>
            {t("Clear selection")}
          </Button>
        </div>
      )}
      {loading && <p role="status">{t("Loading Library…")}</p>}
      {!loading && !visible.length && (
        <div className="rounded-2xl border border-dashed border-border p-12 text-center">
          <h3 className="text-lg font-semibold">
            {assets.length ? t("No matching assets") : t("Give your station its signature")}
          </h3>
          <p className="mx-auto mt-3 max-w-lg text-fg-muted">
            {t(
              "Drop video bumpers, audio stings, image logos or your favourite licensed fonts here. Everything you add is ready to use in your styles.",
            )}
          </p>
        </div>
      )}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
        {visible.map((a) => {
          const Icon = { audio: AudioLines, video: Film, image: Image, font: Type }[a.kind];
          const isReady = !a.status || a.status === "ready";
          return (
            <article
              key={a.digest}
              onClick={(e) => {
                if (!(e.target as HTMLElement).closest("button,input,a,audio,video")) {
                  if (onPick) {
                    if (isReady) onPick(a);
                  } else change("asset", a.digest);
                }
              }}
              className="cursor-pointer overflow-hidden rounded-xl border border-border bg-surface shadow-sm"
            >
              <div className="aspect-video overflow-hidden bg-bg-subtle">
                <Preview asset={a} />
              </div>
              <div className="space-y-2 p-3">
                <div className="flex items-center gap-2">
                  <Icon size={16} aria-hidden />
                  <button
                    className="min-w-0 flex-1 truncate text-left font-medium hover:text-primary focus-visible:ring-2"
                    onClick={() => (onPick ? isReady && onPick(a) : change("asset", a.digest))}
                  >
                    {a.name}
                  </button>
                  {!onPick && !a.readOnly && (
                    <Checkbox
                      aria-label={t("Select {name}", { name: a.name })}
                      checked={selected.includes(a.digest)}
                      onChange={(e) =>
                        setSelected((p) =>
                          e.target.checked ? [...p, a.digest] : p.filter((d) => d !== a.digest),
                        )
                      }
                    />
                  )}
                </div>
                <div className="flex justify-between text-xs text-fg-muted">
                  <span>
                    {a.readOnly
                      ? t("{seconds} s", { seconds: ((a.durationMs ?? 0) / 1000).toFixed(1) })
                      : a.durationMs
                        ? t("{seconds} s · {size}", {
                            seconds: (a.durationMs / 1000).toFixed(1),
                            size: formatBytes(a.size),
                          })
                        : formatBytes(a.size)}
                  </span>
                  <span className="rounded-full bg-bg-subtle px-2">
                    {tn(a.usedBy?.length ?? 0, "Used by {count}", "Used by {count}")}
                  </span>
                </div>
                {a.status && a.status !== "ready" && (
                  <div className="flex items-center justify-between gap-2">
                    <p
                      role="status"
                      className={cn(
                        "text-xs",
                        a.status === "error" ? "text-danger" : "text-fg-muted",
                      )}
                    >
                      {a.status === "error"
                        ? friendlyMediaError(a.error)
                        : t("Preparing media… {progress}%", {
                            progress: Math.round(a.progress ?? 0),
                          })}
                    </p>
                    {!a.readOnly && (
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-danger"
                        onClick={() => void cancelOrDismissImport(a.digest)}
                      >
                        {a.status === "error" ? t("Dismiss") : t("Cancel")}
                      </Button>
                    )}
                  </div>
                )}
              </div>
            </article>
          );
        })}
      </div>
      {active && !onPick && active.readOnly && (
        <ReelClipLibraryDrawer
          key={active.digest}
          asset={active}
          onClose={() => change("asset", "")}
          onReload={reload}
          onSelectAsset={(digest) => change("asset", digest)}
        />
      )}
      {active && !onPick && !active.readOnly && (
        <AssetDrawer
          key={active.digest}
          asset={active}
          onClose={() => change("asset", "")}
          onChanged={async (a) => {
            await reload();
            if (a) change("asset", a.digest);
          }}
          onSelectAsset={(digest) => change("asset", digest)}
          onDelete={async () => {
            const deleted = await remove([active]);
            if (deleted) change("asset", "");
          }}
        />
      )}
    </section>
  );
}

function ReelClipLibraryDrawer({
  asset: a,
  onClose,
  onReload,
  onSelectAsset,
}: {
  asset: GridAsset;
  onClose: () => void;
  onReload: () => Promise<void>;
  onSelectAsset: (digest: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const isError = a.status === "error";

  const handleRetry = async () => {
    if (!a.reelId || !a.clipId) return;
    setBusy(true);
    setError("");
    try {
      if (a.link) {
        await onair.resolveClip(a.reelId, a.clipId);
        await onReload();
      } else if (a.source) {
        const created = await apiFetch<{ id: string }[]>(
          `/onair/reels/${encodeURIComponent(a.reelId)}/clips/import`,
          {
            method: "POST",
            body: JSON.stringify({ urls: [a.source], kind: "video" }),
          },
        );
        await onair.deleteClip(a.reelId, a.clipId);
        await onReload();
        onSelectAsset(created[0]?.id ? `reel-${a.reelId}-${created[0].id}` : "");
      }
    } catch (e) {
      setError(friendlyMediaError(errorMessage(e)));
    } finally {
      setBusy(false);
    }
  };

  const handleDelete = async () => {
    if (!a.reelId || !a.clipId) return;
    if (
      !(await confirmDialog({
        title: t("Delete clip?"),
        message: t("This clip will be removed from its reel."),
        confirmLabel: t("Delete"),
        tone: "danger",
      }))
    )
      return;
    setBusy(true);
    setError("");
    try {
      await onair.deleteClip(a.reelId, a.clipId);
      onClose();
      await onReload();
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Drawer
      open
      title={a.name}
      onClose={onClose}
      size="lg"
      footer={
        isError && a.reelId && a.clipId ? (
          <div className="flex w-full flex-wrap items-center justify-end gap-2">
            <Button
              variant="outline"
              className="text-danger"
              disabled={busy}
              onClick={() => void handleDelete()}
            >
              <Trash2 size={14} />
              {t("Delete")}
            </Button>
            {a.link || a.source ? (
              <Button disabled={busy} onClick={() => void handleRetry()}>
                <RotateCcw size={14} />
                {busy ? t("Retrying…") : t("Retry")}
              </Button>
            ) : null}
          </div>
        ) : undefined
      }
    >
      <div className="space-y-4">
        <Preview asset={a} full />
        {isError ? (
          <div
            role="alert"
            className="space-y-2 rounded-xl border border-danger/30 bg-danger-subtle p-4"
          >
            <p className="text-sm font-medium text-danger">{t("Couldn’t import this media")}</p>
            <p className="text-sm text-fg">{friendlyMediaError(a.error)}</p>
            {a.source ? (
              <a
                className="inline-block break-all text-xs text-primary underline"
                href={a.source}
                target="_blank"
                rel="noreferrer"
              >
                {a.source}
              </a>
            ) : null}
          </div>
        ) : a.link && !a.cached && a.reelId && a.clipId ? (
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void handleRetry()}>
            <Play size={14} />
            {busy ? t("Preparing preview…") : t("Prepare video preview")}
          </Button>
        ) : null}
        {error ? (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        ) : null}
        {a.usedBy.map((u) => (
          <Link
            key={u.id}
            className="block text-sm text-primary"
            to={`/livetv/looks?reel=${encodeURIComponent(u.id)}`}
          >
            {t("Used in reel {name}", { name: u.name })}
          </Link>
        ))}
      </div>
    </Drawer>
  );
}

function AssetDrawer({
  asset: a,
  onClose,
  onChanged,
  onSelectAsset,
  onDelete,
}: {
  asset: LibraryAsset;
  onClose: () => void;
  onChanged: (a?: LibraryAsset) => Promise<void>;
  onSelectAsset: (digest: string) => void;
  onDelete: () => Promise<void>;
}) {
  const [params, setParams] = useSearchParams();
  const tab = params.get("assetTab") ?? "preview";
  const [name, setName] = useState(a.name),
    [license, setLicense] = useState(a.license ?? ""),
    [attribution, setAttribution] = useState(a.attribution ?? ""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false),
    [start, setStart] = useState(0),
    [end, setEnd] = useState((a.durationMs ?? 0) / 1000);
  const run = async (fn: () => Promise<LibraryAsset | void>) => {
    setBusy(true);
    setError("");
    try {
      const v = await fn();
      await onChanged(v || undefined);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  if (a.status === "error") {
    const retryImport = async () => {
      if (!a.source) return;
      setBusy(true);
      setError("");
      try {
        const created = await apiFetch<{ id?: string; digest?: string }[]>("/onair/assets/import", {
          method: "POST",
          body: JSON.stringify({ urls: [a.source], kind: a.kind }),
        });
        await onair.deleteAsset(a.digest);
        await onChanged();
        const nextId = created[0]?.digest ?? created[0]?.id ?? "";
        onSelectAsset(nextId);
      } catch (e) {
        setError(friendlyMediaError(errorMessage(e)));
      } finally {
        setBusy(false);
      }
    };
    const deleteFailed = async () => {
      setBusy(true);
      setError("");
      try {
        await onair.deleteAsset(a.digest);
        await onChanged();
        onClose();
      } catch (e) {
        setError(errorMessage(e));
      } finally {
        setBusy(false);
      }
    };
    return (
      <Drawer
        open
        onClose={onClose}
        title={a.name}
        documentTitle={a.name}
        size="lg"
        footer={
          <div className="flex w-full flex-wrap items-center justify-end gap-2">
            <Button
              variant="outline"
              className="text-danger"
              disabled={busy}
              onClick={() => void deleteFailed()}
            >
              <Trash2 size={14} />
              {t("Delete")}
            </Button>
            {a.source ? (
              <Button disabled={busy} onClick={() => void retryImport()}>
                <RotateCcw size={14} />
                {busy ? t("Retrying…") : t("Retry")}
              </Button>
            ) : null}
          </div>
        }
      >
        <div className="space-y-4">
          <Preview asset={a} full />
          <div
            role="alert"
            className="space-y-2 rounded-xl border border-danger/30 bg-danger-subtle p-4"
          >
            <p className="text-sm font-medium text-danger">{t("Couldn’t import this media")}</p>
            <p className="text-sm text-fg">{friendlyMediaError(a.error)}</p>
            {a.source ? (
              <a
                className="inline-block break-all text-xs text-primary underline"
                href={a.source}
                target="_blank"
                rel="noreferrer"
              >
                {a.source}
              </a>
            ) : null}
          </div>
          {error ? (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          ) : null}
        </div>
      </Drawer>
    );
  }
  return (
    <Drawer open onClose={onClose} title={a.name} documentTitle={a.name} size="lg">
      <div className="space-y-6">
        <SubtabNav
          ariaLabel={t("Asset sections")}
          active={tab}
          onSelect={(v) =>
            setParams((p) => {
              const next = new URLSearchParams(p);
              next.set("assetTab", v);
              return next;
            })
          }
          tabs={[
            { key: "preview", label: t("Preview") },
            { key: "details", label: t("Details") },
            { key: "usage", label: t("Used by") },
          ]}
        />
        {tab === "preview" && <Preview asset={a} full />}
        {error && (
          <p role="alert" className="text-danger">
            {error}
          </p>
        )}
        {tab === "details" && (
          <>
            <label className="block space-y-2">
              {t("Name")}
              <Input value={name} onChange={(e) => setName(e.target.value)} />
            </label>
            <label className="block space-y-2">
              {t("Licence")}
              <Input value={license} onChange={(e) => setLicense(e.target.value)} />
            </label>
            <label className="block space-y-2">
              {t("Attribution")}
              <Input value={attribution} onChange={(e) => setAttribution(e.target.value)} />
            </label>
            {a.source && (
              <a className="text-primary" href={a.source} target="_blank" rel="noreferrer">
                {t("Original source")}
              </a>
            )}
            <Button
              disabled={busy || !name.trim()}
              onClick={() =>
                void run(() =>
                  apiFetch<LibraryAsset>(`/onair/assets/${a.digest}`, {
                    method: "PATCH",
                    body: JSON.stringify({ name, license, attribution }),
                  }),
                )
              }
            >
              {t("Save details")}
            </Button>
            <label className="block cursor-pointer text-primary">
              {t("Replace file")}
              <input
                type="file"
                className="mt-2 block w-full text-sm"
                disabled={busy}
                accept={a.kind === "font" ? ".ttf,.otf" : `${a.kind}/*`}
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  if (f) void run(() => studio.replaceAsset(a.digest, f));
                }}
              />
            </label>
          </>
        )}
        {tab === "preview" &&
          (!a.status || a.status === "ready") &&
          (a.kind === "audio" || a.kind === "video") && (
            <div
              role="group"
              aria-label={t("Save a clip")}
              className="space-y-3 rounded-lg border border-border p-4"
            >
              <h3 className="text-sm font-medium text-fg">{t("Save a clip")}</h3>
              <label className="block">
                {t("Start (seconds)")}
                <Input
                  type="number"
                  min={0}
                  max={end}
                  step={0.1}
                  value={start}
                  onChange={(e) => setStart(Number(e.target.value))}
                />
              </label>
              <label className="block">
                {t("End (seconds)")}
                <Input
                  type="number"
                  min={start}
                  max={(a.durationMs ?? 0) / 1000}
                  step={0.1}
                  value={end}
                  onChange={(e) => setEnd(Number(e.target.value))}
                />
              </label>
              <Button
                disabled={busy || start < 0 || end <= start || end > (a.durationMs ?? 0) / 1000}
                onClick={() =>
                  void run(() =>
                    apiFetch<LibraryAsset>(`/onair/assets/${a.digest}/trim`, {
                      method: "POST",
                      body: JSON.stringify({ start, end, name: t("{name} clip", { name }) }),
                    }),
                  )
                }
              >
                {t("Save as new clip")}
              </Button>
            </div>
          )}
        {tab === "usage" && (
          <>
            <section className="space-y-2">
              <h3 className="font-medium">{t("Used by")}</h3>
              {a.usedBy.map((u, i) => (
                <Link
                  key={i}
                  className="block text-primary"
                  to={
                    u.kind === "channel"
                      ? `/livetv?channel=${encodeURIComponent(u.id)}`
                      : `/livetv/looks?${u.kind === "reel" ? "reel" : "look"}=${encodeURIComponent(u.id)}${u.scene ? `&scene=${encodeURIComponent(u.scene)}` : ""}`
                  }
                >
                  {u.name}
                </Link>
              ))}
              <p className="text-sm text-fg-muted">
                {a.usedBy.length
                  ? tn(
                      a.usedBy.length,
                      "Used by {count} record. Remove it from there first.",
                      "Used by {count} records. Remove it from them first.",
                    )
                  : t("Not used yet")}
              </p>
            </section>
            <Button
              variant="outline"
              disabled={busy || a.usedBy.length > 0}
              onClick={() => void run(onDelete)}
            >
              {t("Delete asset")}
            </Button>
          </>
        )}
      </div>
    </Drawer>
  );
}
export default function Library() {
  return <LibraryGrid filters={false} />;
}

/** Live TV lineup panel for the Library tab: kinds with counts, URL-backed like the grid. */
export function LibraryAside() {
  const [params, setParams] = useSearchParams();
  const [counts, setCounts] = useState<Record<string, number> | null>(null);
  useEffect(() => {
    let active = true;
    const loadCounts = () => {
      void Promise.all([studio.library(), onair.reels()])
        .then(([library, reels]) => {
          if (!active) return;
          const next: Record<string, number> = { "": 0, video: 0, audio: 0, image: 0, font: 0 };
          for (const a of library) {
            if (!kinds.includes(a.kind)) continue;
            next[""]! += 1;
            next[a.kind] = (next[a.kind] ?? 0) + 1;
          }
          for (const reel of reels) {
            next[""]! += reel.clips.length;
            next.video! += reel.clips.length;
          }
          setCounts(next);
        })
        .catch(() => {
          if (active) setCounts(null);
        });
    };
    loadCounts();
    window.addEventListener(LIBRARY_CHANGED_EVENT, loadCounts);
    return () => {
      active = false;
      window.removeEventListener(LIBRARY_CHANGED_EVENT, loadCounts);
    };
  }, []);
  const kind = params.get("kind") ?? "";
  const rows: [string, string, typeof Film][] = [
    ["", t("Everything"), LayoutGrid],
    ["video", t("Video"), Film],
    ["audio", t("Audio"), AudioLines],
    ["image", t("Images"), Image],
    ["font", t("Fonts"), Type],
  ];
  return (
    <nav aria-label={t("Library kinds")} className="space-y-1 p-3">
      {rows.map(([key, label, Icon]) => (
        <button
          key={key || "all"}
          type="button"
          aria-current={kind === key ? "page" : undefined}
          onClick={() =>
            setParams((p) => {
              const next = new URLSearchParams(p);
              if (key) next.set("kind", key);
              else next.delete("kind");
              return next;
            })
          }
          className={cn(
            "flex w-full items-center gap-3 rounded-md px-3 py-2 text-left text-sm transition-colors",
            kind === key
              ? "bg-primary-subtle text-primary"
              : "text-fg-muted hover:bg-surface-hover hover:text-fg",
          )}
        >
          <Icon size={15} aria-hidden />
          <span className="flex-1">{label}</span>
          {counts ? <span className="text-xs tabular-nums">{counts[key] ?? 0}</span> : null}
        </button>
      ))}
    </nav>
  );
}
