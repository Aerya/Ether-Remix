import { Button } from "@braedonsaunders/appkit-ui";
import { AlertTriangle, CircleCheck, CircleHelp, CircleX, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useApp } from "../app-context";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { worstLevel, type HealthItem, type HealthLevel } from "../lib/health";
import type { DashboardData } from "../lib/types";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export interface DiagnosticRow {
  id: string;
  level: HealthLevel;
  label: string;
  detail?: string;
  action?: { label: string; to: string };
}
export interface PseSignals {
  indexing: boolean | null;
  state: string | null;
  playable: number | null;
}

const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : 0);

/** Contrôles du diagnostic, calculés à partir de l’état déjà connu de la console. */
export function buildDiagnostic(
  dashboard: DashboardData,
  health: HealthItem[],
  pse: PseSignals,
): DiagnosticRow[] {
  const step = (id: string) => dashboard.setupSteps?.find((s) => s.id === id);
  const rows: DiagnosticRow[] = [];
  const sources = step("sources");
  rows.push(
    sources?.complete
      ? { id: "source", level: "ok", label: L("Une source est active", "A source is active") }
      : {
          id: "source",
          level: "error",
          label: L("Aucune source active", "No active source"),
          action: { label: L("Ajouter une source", "Add a source"), to: "/remix/sources" },
        },
  );
  const sourceItems = health.filter((h) => h.id === "usenet" || h.id === "indexers");
  const checked = sourceItems.filter((h) => !h.setupGap && h.level !== "off");
  const worst = worstLevel(checked.map((h) => h.level));
  const worstItem = checked.find((h) => h.level === worst);
  rows.push({
    id: "respond",
    level: checked.length === 0 ? "unknown" : worst,
    label:
      worst === "ok" && checked.length
        ? L("Les sources répondent", "Sources respond")
        : worst === "error" || worst === "warn"
          ? L("Une source pose problème", "A source has a problem")
          : L("Réponse des sources non vérifiée", "Source response not verified"),
    detail: worstItem?.summary,
    action: { label: L("Voir les sources", "View sources"), to: "/remix/sources" },
  });
  const catalog = step("catalog");
  if (catalog && !catalog.complete)
    rows.push({
      id: "tmdb",
      level: "error",
      label: L("Clé TMDB manquante : pas de catalogue", "TMDB key missing: no catalogue"),
      action: { label: L("Ouvrir les réglages", "Open settings"), to: "/services/tmdb" },
    });
  if (pse.indexing === false)
    rows.push({
      id: "indexing",
      level: "warn",
      label: L("La préparation automatique est désactivée", "Automatic preparation is off"),
      detail: L(
        "Sans elle, le catalogue « lisible uniquement » reste vide.",
        "Without it, the playable-only catalogue stays empty.",
      ),
      action: {
        label: L("Régler l’indexation", "Configure indexing"),
        to: "/remix/indexation#pse-pse_indexer",
      },
    });
  if (pse.indexing && pse.state === "no_sources")
    rows.push({
      id: "indexsources",
      level: "warn",
      label: L("Aucune source sélectionnée pour l’indexation", "No source selected for indexing"),
      action: { label: L("Choisir les sources", "Choose sources"), to: "/remix/indexation" },
    });
  if (pse.indexing)
    rows.push(
      (pse.playable ?? 0) > 0
        ? {
            id: "playable",
            level: "ok",
            label: L("Des titres sont prêts à lire", "Titles are ready to play"),
          }
        : {
            id: "playable",
            level: "warn",
            label: L("Aucun titre prêt à lire pour l’instant", "No title ready to play yet"),
            detail: L(
              "Le catalogue se remplit progressivement, pas tout de suite après l’installation.",
              "The catalogue fills progressively, not right after installation.",
            ),
            action: {
              label: L("Voir l’activité", "View activity"),
              to: "/remix/apercu?view=recherches",
            },
          },
    );
  const storage = health.filter((h) => ["cache", "storage", "database"].includes(h.id));
  const sworst = worstLevel(storage.map((h) => h.level));
  const sitem = storage.find((h) => h.level === sworst);
  if (sworst === "error" || sworst === "warn")
    rows.push({
      id: "storage",
      level: sworst,
      label: L("Stockage ou cache à vérifier", "Storage or cache needs attention"),
      detail: sitem?.summary,
      action: { label: L("Ouvrir", "Open"), to: sitem?.href ?? "/" },
    });
  rows.push({
    id: "apps",
    level: "unknown",
    label: L("Application connectée : à vérifier", "Connected app: to check"),
    detail: L(
      "Remix ne peut pas savoir seul si votre client est connecté. Essayez de lancer un titre.",
      "Remix cannot tell on its own whether your client is connected. Try playing a title.",
    ),
    action: { label: L("Connecter", "Connect"), to: "/apps" },
  });
  return rows;
}

