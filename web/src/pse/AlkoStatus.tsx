import { useEffect, useState } from "react";
import { Badge, Button, toast } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export interface AlkoState {
  state: "idle" | "syncing" | "error";
  phase?: "full" | "incremental" | "publish";
  read?: number;
  files: number;
  movies: number;
  episodes: number;
  published: number;
  last?: number;
  lastFull?: number;
  journal?: "ok" | "waiting" | "inactive";
  seq?: number;
  langFR: boolean;
  every: number;
  error?: string;
}

interface TestResult {
  ok: boolean;
  message: string;
  code?: string;
}

/** Messages génériques : le serveur ne renvoie qu'un code, jamais l'adresse, la clé ni un lien. */
export function alkoErrorText(code: string): string {
  switch (code) {
    case "key":
      return L(
        "Clé de lecture refusée : recopiez celle de la page /admin/alko-lecture de StreamFusion.",
        "Read key refused: copy the one from StreamFusion’s /admin/alko-lecture page.",
      );
    case "old":
      return L(
        "API absente : StreamFusion est trop ancien ou la lecture ALKO est désactivée (SFR_ALKO_ENABLED).",
        "API not found: StreamFusion is too old or ALKO reading is disabled (SFR_ALKO_ENABLED).",
      );
    case "views":
      return L(
        "Les vues Alkodiques de StreamFusion sont indisponibles (migration non appliquée ?).",
        "StreamFusion’s Alkodiques views are unavailable (migration not applied?).",
      );
    case "unreachable":
      return L(
        "StreamFusion injoignable : vérifiez l’adresse et le réseau Docker commun.",
        "StreamFusion unreachable: check the address and the shared Docker network.",
      );
    case "params":
    case "protocol":
      return L("Réponse inattendue de StreamFusion.", "Unexpected response from StreamFusion.");
  }
  return L("Synchronisation interrompue (voir les journaux).", "Sync interrupted (see the logs).");
}

function when(unix?: number): string {
  if (!unix) return "—";
  return new Date(unix * 1000).toLocaleString(getLocale());
}

function phaseText(s: AlkoState): string {
  if (s.phase === "full")
    return L(
      `Lecture complète du catalogue : ${s.read ?? 0} fichiers lus`,
      `Full catalogue read: ${s.read ?? 0} files read`,
    );
  if (s.phase === "publish")
    return L("Publication dans le catalogue…", "Publishing to the catalogue…");
  return L("Suivi des modifications…", "Following changes…");
}

function journalText(j?: string): [string, "success" | "warning" | "secondary"] {
  switch (j) {
    case "ok":
      return [L("Journal à jour", "Journal up to date"), "success"];
    case "waiting":
      return [
        L(
          "Journal en retard : relecture complète si l’écart dure",
          "Journal behind: full re-read if it persists",
        ),
        "warning",
      ];
    case "inactive":
      return [
        L(
          "Journal inactif côté StreamFusion : relecture complète périodique",
          "Journal inactive on StreamFusion: periodic full re-read",
        ),
        "warning",
      ];
  }
  return [L("Journal inconnu", "Journal unknown"), "secondary"];
}

