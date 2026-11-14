import { useEffect, useState, type FormEvent } from "react";
import { Badge, Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";
import { formatETA, formatSize } from "./TorrentStatus";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

type BadgeVariant = "success" | "warning" | "destructive" | "secondary" | "outline";

export interface FtpTask {
  id: string;
  name: string;
  size: number;
  state: string;
  current: number;
  speed: number;
  eta?: number;
  reason?: string;
  keep: boolean;
  manual?: boolean;
  reading?: boolean;
}

export interface FtpState {
  protocol: string;
  server: string;
  mode: string;
  writes: boolean;
  files: number;
  movies: number;
  episodes: number;
  scanning: boolean;
  lastScan?: string;
  nextScan?: string;
  scanDirs?: number;
  scanFailed?: string[];
  scanCapped?: boolean;
  scanError?: string;
  error?: string;
  reading: number;
  downloadDir?: string;
  downloadVisible: boolean;
  active: number;
  maxActive: number;
  cacheUsed: number;
  cacheMax: number;
  tasks: FtpTask[] | null;
}

interface SearchResult {
  id: string;
  name: string;
  path: string;
  size: number;
  height?: number;
  langs?: string;
  state: string;
}

interface TestResult {
  ok: boolean;
  message: string;
  fingerprint?: string;
}

// États lisibles d'une copie ou d'un fichier FTP.
export function ftpStateLabel(state: string): [string, BadgeVariant] {
  switch (state) {
    case "available":
      return [L("Sur le serveur", "On the server"), "success"];
    case "queued":
      return [L("En attente", "Waiting"), "warning"];
    case "downloading":
      return [L("Copie en cours", "Copying"), "warning"];
    case "done":
    case "ready":
      return [L("Copie prête", "Copy ready"), "success"];
    case "failed":
      return [L("Échec", "Failed"), "destructive"];
    case "cancelled":
      return [L("Arrêtée", "Stopped"), "outline"];
    case "removed":
      return [L("Effacée du cache", "Removed from cache"), "outline"];
  }
  return [state, "outline"];
}

async function post<T>(path: string, body: unknown = {}): Promise<T> {
  return apiFetch<T>(`/pse/ftp/${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

function when(iso?: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "—" : d.toLocaleString(getLocale());
}

function progressText(t: FtpTask): string {
  if (t.state === "done") return "100 %";
  if (!t.size || !t.current) return "—";
  const pct = Math.min(99, Math.floor((t.current * 100) / t.size));
  const parts = [`${pct} %`];
  if (t.speed > 0) parts.push(`${formatSize(t.speed)}/s`);
  if (t.eta) parts.push(formatETA(t.eta));
  return parts.join(" · ");
}

// Panneau de la source FTP / SFTP : test de connexion (empreinte du
// serveur), index des fichiers, copies et recherche manuelle. Le mot de
// passe et la clé ne transitent jamais ici.
export function FtpStatus({ status }: { status?: Record<string, unknown> }) {
  const [fresh, setFresh] = useState<FtpState>();
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<TestResult>();
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<SearchResult[]>();
  const [writes, setWrites] = useState(false);
  const [acting, setActing] = useState<string>();
  const state = fresh ?? (status?.state as FtpState | undefined);
  useEffect(() => {
    setFresh(undefined);
    setResults(undefined);
    setWrites(false);
  }, [status]);
  const active = status?.active === true;
  const problem = typeof status?.problem === "string" ? status.problem : "";

  async function refresh() {
    setBusy(true);
    try {
      const doc = await apiFetch<{ features: { id: string; status?: Record<string, unknown> }[] }>(
        "/pse/",
      );
      const next = doc.features.find((f) => f.id === "ftp")?.status?.state as FtpState | undefined;
      setFresh(next);
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

  async function scan() {
    try {
      const res = await post<{ ok: boolean; message?: string }>("scan");
      if (!res.ok) toast.error(res.message ?? L("Parcours impossible", "Scan failed"));
      else toast.success(L("Parcours du serveur lancé", "Server scan started"));
      setTimeout(() => void refresh(), 1500);
    } catch (e) {
      toast.error((e as Error).message);
    }
  }

  async function search(e: FormEvent) {
    e.preventDefault();
    if (!query.trim()) return;
    try {
      const res = await post<{
        ok: boolean;
        message?: string;
        results?: SearchResult[];
        writes?: boolean;
      }>("search", { query });
      if (!res.ok) toast.error(res.message ?? L("Recherche impossible", "Search failed"));
      setResults(res.results ?? []);
      setWrites(res.writes === true);
    } catch (err) {
      toast.error((err as Error).message);
    }
  }

  async function act(action: "grab" | "cancel" | "keep", id: string) {
    setActing(id + action);
    try {
      const res = await post<{ ok: boolean; message?: string }>(action, { id });
      if (!res.ok) toast.error(res.message ?? L("Action refusée", "Action refused"));
      else
        toast.success(
          action === "grab"
            ? L("Copie lancée", "Copy started")
            : action === "cancel"
              ? L("Copie arrêtée", "Copy stopped")
              : L("Copie gardée", "Copy kept"),
        );
      await refresh();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setActing(undefined);
    }
  }

  async function copyFingerprint(fp: string) {
    try {
      await navigator.clipboard.writeText(fp);
      toast.success(L("Empreinte copiée", "Fingerprint copied"));
    } catch {
      toast.error(L("Copie impossible : sélectionnez-la", "Copy failed: select it"));
    }
  }

  const tasks = state?.tasks ?? [];
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
              disabled={state?.scanning}
              onClick={() => void scan()}
            >
              {state?.scanning
                ? L("Parcours…", "Scanning…")
                : L("Parcourir le serveur", "Scan server")}
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
        <div role="status" className={test.ok ? "text-fg-muted" : "text-destructive"}>
          <p>{test.message}</p>
          {test.fingerprint ? (
            <p className="mt-1 flex flex-wrap items-center gap-2 text-fg">
              <code className="break-all rounded bg-surface px-1 py-0.5 text-xs">
                {test.fingerprint}
              </code>
              <Button
                size="sm"
                variant="outline"
                onClick={() => void copyFingerprint(test.fingerprint!)}
              >
                {L("Copier", "Copy")}
              </Button>
            </p>
          ) : null}
        </div>
      ) : null}
      {!active && problem ? (
        <p className="text-fg-muted">
          {L(`Source inactive : ${problem}.`, `Source inactive: ${problem}.`)}
        </p>
      ) : null}
      {active && state ? (
        <>
          <ul className="space-y-1">
            <li className="flex flex-wrap items-center gap-2">
              <Badge variant={state.error ? "destructive" : "success"}>
                {state.protocol.toUpperCase()}
              </Badge>
              <span className="text-fg-muted">
                {state.error
                  ? L(`${state.server} : ${state.error}`, `${state.server}: ${state.error}`)
                  : state.server}
              </span>
            </li>
            <li className="flex flex-wrap items-center gap-2">
              <Badge
                variant={state.scanError ? "destructive" : state.files ? "success" : "warning"}
              >
                {L("Index", "Index")}
              </Badge>
              <span className="text-fg-muted">
                {state.scanError
                  ? L(`Parcours en échec : ${state.scanError}`, `Scan failed: ${state.scanError}`)
                  : L(
                      `${state.files} vidéos (${state.movies} films, ${state.episodes} épisodes) · dernier parcours : ${when(state.lastScan)} · prochain : ${when(state.nextScan)}`,
                      `${state.files} videos (${state.movies} movies, ${state.episodes} episodes) · last scan: ${when(state.lastScan)} · next: ${when(state.nextScan)}`,
                    )}
              </span>
            </li>
            {state.scanFailed?.length ? (
              <li className="text-fg-muted">
                {L("Dossiers illisibles : ", "Unreadable folders: ")}
                {state.scanFailed.join(", ")}
              </li>
            ) : null}
            {state.scanCapped ? (
              <li className="text-fg-muted">
                {L(
                  "Parcours plafonné (trop de dossiers ou de fichiers) : réduisez les dossiers à parcourir.",
                  "Scan capped (too many folders or files): narrow the folders to scan.",
                )}
              </li>
            ) : null}
            <li className="flex flex-wrap items-center gap-2">
              <Badge variant={state.writes ? "warning" : "secondary"}>
                {state.writes
                  ? L("Écrit sur le disque", "Writes to disk")
                  : L("Sans copie complète", "No complete copy")}
              </Badge>
              <span className="text-fg-muted">
                {state.writes
                  ? state.downloadVisible
                    ? L(`Copies dans ${state.downloadDir}`, `Copies in ${state.downloadDir}`)
                    : L(
                        `Dossier de téléchargement introuvable (${state.downloadDir}) : vérifiez le volume.`,
                        `Download folder not found (${state.downloadDir}): check the volume.`,
                      )
                  : L(
                      "Lecture directe sans copie complète ; le cache habituel d’Ether reste utilisé.",
                      "Direct playback without a complete copy; Ether’s usual cache still applies.",
                    )}
              </span>
            </li>
          </ul>
          <p className="text-fg-muted">
            {L(
              `Lectures en cours : ${state.reading}${state.writes ? ` · copies : ${state.active} / ${state.maxActive} · cache : ${formatSize(state.cacheUsed)}${state.cacheMax ? ` / ${formatSize(state.cacheMax)}` : ""}` : ""}`,
              `Playing now: ${state.reading}${state.writes ? ` · copies: ${state.active} / ${state.maxActive} · cache: ${formatSize(state.cacheUsed)}${state.cacheMax ? ` / ${formatSize(state.cacheMax)}` : ""}` : ""}`,
            )}
          </p>
          {tasks.length ? (
            <div className="overflow-x-auto">
              <table className="w-full text-left text-sm">
                <thead className="text-fg-muted">
                  <tr>
                    <th className="py-1 pr-3 font-medium">{L("Fichier", "File")}</th>
                    <th className="py-1 pr-3 font-medium">{L("État", "State")}</th>
                    <th className="py-1 pr-3 font-medium">{L("Avancement", "Progress")}</th>
                    <th className="py-1 pr-3 font-medium">{L("Taille", "Size")}</th>
                    <th className="py-1 font-medium" />
                  </tr>
                </thead>
                <tbody>
                  {tasks.map((t) => {
                    const [label, variant] = ftpStateLabel(t.state);
                    const running = t.state === "queued" || t.state === "downloading";
                    return (
                      <tr key={t.id} className="border-t border-border align-top">
                        <td className="max-w-[24rem] truncate py-1 pr-3" title={t.name}>
                          {t.name}
                          {t.keep ? (
                            <span className="ml-2 text-xs text-fg-muted">
                              {L("gardée", "kept")}
                            </span>
                          ) : null}
                        </td>
                        <td className="py-1 pr-3">
                          <Badge variant={variant}>{label}</Badge>
                          {t.reason ? (
                            <div className="mt-1 max-w-[18rem] text-xs text-fg-muted">
                              {t.reason}
                            </div>
                          ) : null}
                        </td>
                        <td className="whitespace-nowrap py-1 pr-3">{progressText(t)}</td>
                        <td className="whitespace-nowrap py-1 pr-3">
                          {t.size ? formatSize(t.size) : "—"}
                        </td>
                        <td className="whitespace-nowrap py-1">
                          {running ? (
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={acting === t.id + "cancel"}
                              onClick={() => void act("cancel", t.id)}
                            >
                              {L("Arrêter", "Stop")}
                            </Button>
                          ) : null}
                          {!t.keep && (running || t.state === "done") ? (
                            <Button
                              size="sm"
                              variant="outline"
                              className="ml-2"
                              disabled={acting === t.id + "keep"}
                              onClick={() => void act("keep", t.id)}
                            >
                              {L("Garder", "Keep")}
                            </Button>
                          ) : null}
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
          ) : null}
          <form className="flex flex-wrap items-center gap-2" onSubmit={(e) => void search(e)}>
            <Input
              className="max-w-sm"
              value={query}
              placeholder={L(
                "Chercher dans l'index, ex. Inception",
                "Search the index, e.g. Inception",
              )}
              aria-label={L("Recherche FTP manuelle", "Manual FTP search")}
              onChange={(e) => setQuery(e.target.value)}
            />
            <Button size="sm" type="submit" disabled={!query.trim()}>
              {L("Chercher", "Search")}
            </Button>
          </form>
          {results ? (
            results.length ? (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-sm">
                  <thead className="text-fg-muted">
                    <tr>
                      <th className="py-1 pr-3 font-medium">{L("Fichier", "File")}</th>
                      <th className="py-1 pr-3 font-medium">{L("État", "State")}</th>
                      <th className="py-1 pr-3 font-medium">{L("Taille", "Size")}</th>
                      <th className="py-1 font-medium" />
                    </tr>
                  </thead>
                  <tbody>
                    {results.map((r) => {
                      const [label, variant] = ftpStateLabel(r.state);
                      return (
                        <tr key={r.id} className="border-t border-border align-top">
                          <td className="max-w-[28rem] truncate py-1 pr-3" title={r.path}>
                            {r.name}
                            <div className="truncate text-xs text-fg-muted">
                              {[r.height ? `${r.height}p` : "", r.langs ?? "", r.path]
                                .filter(Boolean)
                                .join(" · ")}
                            </div>
                          </td>
                          <td className="py-1 pr-3">
                            <Badge variant={variant}>{label}</Badge>
                          </td>
                          <td className="whitespace-nowrap py-1 pr-3">{formatSize(r.size)}</td>
                          <td className="whitespace-nowrap py-1">
                            {writes &&
                            state.writes &&
                            (r.state === "available" || r.state === "failed") ? (
                              <Button
                                size="sm"
                                variant="outline"
                                disabled={acting === r.id + "grab"}
                                onClick={() => void act("grab", r.id)}
                              >
                                {L("Copier sur le disque", "Copy to disk")}
                              </Button>
                            ) : null}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className="text-fg-muted">{L("Aucun fichier trouvé.", "No file found.")}</p>
            )
          ) : null}
          <p className="text-xs text-fg-muted">
            {state.writes
              ? L(
                  "Une copie lancée ici est gardée : Remix ne l'efface jamais. Les copies du mode Téléchargement seulement sont effacées selon la durée de conservation.",
                  "A copy started here is kept: Remix never deletes it. Copies made in Download only mode are deleted after the retention time.",
                )
              : L(
                  "En mode Streaming, la recherche sert à vérifier l'index : aucune copie n'est possible.",
                  "In Streaming mode, search only checks the index: no copy is possible.",
                )}
          </p>
        </>
      ) : null}
    </div>
  );
}
