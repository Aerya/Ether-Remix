import { Badge, Button, Input } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";

/**
 * Test des sources audio (internal/pse/music/diagnose.go) : chaque source
 * réglée est essayée seule sur un même titre, avec le format réellement
 * servi. Le plan montre l'ordre réel de lecture et pourquoi une source est
 * inactive. Les liens d'addons ne sont affichés que par leur hôte ; aucun
 * lien audio ni aucune clé n'est renvoyé.
 */

interface SourceReport {
  name: string;
  kind: string;
  priority: number;
  ok: boolean;
  quality?: string;
  format?: string;
  lossless: boolean;
  sampleRate?: number;
  bitDepth?: number;
  hiRes?: boolean;
  error?: string;
  ms: number;
  stage?: "source" | "link" | "bytes";
}

interface PlanEntry {
  name: string;
  kind: string;
  active: boolean;
  order?: number;
  reason?: string;
}

interface Diagnosis {
  track: string;
  catalog?: boolean;
  plan?: PlanEntry[] | null;
  sources: SourceReport[] | null;
}

const STAGES: { id: "source" | "link" | "bytes"; fr: string; en: string }[] = [
  { id: "source", fr: "Résultat de la source", en: "Source result" },
  { id: "link", fr: "Lien audio obtenu", en: "Audio link obtained" },
  { id: "bytes", fr: "Premiers octets vérifiés", en: "First bytes checked" },
];

const reached = (s: SourceReport, id: string) =>
  STAGES.findIndex((x) => x.id === s.stage) >= STAGES.findIndex((x) => x.id === id);

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export function MusicSources() {
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Diagnosis | null>(null);
  const [error, setError] = useState("");
  const [retrying, setRetrying] = useState("");

  const diagnose = (source?: string) =>
    apiFetch<Diagnosis>("/pse/music/diagnose", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(source ? { query, source } : { query }),
    });

  const run = async () => {
    setBusy(true);
    setError("");
    try {
      setResult(await diagnose());
    } catch (e) {
      setResult(null);
      setError(e instanceof Error ? e.message : String(e));
    }
    setBusy(false);
  };

  // Nouvel essai d'une seule source, sans aucun cache de lecture.
  const retry = async (name: string) => {
    setRetrying(name);
    try {
      const d = await diagnose(name);
      const fresh = d.sources?.find((s) => s.name === name);
      if (fresh) {
        setResult((prev) =>
          prev
            ? { ...prev, sources: (prev.sources ?? []).map((s) => (s.name === name ? fresh : s)) }
            : d,
        );
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
    setRetrying("");
  };

  const sources = result?.sources ?? [];
  return (
    <div className="space-y-3 border-t border-border px-4 py-4">
      <div className="text-sm font-medium">
        {L("Tester les sources audio", "Test audio sources")}
      </div>
      <p className="text-sm text-fg-muted">
        {L(
          "Chaque source est essayée seule, dans l'ordre réel de lecture, sur le même titre, sans les caches de lecture. Le format est vérifié dans les premiers octets : la lecture complète n'est pas testée, et FLAC ne veut pas dire Hi-Res.",
          "Each source is tried on its own, in the real playback order, on the same track, without the playback caches. The format is checked in the first bytes: full playback is not tested, and FLAC does not mean Hi-Res.",
        )}
      </p>
      <div className="flex flex-wrap gap-2">
        <Input
          className="h-9 min-w-0 flex-1"
          aria-label={L("Titre à tester", "Track to test")}
          value={query}
          placeholder="Stromae Papaoutai"
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !busy) void run();
          }}
        />
        <Button size="sm" disabled={busy} onClick={() => void run()}>
          {busy ? L("Test en cours…", "Testing…") : L("Tester les sources", "Test sources")}
        </Button>
      </div>
      {error ? <p className="text-sm text-warning">{error}</p> : null}
      {result ? (
        <div className="space-y-2">
          {result.track ? (
            <p className="text-xs text-fg-muted">
              {L("Titre testé : ", "Tested track: ")}
              {result.track}
            </p>
          ) : null}
          {result.plan && result.plan.length > 0 ? (
            <div className="space-y-1">
              <div className="text-xs font-medium">{L("Ordre de lecture", "Playback order")}</div>
              <ol className="space-y-0.5 text-xs">
                {result.plan
                  .filter((p) => p.active)
                  .sort((a, b) => (a.order ?? 0) - (b.order ?? 0))
                  .map((p) => (
                    <li key={`on-${p.name}`}>
                      {p.order}. {p.name}
                    </li>
                  ))}
              </ol>
              {result.plan.some((p) => !p.active) ? (
                <ul className="space-y-0.5 text-xs text-fg-muted">
                  {result.plan
                    .filter((p) => !p.active)
                    .map((p) => (
                      <li key={`off-${p.name}`}>
                        {L("Inactive : ", "Inactive: ")}
                        {p.name}
                        {p.reason ? ` — ${p.reason}` : ""}
                      </li>
                    ))}
                </ul>
              ) : null}
            </div>
          ) : null}
          {sources.length === 0 ? (
            <p className="text-sm text-fg-muted">
              {L("Aucune source audio réglée.", "No audio source configured.")}
            </p>
          ) : null}
          <ul className="space-y-2">
            {sources.map((s) => (
              <li key={`${s.priority}-${s.name}`} className="rounded-lg border border-border p-3">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <span className="truncate text-sm font-medium">
                    {s.priority}. {s.name}
                  </span>
                  <span className="flex flex-wrap gap-1.5">
                    {s.ok ? (
                      <>
                        <Badge variant={s.lossless ? "success" : "warning"}>
                          {s.format}
                          {s.lossless ? L(" · sans perte", " · lossless") : ""}
                        </Badge>
                        {s.bitDepth && s.sampleRate ? (
                          <Badge variant="outline">
                            {s.bitDepth} bits · {s.sampleRate / 1000} kHz
                            {s.hiRes ? " · Hi-Res" : ""}
                          </Badge>
                        ) : null}
                        <Badge variant="outline">{(s.ms / 1000).toFixed(1)} s</Badge>
                      </>
                    ) : (
                      <Badge variant="secondary">{L("Échec", "Failed")}</Badge>
                    )}
                  </span>
                </div>
                {s.ok && s.quality ? (
                  <p className="mt-1 text-xs text-fg-muted">
                    {L("Qualité annoncée : ", "Advertised quality: ")}
                    {s.quality}
                  </p>
                ) : null}
                <ul className="mt-2 space-y-0.5 text-xs">
                  <li>
                    {result.catalog ? "✓ " : "✗ "}
                    {L("Titre trouvé au catalogue", "Track found in the catalogue")}
                  </li>
                  {STAGES.map((st) => (
                    <li key={st.id} className={reached(s, st.id) ? "" : "text-fg-muted"}>
                      {reached(s, st.id) ? "✓ " : "✗ "}
                      {L(st.fr, st.en)}
                    </li>
                  ))}
                  <li className="text-fg-muted">
                    {"– "}
                    {L("Lecture complète : non testée", "Full playback: not tested")}
                  </li>
                </ul>
                {s.error ? <p className="mt-1 text-xs text-warning">{s.error}</p> : null}
                {!s.ok ? (
                  <Button
                    className="mt-2"
                    size="sm"
                    variant="outline"
                    disabled={busy || retrying !== ""}
                    onClick={() => void retry(s.name)}
                  >
                    {retrying === s.name
                      ? L("Nouvel essai…", "Retrying…")
                      : L("Réessayer cette source", "Retry this source")}
                  </Button>
                ) : null}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </div>
  );
}
