import type { LoadPolicy, RetryConfig } from "hls.js";
import { cn } from "@braedonsaunders/appkit-ui";
import { Loader2, Maximize2, Radio, RotateCcw, Volume2, VolumeX, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { errorMessage, sessionXhrSetup } from "../../lib/api";
import { formatBitrate } from "../../lib/format";
import { librariesApi, type LibraryLiveItem, type LibraryLiveSession } from "./api";

type Phase = "tuning" | "playing" | "error";
type HlsLike = {
  loadSource: (u: string) => void;
  attachMedia: (v: HTMLMediaElement) => void;
  destroy: () => void;
  on: (e: string, fn: (...args: unknown[]) => void) => void;
  levels?: { height?: number; bitrate?: number }[];
  currentLevel?: number;
};

/**
 * Floating mini player for a plugin library live item. Resolves and tunes the
 * stream through the server's HLS passthrough/relay pipeline and tears down the
 * ephemeral session immediately on close.
 */
export function PluginLivePreview({
  libraryId,
  tabKey,
  item,
  onClose,
}: {
  libraryId: number;
  tabKey: string;
  item: LibraryLiveItem;
  onClose: () => void;
}) {
  const video = useRef<HTMLVideoElement>(null);
  const stopped = useRef<Promise<unknown>>(Promise.resolve());
  const [phase, setPhase] = useState<Phase>("tuning");
  const [error, setError] = useState("");
  const [session, setSession] = useState<LibraryLiveSession | null>(null);
  const [muted, setMuted] = useState(false);
  const [quality, setQuality] = useState("");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    let hls: HlsLike | null = null;
    const previousStop = stopped.current;
    let tune: Promise<LibraryLiveSession> | undefined;
    let live = true;
    setPhase("tuning");
    setError("");
    setQuality("");
    setSession(null);
    const el = video.current;
    const resetMedia = () => {
      if (!el) return;
      el.pause();
      el.removeAttribute("src");
      el.load();
    };
    resetMedia();
    const fail = (message: string) => {
      if (!live) return;
      setError(message);
      setPhase("error");
    };
    (async () => {
      try {
        await previousStop;
        if (!live) return;
        const hevc =
          typeof MediaSource !== "undefined" &&
          MediaSource.isTypeSupported('video/mp4; codecs="hvc1.1.6.L153.B0"');
        const hevc10 = hevc && MediaSource.isTypeSupported('video/mp4; codecs="hvc1.2.4.L153.B0"');
        tune = librariesApi.playLive(libraryId, item.id, tabKey, hevc, hevc10);
        const s = await tune;
        if (!live) return;
        setSession(s);
        const { default: Hls } = await import("hls.js");
        const el = video.current;
        if (!live || !el) return;
        if (!Hls.isSupported()) {
          fail(t("This browser can't play HLS through Media Source Extensions."));
          return;
        }
        const deadline = performance.now() + 30_000;
        let manifestLoaded = false;
        const retry: RetryConfig = {
          maxNumRetry: 8,
          retryDelayMs: 1000,
          maxRetryDelayMs: 4000,
          shouldRetry: (_config, count, timeout, response) =>
            count < (manifestLoaded ? 3 : 8) &&
            (manifestLoaded || performance.now() < deadline) &&
            (timeout || response?.code === 502 || response?.code === 503),
        };
        const loadPolicy: LoadPolicy = {
          default: {
            maxTimeToFirstByteMs: 10_000,
            maxLoadTimeMs: 10_000,
            timeoutRetry: retry,
            errorRetry: retry,
          },
        };
        const h = new Hls({
          manifestLoadPolicy: loadPolicy,
          playlistLoadPolicy: loadPolicy,
          liveSyncDurationCount: 3,
          initialLiveManifestSize: 2,
          maxBufferLength: 24,
          maxMaxBufferLength: 24,
          backBufferLength: 8,
          xhrSetup: sessionXhrSetup,
        }) as unknown as HlsLike;
        hls = h;
        h.on(Hls.Events.MANIFEST_PARSED, () => {
          manifestLoaded = true;
        });
        h.on(Hls.Events.ERROR, (...args: unknown[]) => {
          const data = args[1] as {
            fatal?: boolean;
            details?: string;
            response?: { code?: number };
          };
          if (data?.fatal)
            fail(
              (data.response?.code ?? 0) >= 400
                ? t("The stream returned HTTP {code}.", { code: data.response!.code! })
                : data.details
                  ? t("Playback stopped ({details}).", { details: data.details })
                  : t("Playback stopped (stream error)."),
            );
        });
        h.on(Hls.Events.LEVEL_SWITCHED, () => {
          const l = h.levels?.[h.currentLevel ?? 0];
          if (l)
            setQuality(
              [l.height ? `${l.height}p` : "", l.bitrate ? formatBitrate(l.bitrate) : ""]
                .filter(Boolean)
                .join(" · "),
            );
        });
        h.attachMedia(el);
        h.loadSource(s.playlist);
        el.play()?.catch(() => {
          el.muted = true;
          setMuted(true);
          void el.play()?.catch(() => undefined);
        });
      } catch (e) {
        fail(errorMessage(e));
      }
    })();
    return () => {
      live = false;
      hls?.destroy();
      resetMedia();
      stopped.current = (async () => {
        await previousStop;
        const s = await tune;
        if (s) await librariesApi.stopLive(libraryId, s.id);
      })().catch(() => undefined);
    };
  }, [libraryId, tabKey, item.id, attempt]);

  useEffect(() => {
    const stop = () => {
      if (session) void librariesApi.stopLive(libraryId, session.id).catch(() => undefined);
    };
    window.addEventListener("pagehide", stop);
    return () => window.removeEventListener("pagehide", stop);
  }, [libraryId, session]);

  const toggleMute = useCallback(() => {
    const el = video.current;
    if (!el) return;
    el.muted = !el.muted;
    setMuted(el.muted);
  }, []);

  const title = session?.title || item.title;

  return (
    <div
      role="dialog"
      aria-label={t("Watching {stream}", { stream: title })}
      className="fixed bottom-5 right-5 z-50 w-[min(26rem,calc(100vw-2.5rem))] overflow-hidden rounded-2xl border border-white/10 bg-black shadow-2xl ring-1 ring-black/40"
    >
      <div className="group relative aspect-video bg-black">
        <video
          ref={video}
          className="size-full bg-black object-contain"
          playsInline
          muted={muted}
          onPlaying={() => setPhase("playing")}
          aria-label={t("{stream} live", { stream: title })}
        />
        {phase !== "playing" ? (
          <div className="absolute inset-0 grid place-items-center bg-gradient-to-br from-zinc-900 via-black to-zinc-900">
            <div className="flex flex-col items-center gap-3 px-6 text-center">
              <span className="grid size-11 place-items-center rounded-full bg-white/10 text-white">
                <Radio size={20} aria-hidden />
              </span>
              {phase === "tuning" ? (
                <p className="flex items-center gap-2 text-sm text-white/80">
                  <Loader2 size={14} className="animate-spin" />{" "}
                  {t("Starting {stream}…", { stream: title })}
                </p>
              ) : (
                <>
                  <p className="text-sm text-white/90">
                    {error || t("Couldn't play this live stream.")}
                  </p>
                  <button
                    type="button"
                    onClick={() => setAttempt((a) => a + 1)}
                    className="inline-flex items-center gap-1.5 rounded-full bg-white/10 px-3 py-1 text-xs font-medium text-white hover:bg-white/20"
                  >
                    <RotateCcw size={12} /> {t("Try again")}
                  </button>
                </>
              )}
            </div>
          </div>
        ) : null}

        <div className="pointer-events-none absolute inset-x-0 top-0 flex items-center gap-2 bg-gradient-to-b from-black/80 to-transparent px-3 pb-6 pt-2.5">
          <span className="inline-flex items-center gap-1 rounded bg-danger px-1.5 py-0.5 text-[10px] font-bold uppercase tracking-wider text-white">
            <span className="size-1.5 animate-pulse rounded-full bg-white" /> {t("Live")}
          </span>
          <span className="min-w-0 truncate text-xs font-medium text-white">{title}</span>
          <div className="pointer-events-auto ml-auto flex items-center gap-1">
            <PreviewIconButton label={muted ? t("Unmute") : t("Mute")} onClick={toggleMute}>
              {muted ? <VolumeX size={14} /> : <Volume2 size={14} />}
            </PreviewIconButton>
            <PreviewIconButton
              label={t("Full screen")}
              onClick={() => void video.current?.requestFullscreen?.().catch(() => undefined)}
            >
              <Maximize2 size={14} />
            </PreviewIconButton>
            <PreviewIconButton label={t("Stop watching")} onClick={onClose}>
              <X size={15} />
            </PreviewIconButton>
          </div>
        </div>

        <div
          className={cn(
            "pointer-events-none absolute inset-x-0 bottom-0 flex items-end gap-2 bg-gradient-to-t from-black/85 to-transparent px-3 pb-2.5 pt-8 text-[11px] text-white/80 transition-opacity",
            phase === "playing" && "opacity-0 group-hover:opacity-100",
          )}
        >
          <span className="min-w-0 flex-1 truncate">
            {item.subtitle ? <span className="text-white/85">{item.subtitle}</span> : null}
          </span>
          {item.badge ? (
            <span className="shrink-0 rounded-full bg-white/15 px-2 py-0.5">{item.badge}</span>
          ) : null}
          {quality ? <span className="shrink-0 tabular-nums">{quality}</span> : null}
        </div>
      </div>
    </div>
  );
}

function PreviewIconButton({
  label,
  onClick,
  children,
}: {
  label: string;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className="grid size-7 place-items-center rounded-full text-white/85 transition-colors hover:bg-white/15 hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-white/60"
    >
      {children}
    </button>
  );
}
