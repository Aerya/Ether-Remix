import "./create-reveal.css";
import { AgentPanel } from "@braedonsaunders/appkit-ai/react";
import { Button } from "@braedonsaunders/appkit-ui";
import { Check, Palette, Pencil, RotateCcw, Undo2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import {
  agentPanelLabels,
  fetchAgentStatus,
  cancelAgentTurn,
} from "../../../../components/agent/agentApi";
import { t } from "../../../../i18n";
import { apiStream, errorMessage } from "../../../../lib/api";
import { readAgentStream } from "../../../plugins/LiveStage";
import type { Bindings } from "../model";
import { ROLES, studio, type Style } from "../studio-model";
import { StyleThumbnail } from "./RemixPanel";

const SAMPLE_BINDINGS: Bindings = {
  viewer: { name: "Alex", greeting: "Good evening" },
  now: {
    title: "Carnival of Souls",
    time: "20:00",
    meta: "1962 · 1h 18m",
    logline: "A church organist drifts toward a deserted pavilion.",
  },
  next: {
    title: "The Grand Budapest Hotel",
    time: "21:30",
    reason: "Picked for your evening",
    meta: "2014 · 1h 39m",
    logline: "A legendary concierge and his lobby boy are swept into an alpine caper.",
  },
  offair: { message: "Back on air soon" },
  list: [
    { time: "20:00", title: "Carnival of Souls", meta: "1962 · 1h 18m" },
    { time: "21:30", title: "The Grand Budapest Hotel", meta: "2014 · 1h 39m" },
    { time: "23:15", title: "Moonrise Kingdom", meta: "2012 · 1h 34m" },
    { time: "01:00", title: "Fantastic Mr. Fox", meta: "2009 · 1h 27m" },
  ],
};

export function CreateTab({
  style,
  onChange,
  selectedSceneId: sceneId,
  sceneOnly = false,
  bindings,
  active = true,
}: {
  style: Style;
  onChange(style: Style): void;
  selectedSceneId?: string;
  sceneOnly?: boolean;
  bindings?: Bindings;
  active?: boolean;
}) {
  const selectedSceneId = sceneOnly ? sceneId : undefined;
  const previewBindings = useMemo<Bindings>(
    () =>
      bindings &&
      (bindings.now?.title || bindings.next?.title || bindings.list?.some((item) => item.title))
        ? bindings
        : {
            ...SAMPLE_BINDINGS,
            ...(bindings?.channel?.name ? { channel: bindings.channel } : {}),
            ...(bindings?.viewer?.name ? { viewer: bindings.viewer } : {}),
            ...(bindings?.offair?.message ? { offair: bindings.offair } : {}),
          },
    [bindings],
  );
  const [proposal, setProposal] = useState<Style | null>(null);
  const [revealed, setRevealed] = useState<number | null>(null);
  const [reducedMotion, setReducedMotion] = useState(false);
  useEffect(() => {
    const media = matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => setReducedMotion(media.matches);
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  useEffect(() => {
    if (!proposal || revealed === null || revealed >= proposal.scenes.length) return;
    const timer = setTimeout(() => setRevealed((n) => (n ?? 0) + 1), reducedMotion ? 0 : 700);
    return () => clearTimeout(timer);
  }, [proposal, revealed, reducedMotion]);
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [before, setBefore] = useState<Style | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [lastPrompt, setLastPrompt] = useState("");
  const thread = useRef<string | undefined>(undefined);
  const turn = useRef<string | undefined>(undefined);
  const controller = useRef<AbortController | null>(null);
  const chat = useRef<HTMLDivElement>(null);
  const revision = useRef(0);
  useEffect(() => {
    let live = true;
    void fetchAgentStatus()
      .then((s) => {
        if (live) setEnabled(s.enabled);
      })
      .catch(() => {
        if (live) setEnabled(false);
      });
    return () => {
      live = false;
      revision.current++;
      controller.current?.abort();
      if (turn.current) void cancelAgentTurn(turn.current).catch(() => {});
    };
  }, []);
  const fill = (value: string) => {
    const box = chat.current?.querySelector("textarea");
    if (!box) return;
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(box, value);
    box.dispatchEvent(new Event("input", { bubbles: true }));
    box.focus();
  };
  const send = async (prompt: string, signal: AbortSignal): Promise<Response> => {
    setError("");
    setBusy(true);
    setRevealed(0);
    setProposal(null);
    setLastPrompt(prompt);
    const current = ++revision.current;
    const abort = new AbortController();
    controller.current = abort;
    const stop = () => {
      abort.abort();
      if (turn.current) void cancelAgentTurn(turn.current).catch(() => {});
    };
    signal.addEventListener("abort", stop, { once: true });
    try {
      const response = await apiStream("/agent/turn", {
        method: "POST",
        signal: abort.signal,
        body: JSON.stringify({
          threadId: thread.current,
          prompt,
          context: {
            route: "/livetv/look",
            draft: { look: proposal ?? style, sceneId: selectedSceneId },
          },
        }),
      });
      thread.current = response.headers.get("X-Ether-Thread-Id") ?? thread.current;
      if (!response.ok || !response.body) {
        setBusy(false);
        return response;
      }
      const [preview, panel] = response.body.tee();
      // Serial validation keeps drafts in stream order and prevents stale proposals winning.
      let pending = Promise.resolve();
      void readAgentStream(new Response(preview), (chunk) => {
        if (chunk.type === "start") turn.current = chunk.messageId;
        if (chunk.type === "error")
          setError(chunk.errorText ?? t("Ether could not finish that. Try again."));
        if (chunk.type === "tool-output-available" && chunk.output?.look) {
          const draft = chunk.output.look as Style;
          pending = pending
            .then(async () => {
              const scenes: Style["scenes"] = [];
              for (const scene of draft.scenes) scenes.push(await studio.validateScene(scene));
              if (current === revision.current && !abort.signal.aborted)
                setProposal(
                  selectedSceneId
                    ? {
                        ...(proposal ?? style),
                        scenes: (proposal ?? style).scenes.map((scene) =>
                          scene.id === selectedSceneId
                            ? (scenes.find((next) => next.id === scene.id) ?? scene)
                            : scene,
                        ),
                      }
                    : { ...draft, id: style.id, channelIds: style.channelIds, scenes },
                );
            })
            .catch((e) => {
              if (current === revision.current) setError(errorMessage(e));
            });
        }
      })
        .catch((e) => {
          if (!abort.signal.aborted) setError(errorMessage(e));
        })
        .finally(async () => {
          await pending;
          if (current === revision.current) setBusy(false);
          turn.current = undefined;
          signal.removeEventListener("abort", stop);
        });
      return new Response(panel, { headers: response.headers });
    } catch (e) {
      setBusy(false);
      signal.removeEventListener("abort", stop);
      throw e;
    }
  };
  const preview = proposal ?? style;
  const building =
    busy || (proposal !== null && revealed !== null && revealed < proposal.scenes.length);
  const roles = {
    ident: t("Ident"),
    upnext: t("Up next"),
    tonight: t("Tonight"),
    break: t("Break"),
    back: t("Back from break"),
    offair: t("Off air"),
    welcome: t("Welcome"),
    bug: t("Corner logo"),
    lowerthird: t("Lower third"),
  };
  const textures: Record<string, string> = {
    grain: t("Grain"),
    flicker: t("Flicker"),
    scanlines: t("Scanlines"),
    vignette: t("Vignette"),
    chroma: t("Chroma"),
  };
  const fontNames: Record<string, string> = {
    inter: "Inter",
    playfairdisplay: "Playfair Display",
    specialelite: "Special Elite",
    fraunces: "Fraunces",
    bungee: "Bungee",
    fredoka: "Fredoka",
    orbitron: "Orbitron",
    vt323: "VT323",
    spacegrotesk: "Space Grotesk",
    ibmplexmono: "IBM Plex Mono",
    archivoblack: "Archivo Black",
    notosans: "Noto Sans",
  };
  const ready = !busy || proposal !== null;
  const scenes = busy
    ? [
        ...preview.scenes,
        ...style.scenes.filter((scene) => !preview.scenes.some((next) => next.id === scene.id)),
      ]
    : preview.scenes;
  return (
    <section className={`studio-create${sceneOnly ? " studio-create-stacked" : ""}`}>
      <div className="studio-create-conversation">
        {enabled === false ? (
          <div className="m-4 rounded-xl border border-border p-6">
            <p>{t("Connect a language model to use Ask Ether.")}</p>
            <Link className="text-primary underline" to="/services">
              {t("Integrations")} → {t("LLM")}
            </Link>
          </div>
        ) : (
          <div ref={chat} className="studio-create-chat">
            <AgentPanel
              enabled={enabled === true}
              labels={{
                ...agentPanelLabels(),
                welcomeTitle: selectedSceneId
                  ? t("Shape this scene")
                  : t("Design your channel’s on-air look"),
                welcomeDescription: selectedSceneId
                  ? t(
                      "Describe the motion, typography, or layout you want for this moment. Your preview updates live.",
                    )
                  : t(
                      "Describe the era, palette, typography, and mood. Ether drafts your brand kit and scenes live on the right.",
                    ),
                placeholder: selectedSceneId ? t("Describe a change") : t("Describe your channel"),
              }}
              toolLabels={{ look_draft: t("Draft a style") }}
              send={send}
              emptyContent={
                <div className="m-auto flex max-w-md flex-col items-center px-5 py-8 text-center">
                  <span className="mb-3 grid size-10 place-items-center rounded-xl bg-primary-subtle text-primary">
                    <Palette size={20} aria-hidden />
                  </span>
                  <h3 className="text-base font-semibold text-fg">
                    {selectedSceneId
                      ? t("Shape this scene")
                      : t("Design your channel’s on-air look")}
                  </h3>
                  <p className="mt-1.5 text-sm leading-relaxed text-fg-muted">
                    {selectedSceneId
                      ? t(
                          "Describe the motion, typography, or layout you want for this moment. Your preview updates live.",
                        )
                      : t(
                          "Describe the era, palette, typography, and mood. Ether drafts your brand kit and scenes live on the right.",
                        )}
                  </p>
                  <div className="mt-5 grid w-full gap-2 text-left">
                    {(selectedSceneId
                      ? [
                          t("Slow the drift and make the title larger"),
                          t("Add a subtle timestamp in the accent colour"),
                          t("Give it a warm film-grain fade-in"),
                        ]
                      : [
                          t("Late-night 1980s cinema station with warm gold type and film grain"),
                          t("Crisp minimalist daytime channel with bright geometry"),
                          t("Cosy rainy-evening mystery channel with classic serif titles"),
                        ]
                    ).map((idea) => (
                      <button
                        key={idea}
                        type="button"
                        disabled={enabled !== true || busy}
                        onClick={() => fill(idea)}
                        className="rounded-xl border border-border bg-surface px-3.5 py-2.5 text-left text-xs leading-snug text-fg-muted transition-colors hover:border-primary/40 hover:bg-primary-subtle hover:text-fg disabled:pointer-events-none disabled:opacity-50"
                      >
                        {idea}
                      </button>
                    ))}
                  </div>
                </div>
              }
              composerContent={
                error || (!busy && (proposal || lastPrompt || before)) ? (
                  <div className="flex flex-wrap items-center gap-1 px-1 pb-1">
                    {error ? (
                      <p role="alert" className="mr-auto text-xs text-danger">
                        {error}
                      </p>
                    ) : null}
                    {!busy && proposal ? (
                      <>
                        <Button
                          size="sm"
                          aria-label={t("Keep this")}
                          title={t("Keep this")}
                          onClick={() => {
                            setBefore(structuredClone(style));
                            onChange(proposal);
                            setProposal(null);
                          }}
                        >
                          <Check size={14} />
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          aria-label={t("Refine")}
                          title={t("Refine")}
                          onClick={() => fill("")}
                        >
                          <Pencil size={14} />
                        </Button>
                      </>
                    ) : null}
                    {!busy && lastPrompt ? (
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={t("Try again")}
                        title={t("Try again")}
                        onClick={() => {
                          fill(lastPrompt);
                          requestAnimationFrame(() =>
                            chat.current
                              ?.querySelector("textarea")
                              ?.dispatchEvent(
                                new KeyboardEvent("keydown", { key: "Enter", bubbles: true }),
                              ),
                          );
                        }}
                      >
                        <RotateCcw size={14} />
                      </Button>
                    ) : null}
                    {!busy && (before || proposal) ? (
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={t("Revert")}
                        title={t("Revert")}
                        onClick={() => {
                          if (before) onChange(before);
                          setBefore(null);
                          setProposal(null);
                        }}
                      >
                        <Undo2 size={14} />
                      </Button>
                    ) : null}
                  </div>
                ) : undefined
              }
            />
          </div>
        )}
      </div>
      <div className="studio-create-build" aria-label={t("Live build")} aria-busy={building}>
        <div className={`studio-create-kit ${!ready ? "studio-create-pending" : ""}`}>
          <div className="flex items-center justify-between">
            <h3 className="text-sm font-semibold">{t("Brand kit")}</h3>
            <span className="text-xs text-fg-muted">
              {building ? t("Getting ready for TV") : t("Live build")}
            </span>
          </div>
          <div
            className="studio-create-brand studio-create-arrival"
            key={preview.brand.palette.accent}
            style={{
              background: preview.brand.palette.background,
              color: preview.brand.palette.text,
            }}
          >
            {preview.brand.logo?.length === 64 ? (
              <img
                className="h-12 w-12 object-contain"
                src={studio.thumb(preview.brand.logo)}
                alt={t("Logo")}
              />
            ) : (
              <span
                className="studio-create-monogram"
                style={{ color: preview.brand.palette.accent }}
              >
                {preview.brand.logo || preview.name.slice(0, 2)}
              </span>
            )}
            <div className="min-w-0">
              <p
                className="truncate text-2xl"
                style={{ fontFamily: `onair-${preview.brand.type.display}` }}
              >
                {preview.name}
              </p>
              <p
                className="mt-1 text-sm"
                style={{ fontFamily: `onair-${preview.brand.type.body}` }}
              >
                {preview.brand.tagline}
              </p>
            </div>
          </div>
          <div
            className="studio-create-swatches studio-create-arrival"
            key={JSON.stringify(preview.brand.palette)}
          >
            {Object.entries(preview.brand.palette).map(([key, color]) => (
              <div key={key}>
                <div
                  className="h-10 rounded-lg border border-border"
                  style={{ background: color }}
                />
                <p className="mt-1 text-xs text-fg-muted">{color}</p>
              </div>
            ))}
          </div>
          <div
            className="studio-create-specimens studio-create-arrival"
            key={JSON.stringify(preview.brand.type)}
          >
            {(["display", "body"] as const).map((key) => (
              <div key={key}>
                <p className="text-xs text-fg-muted">
                  {key === "display" ? t("Display font") : t("Body font")}
                </p>
                <p
                  className="mt-1 truncate text-2xl"
                  style={{ fontFamily: `onair-${preview.brand.type[key]}` }}
                >
                  {fontNames[preview.brand.type[key]] ?? t("Library font")}
                </p>
              </div>
            ))}
          </div>
          <div className="flex items-center gap-3">
            <span className="text-xs text-fg-muted">{t("Energy")}</span>
            <meter
              className="studio-create-energy"
              min="0"
              max="1"
              value={preview.brand.energy}
              aria-label={t("Energy")}
            />
            <span className="text-xs">{Math.round(preview.brand.energy * 100)}%</span>
          </div>
          <div className="flex flex-wrap gap-2">
            {preview.brand.textures.map((texture) => (
              <span key={texture} className="rounded-full border border-border px-3 py-1 text-xs">
                {textures[texture] ?? texture}
              </span>
            ))}
          </div>
        </div>
        <div className="studio-create-scenes">
          {scenes.map((scene, index) => (
            <div key={`${revision.current}-${scene.id}`} className="min-w-0">
              {ready &&
              preview.scenes.some((next) => next.id === scene.id) &&
              (revealed === null || index < revealed) ? (
                <div className="studio-create-scene">
                  <StyleThumbnail
                    style={preview}
                    scene={scene}
                    bindings={previewBindings}
                    playing={active && !reducedMotion}
                  />
                </div>
              ) : (
                <div className="studio-create-placeholder">
                  <span>{roles[scene.role]}</span>
                </div>
              )}
              <p className="mt-2 text-xs font-medium text-primary">{roles[scene.role]}</p>
              <p className="truncate text-sm">
                {scene.name === scene.role ? roles[scene.role] : scene.name}
              </p>
            </div>
          ))}
          {scenes.length === 0 &&
            ROLES.slice(0, 6).map((role) => (
              <div key={role} className="studio-create-placeholder">
                {roles[role]}
              </div>
            ))}
        </div>
      </div>
    </section>
  );
}