const ICON = {
  ok: <CircleCheck className="h-5 w-5 text-success" aria-hidden="true" />,
  warn: <AlertTriangle className="h-5 w-5 text-warning" aria-hidden="true" />,
  error: <CircleX className="h-5 w-5 text-danger" aria-hidden="true" />,
  off: <CircleHelp className="h-5 w-5 text-fg-muted" aria-hidden="true" />,
  unknown: <CircleHelp className="h-5 w-5 text-fg-muted" aria-hidden="true" />,
} as const;
const STATE = {
  ok: ["OK", "OK"],
  warn: ["Attention", "Warning"],
  error: ["Problème", "Problem"],
  off: ["Inactif", "Off"],
  unknown: ["À vérifier", "To check"],
} as const;

/** Page Diagnostic : des verdicts clairs et une action par problème. */
export function RemixDiagnostic() {
  const { dashboard, health, refreshDashboard } = useApp();
  const navigate = useNavigate();
  const [pse, setPse] = useState<PseSignals>({ indexing: null, state: null, playable: null });
  const [busy, setBusy] = useState(false);
  const load = useCallback(async () => {
    setBusy(true);
    const [settings, activity, details] = await Promise.all([
      apiFetch<{ features?: { fields: { key: string; value: string }[] }[] }>("/pse/").catch(
        () => null,
      ),
      apiFetch<{ state?: string }>("/pse/indexer/activity").catch(() => null),
      apiFetch<{ types?: Record<string, { playable?: number }> | null }>(
        "/pse/indexer/details",
      ).catch(() => null),
    ]);
    const flag = settings?.features
      ?.flatMap((f) => f.fields)
      .find((f) => f.key === "PSE_INDEXER")?.value;
    setPse({
      indexing: flag === undefined ? null : flag === "on",
      state: activity?.state ?? null,
      playable: details?.types
        ? Object.values(details.types).reduce((n, c) => n + num(c?.playable), 0)
        : null,
    });
    setBusy(false);
  }, []);
  useEffect(() => {
    void load();
  }, [load]);
  if (!dashboard) return null;
  const rows = buildDiagnostic(dashboard, health, pse);
  const problems = rows.filter((r) => r.level === "error" || r.level === "warn").length;
  return (
    <section className="space-y-4" aria-label={L("Diagnostic", "Diagnostics")}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-lg font-semibold">{L("Diagnostic", "Diagnostics")}</h2>
          <p className="text-sm text-fg-muted">
            {problems === 0
              ? L("Aucun problème détecté.", "No problem detected.")
              : L(`${problems} point(s) à corriger.`, `${problems} item(s) to fix.`)}
          </p>
        </div>
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            void refreshDashboard();
            void load();
          }}
        >
          <RefreshCw
            className={`mr-1.5 h-4 w-4 ${busy ? "animate-spin" : ""}`}
            aria-hidden="true"
          />
          {L("Relancer le diagnostic", "Run again")}
        </Button>
      </div>
      <ul className="divide-y divide-border rounded-xl border border-border">
        {rows.map((r) => (
          <li key={r.id} className="flex items-center gap-3 p-4">
            {ICON[r.level]}
            <div className="min-w-0 flex-1">
              <p className="font-medium">
                {r.label}
                <span className="sr-only"> — {L(STATE[r.level][0], STATE[r.level][1])}</span>
              </p>
              {r.detail ? <p className="text-sm text-fg-muted">{r.detail}</p> : null}
            </div>
            {r.action && r.level !== "ok" ? (
              <Button
                size="sm"
                variant={r.level === "error" ? "default" : "outline"}
                onClick={() => navigate(r.action!.to)}
              >
                {r.action.label}
              </Button>
            ) : null}
          </li>
        ))}
      </ul>
    </section>
  );
}
