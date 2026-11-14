import { useState, type FormEvent } from "react";
import { Badge, Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";
import { formatETA, formatSize } from "./TorrentStatus";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

type BadgeVariant = "success" | "warning" | "destructive" | "secondary" | "outline";

export interface XdccTask {
  packet: string;
  name: string;
  size: number;
  bot: string;
  state: string;
  current: number;
  speed: number;
  eta?: number;
  queue?: number;
  reason?: string;
  keep: boolean;
  manual?: boolean;
  reading?: boolean;
}

export interface XdccState {
  mode: string;
  progressive: boolean;
  reachable: boolean;
  error?: string;
  completeDir?: string;
  readyLocal?: string;
  readyVisible: boolean;
  tempLocal?: string;
  tempVisible: boolean;
  active: number;
  maxActive: number;
  today: number;
  maxPerDay: number;
  cacheUsed: number;
  cacheMax: number;
  tasks: XdccTask[] | null;
}

interface SearchResult {
  packet: string;
  name: string;
  size: number;
  bot: string;
  online: boolean;
  queue: number;
  slots?: string;
  state: string;
  reason?: string;
  height?: number;
  langs?: string;
  stream?: string;
}

// États lisibles d'un téléchargement ou d'un résultat XDCC.
export function xdccStateLabel(state: string): [string, BadgeVariant] {
  switch (state) {
    case "available":
      return [L("Disponible", "Available"), "success"];
    case "offline":
      return [L("Bot hors ligne", "Bot offline"), "secondary"];
    case "waiting":
    case "queued":
      return [L("En attente", "Waiting"), "warning"];
    case "downloading":
      return [L("En cours", "Downloading"), "warning"];
    case "playable":
      return [L("Prêt à lire (en cours)", "Ready to play (downloading)"), "success"];
    case "ready":
      return [L("Prêt à lire", "Ready to play"), "success"];
    case "failed":
      return [L("Échec", "Failed"), "destructive"];
    case "cancelled":
      return [L("Arrêté", "Stopped"), "outline"];
    case "removed":
      return [L("Effacé du cache", "Removed from cache"), "outline"];
  }
  return [state, "outline"];
}

async function post<T>(path: string, body: unknown = {}): Promise<T> {
  return apiFetch<T>(`/pse/xdcc/${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
}

function progressText(t: XdccTask): string {
  if (t.state === "ready") return "100 %";
  if (!t.size || !t.current) return t.queue ? L(`file : ${t.queue}`, `queue: ${t.queue}`) : "—";
  const pct = Math.min(99, Math.floor((t.current * 100) / t.size));
  const parts = [`${pct} %`];
  if (t.speed > 0) parts.push(`${formatSize(t.speed)}/s`);
  if (t.eta) parts.push(formatETA(t.eta));
  return parts.join(" · ");
}

// Panneau de la source XDCC : test de Grabscher, dossiers vus par Remix,
// téléchargements et recherche manuelle. La clé d'API ne transite jamais ici.
export function XdccStatus({ status }: { status?: Record<string, unknown> }) {
  const [fresh, setFresh] = useState<XdccState>();
  const [busy, setBusy] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<{ ok: boolean; message: string }>();
  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);
  const [results, setResults] = useState<SearchResult[]>();
  const [acting, setActing] = useState<string>();
  const state = fresh ?? (status?.state as XdccState | undefined);
  const active = status?.active === true;

  async function refresh() {
    setBusy(true);
    try {
      const doc = await apiFetch<{ features: { id: string; status?: Record<string, unknown> }[] }>(
        "/pse/",
      );
      const next = doc.features.find((f) => f.id === "xdcc")?.status?.state as
        XdccState | undefined;
      if (next) setFresh(next);
    } finally {
      setBusy(false);
    }
  }

  async function runTest() {
    setTesting(true);
    try {
      setTest(await post<{ ok: boolean; message: string }>("test"));
    } catch (e) {
      setTest({ ok: false, message: (e as Error).message });
    } finally {
      setTesting(false);
    }
  }

  async function search(e: FormEvent) {
    e.preventDefault();
    if (!query.trim()) return;
    setSearching(true);
    try {
      const res = await post<{ ok: boolean; message?: string; results?: SearchResult[] }>(
        "search",
        { query },
      );
      if (!res.ok) toast.error(res.message ?? L("Recherche impossible", "Search failed"));
      setResults(res.results ?? []);
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setSearching(false);
    }
  }

  async function act(action: "grab" | "cancel" | "keep", packet: string) {
    setActing(packet + action);
    try {
      const res = await post<{ ok: boolean; message?: string }>(action, { packet, keep: true });
      if (!res.ok) toast.error(res.message ?? L("Action refusée", "Action refused"));
      else
        toast.success(
          action === "grab"
            ? L("Téléchargement demandé au bot", "Download requested from the bot")
            : action === "cancel"
              ? L("Téléchargement arrêté", "Download stopped")
              : L("Fichier gardé", "File kept"),
        );
      await refresh();
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setActing(undefined);
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
          <Button size="sm" variant="secondary" disabled={busy} onClick={() => void refresh()}>
            {busy ? L("Actualisation…", "Refreshing…") : L("Actualiser", "Refresh")}
          </Button>
        ) : null}
        {test ? (
          <span className={test.ok ? "text-fg-muted" : "text-destructive"} role="status">
            {test.message}
          </span>
        ) : (
          <span className="text-fg-muted">
            {L(
              "Le test utilise l’adresse et la clé enregistrées.",
              "The test uses the saved address and key.",
            )}
          </span>
        )}
      </div>
      {active && state ? (
        <>
          <ul className="space-y-1">
            <li className="flex flex-wrap items-center gap-2">
              <Badge variant={state.reachable ? "success" : "destructive"}>Grabscher</Badge>
              <span className="text-fg-muted">
                {state.reachable
                  ? L("Joignable", "Reachable")
                  : L(`Injoignable : ${state.error ?? ""}`, `Unreachable: ${state.error ?? ""}`)}
              </span>
            </li>
            <li className="flex flex-wrap items-center gap-2">
              <Badge variant={state.readyVisible ? "success" : "destructive"}>
                {L("Téléchargements", "Downloads")}
              </Badge>
              <span className="text-fg-muted">
                {state.readyVisible
                  ? L(`Vus par Remix : ${state.readyLocal}`, `Seen by Remix: ${state.readyLocal}`)
                  : L(
                      `Invisibles pour Remix (${state.readyLocal || state.completeDir || "?"}) : partagez ce volume, sinon rien ne se lit.`,
                      `Not visible to Remix (${state.readyLocal || state.completeDir || "?"}): share this volume, otherwise nothing plays.`,
                    )}
              </span>
            </li>
            {state.progressive ? (
              <li className="flex flex-wrap items-center gap-2">
                <Badge variant={state.tempVisible ? "success" : "warning"}>
                  {L("Streaming", "Streaming")}
                </Badge>
                <span className="text-fg-muted">
                  {state.tempVisible
                    ? L(
                        "Lecture progressive possible : le dossier temporaire de Grabscher est visible.",
                        "Progressive playback available: Grabscher's temporary folder is visible.",
                      )
                    : L(
                        `Dossier temporaire invisible (${state.tempLocal || "?"}) : la lecture attend la fin du transfert.`,
                        `Temporary folder not visible (${state.tempLocal || "?"}): playback waits for the transfer to finish.`,
                      )}
                </span>
              </li>
            ) : null}
          </ul>
          <p className="text-fg-muted">
            {L(
              `${state.active} / ${state.maxActive} en cours · lancements aujourd’hui : ${state.today} / ${state.maxPerDay} · cache : ${formatSize(state.cacheUsed)}${state.cacheMax ? ` / ${formatSize(state.cacheMax)}` : ""}`,
              `${state.active} / ${state.maxActive} running · started today: ${state.today} / ${state.maxPerDay} · cache: ${formatSize(state.cacheUsed)}${state.cacheMax ? ` / ${formatSize(state.cacheMax)}` : ""}`,
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
                    <th className="py-1 pr-3 font-medium">Bot</th>
                    <th className="py-1 font-medium" />
                  </tr>
                </thead>
                <tbody>
                  {tasks.map((t) => {
                    const [label, variant] = xdccStateLabel(t.state);
                    const running = ["waiting", "offline", "downloading", "playable"].includes(
                      t.state,
                    );
                    return (
                      <tr key={t.packet} className="border-t border-border align-top">
                        <td className="max-w-[24rem] truncate py-1 pr-3" title={t.name}>
                          {t.name}
                          {t.keep ? (
                            <span className="ml-2 text-xs text-fg-muted">{L("gardé", "kept")}</span>
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
                        <td className="whitespace-nowrap py-1 pr-3">{t.bot}</td>
                        <td className="whitespace-nowrap py-1">
                          {running && !t.reading ? (
                            <Button
                              size="sm"
                              variant="outline"
                              disabled={acting === t.packet + "cancel"}
                              onClick={() => void act("cancel", t.packet)}
                            >
                              {L("Arrêter", "Stop")}
                            </Button>
                          ) : null}
                          {!t.keep && (running || t.state === "ready") ? (
                            <Button
                              size="sm"
                              variant="outline"
                              className="ml-2"
                              disabled={acting === t.packet + "keep"}
                              onClick={() => void act("keep", t.packet)}
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
          ) : (
            <p className="text-fg-muted">
              {L("Aucun téléchargement XDCC pour l’instant.", "No XDCC download yet.")}
            </p>
          )}
          <form className="flex flex-wrap items-center gap-2" onSubmit={(e) => void search(e)}>
            <Input
              className="max-w-sm"
              value={query}
              placeholder={L(
                "Recherche manuelle, ex. Inception 2010",
                "Manual search, e.g. Inception 2010",
              )}
              aria-label={L("Recherche XDCC manuelle", "Manual XDCC search")}
              onChange={(e) => setQuery(e.target.value)}
            />
            <Button size="sm" type="submit" disabled={searching || !query.trim()}>
              {searching ? L("Recherche…", "Searching…") : L("Chercher", "Search")}
            </Button>
          </form>
          {results ? (
            results.length ? (
              <div className="overflow-x-auto">
                <table className="w-full text-left text-sm">
                  <thead className="text-fg-muted">
                    <tr>
                      <th className="py-1 pr-3 font-medium">{L("Paquet", "Packet")}</th>
                      <th className="py-1 pr-3 font-medium">{L("État", "State")}</th>
                      <th className="py-1 pr-3 font-medium">{L("Taille", "Size")}</th>
                      <th className="py-1 pr-3 font-medium">Bot</th>
                      <th className="py-1 font-medium" />
                    </tr>
                  </thead>
                  <tbody>
                    {results.map((r) => {
                      const [label, variant] = xdccStateLabel(r.state);
                      return (
                        <tr key={r.packet} className="border-t border-border align-top">
                          <td className="max-w-[24rem] truncate py-1 pr-3" title={r.name}>
                            {r.name}
                            <div className="text-xs text-fg-muted">
                              {[
                                r.height ? `${r.height}p` : "",
                                r.langs ?? "",
                                r.stream === "no"
                                  ? L("pas une vidéo lisible", "not a playable video")
                                  : "",
                              ]
                                .filter(Boolean)
                                .join(" · ")}
                            </div>
                          </td>
                          <td className="py-1 pr-3">
                            <Badge variant={variant}>{label}</Badge>
                            {r.reason ? (
                              <div className="mt-1 max-w-[18rem] text-xs text-fg-muted">
                                {r.reason}
                              </div>
                            ) : null}
                          </td>
                          <td className="whitespace-nowrap py-1 pr-3">{formatSize(r.size)}</td>
                          <td className="whitespace-nowrap py-1 pr-3">
                            {r.bot || "?"}
                            {r.slots ? (
                              <span className="ml-1 text-xs text-fg-muted">
                                {L(`créneaux ${r.slots}`, `slots ${r.slots}`)}
                              </span>
                            ) : null}
                          </td>
                          <td className="whitespace-nowrap py-1">
                            {r.state === "available" || r.state === "failed" ? (
                              <Button
                                size="sm"
                                variant="outline"
                                disabled={!r.online || acting === r.packet + "grab"}
                                onClick={() => void act("grab", r.packet)}
                              >
                                {L("Télécharger", "Download")}
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
              <p className="text-fg-muted">{L("Aucun paquet trouvé.", "No packet found.")}</p>
            )
          ) : null}
          <p className="text-xs text-fg-muted">
            {L(
              "Un téléchargement manuel est gardé : Remix ne l’efface jamais. Les lectures en mode Streaming utilisent le cache, effacé selon la durée de conservation.",
              "A manual download is kept: Remix never deletes it. Playback in Streaming mode uses the cache, deleted after the retention time.",
            )}
          </p>
        </>
      ) : null}
    </div>
  );
}