async function post<T>(path: string): Promise<T> {
  return apiFetch<T>(`/pse/alko/${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  });
}

// Panneau de la source Alkodiques (API StreamFusion) : test de l'API, fichiers
// du miroir local, état du journal et dernière erreur générique. La clé et
// les liens ne transitent jamais ici.
export function AlkoStatus({ status }: { status?: Record<string, unknown> }) {
  const [fresh, setFresh] = useState<AlkoState>();
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<TestResult>();
  const state = fresh ?? (status?.state as AlkoState | undefined);
  useEffect(() => setFresh(undefined), [status]);
  const active = status?.active === true;
  const problem = typeof status?.problem === "string" ? status.problem : "";

  async function refresh() {
    setBusy(true);
    try {
      const doc = await apiFetch<{ features: { id: string; status?: Record<string, unknown> }[] }>(
        "/pse/",
      );
      setFresh(doc.features.find((f) => f.id === "alko")?.status?.state as AlkoState | undefined);
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function runTest() {
    setTesting(true);
    try {
      setTest(await post<TestResult>("test"));
    } catch (e) {
      setTest({ ok: false, message: (e as Error).message });
    } finally {
      setTesting(false);
    }
  }

  async function sync() {
    try {
      const res = await post<{ ok: boolean; message?: string }>("sync");
      if (!res.ok) toast.error(res.message ?? L("Synchronisation impossible", "Sync failed"));
      else toast.success(L("Synchronisation lancée", "Sync started"));
      setTimeout(() => void refresh(), 1500);
    } catch (e) {
      toast.error((e as Error).message);
    }
  }

  const [journalLabel, journalVariant] = journalText(state?.journal);
  return (
    <div className="space-y-4 border-b border-border px-4 py-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant="secondary" disabled={testing} onClick={() => void runTest()}>
          {testing ? L("Test…", "Testing…") : L("Tester la connexion", "Test connection")}
        </Button>
        {active ? (
          <>
            <Button
              size="sm"
              variant="secondary"
              disabled={state?.state === "syncing"}
              onClick={() => void sync()}
            >
              {state?.state === "syncing"
                ? L("Synchronisation…", "Syncing…")
                : L("Synchroniser maintenant", "Sync now")}
            </Button>
            <Button size="sm" variant="secondary" disabled={busy} onClick={() => void refresh()}>
              {busy ? L("Actualisation…", "Refreshing…") : L("Actualiser", "Refresh")}
            </Button>
          </>
        ) : null}
        {!test ? (
          <span className="text-fg-muted">
            {L("Le test utilise les réglages enregistrés.", "The test uses the saved settings.")}
          </span>
        ) : null}
      </div>
      {test ? (
        <p role="status" className={test.ok ? "text-fg-muted" : "text-destructive"}>
          {test.message}
        </p>
      ) : null}
      {!active && problem ? (
        <p className="text-fg-muted">
          {L(`Source inactive : ${problem}.`, `Source inactive: ${problem}.`)}
        </p>
      ) : null}
      {active && state ? (
        <ul className="space-y-1">
          <li className="flex flex-wrap items-center gap-2">
            <Badge
              variant={
                state.state === "error"
                  ? "destructive"
                  : state.state === "syncing"
                    ? "warning"
                    : "success"
              }
            >
              {state.state === "syncing"
                ? L("En cours", "Running")
                : state.state === "error"
                  ? L("Erreur", "Error")
                  : L("À jour", "Up to date")}
            </Badge>
            <span className="text-fg-muted">
              {state.state === "syncing"
                ? phaseText(state)
                : L(
                    `Dernière synchronisation : ${when(state.last)}`,
                    `Last sync: ${when(state.last)}`,
                  )}
            </span>
          </li>
          {state.error ? (
            <li className="text-destructive" role="alert">
              {alkoErrorText(state.error)}
            </li>
          ) : null}
          <li className="flex flex-wrap items-center gap-2">
            <Badge variant={state.files ? "success" : "warning"}>{L("Miroir", "Mirror")}</Badge>
            <span className="text-fg-muted">
              {L(
                `${state.files} fichiers (${state.movies} films, ${state.episodes} épisodes) · ${state.published} publiés pour votre catalogue`,
                `${state.files} files (${state.movies} movies, ${state.episodes} episodes) · ${state.published} published for your catalogue`,
              )}
            </span>
          </li>
          <li className="flex flex-wrap items-center gap-2">
            <Badge variant={journalVariant}>{L("Journal", "Journal")}</Badge>
            <span className="text-fg-muted">{journalLabel}</span>
          </li>
          <li className="flex flex-wrap items-center gap-2">
            <Badge variant="secondary">{L("Langue", "Language")}</Badge>
            <span className="text-fg-muted">
              {state.langFR
                ? L("FR, MULTi et VOSTFR seulement", "French, MULTi and VOSTFR only")
                : L("Toutes les langues", "All languages")}
              {" · "}
              {L(
                `intervalle : ${Math.round(state.every / 60)} min · dernière relecture complète : ${when(state.lastFull)}`,
                `interval: ${Math.round(state.every / 60)} min · last full read: ${when(state.lastFull)}`,
              )}
            </span>
          </li>
        </ul>
      ) : null}
    </div>
  );
}
