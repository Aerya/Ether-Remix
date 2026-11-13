import { Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { useEffect, useRef, useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";

const BASE = "/pse/music/playlists/";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
interface Viewer {
  id: number;
  name: string;
  spotify?: boolean;
}
interface Entry {
  title: string;
  artist: string;
}
interface Preview {
  token: string;
  name: string;
  source: string;
  url?: string;
  tracks: Entry[];
  total: number;
  skipped: number;
  truncated: boolean;
}
interface Job {
  id: string;
  done: boolean;
  processed: number;
  total: number;
  matched: number;
  missing: Entry[];
  error?: string;
  playlistId?: number;
}
interface Choice {
  name: string;
  url: string;
}
const post = <T,>(path: string, body: object) =>
  apiFetch<T>(BASE + path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function PlaylistBrowser({
  viewers,
  mode,
  refresh,
}: {
  viewers: Viewer[];
  mode: "discover" | "import";
  refresh: () => void;
}) {
  const [viewer, setViewer] = useState(viewers[0]?.id ?? 0);
  const [service, setService] = useState("deezer");
  const [query, setQuery] = useState("");
  const [offset, setOffset] = useState(0);
  const [items, setItems] = useState<Choice[]>([]);
  const [more, setMore] = useState(false);
  const [searched, setSearched] = useState(false);
  const [url, setURL] = useState("");
  const [file, setFile] = useState<{ name: string; format: string; file: string } | null>(null);
  const [preview, setPreview] = useState<Preview | null>(null);
  const [job, setJob] = useState<Job | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [clientID, setClientID] = useState("");
  const callbackStarted = useRef(false);
  const redirectURI = `${window.location.origin}/ether/remix/contenus?content=musique`;
  const selected = viewers.find((v) => v.id === viewer);
  const running = job && !job.done;
  const locked = busy || Boolean(running);
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (
      (!params.has("code") && !params.has("error")) ||
      !params.has("state") ||
      callbackStarted.current
    )
      return;
    callbackStarted.current = true;
    let pending: { viewer: number; state: string } | null = null;
    try {
      pending = JSON.parse(sessionStorage.getItem("remix.spotify.pending") ?? "null");
      sessionStorage.removeItem("remix.spotify.pending");
    } catch {
      /* unavailable storage */
    }
    const code = params.get("code"),
      state = params.get("state");
    const denied = params.has("error");
    for (const key of ["code", "state", "error", "error_description"]) params.delete(key);
    window.history.replaceState(null, "", `${window.location.pathname}?${params}`);
    if (!pending || pending.state !== state || !viewers.some((v) => v.id === pending!.viewer)) {
      setError(
        L(
          "Retour Spotify invalide ou expiré. Recommencez la connexion.",
          "Invalid or expired Spotify return. Connect again.",
        ),
      );
      return;
    }
    if (denied || !code) {
      setError(L("Connexion Spotify annulée.", "Spotify connection cancelled."));
      return;
    }
    setViewer(pending.viewer);
    setService("spotify");
    setBusy(true);
    void post("spotify/callback", { viewer: pending.viewer, state, code })
      .then(() => {
        refreshRef.current();
        toast.success(L("Spotify connecté", "Spotify connected"));
      })
      .catch((e) => setError(errorText(e)))
      .finally(() => setBusy(false));
  }, [viewers]);

  useEffect(() => {
    if (!running || !job) return;
    let active = true;
    const timer = window.setInterval(() => {
      void apiFetch<Job>(`${BASE}import?viewer=${viewer}&id=${encodeURIComponent(job.id)}`)
        .then((next) => {
          if (!active) return;
          setJob(next);
          if (next.done) refreshRef.current();
        })
        .catch((e) => {
          if (active) {
            setError(errorText(e));
            setJob((j) => (j ? { ...j, done: true, error: errorText(e) } : null));
          }
        });
    }, 2000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [running, job?.id, viewer]);

  const act = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  const discover = (page = 0) =>
    act(async () => {
      const args = new URLSearchParams({
        service,
        viewer: String(viewer),
        q: query,
        offset: String(page),
      });
      const r = await apiFetch<{ items: Choice[]; more: boolean }>(BASE + "discover?" + args);
      setItems(r.items);
      setMore(r.more && page < 1000);
      setOffset(page);
      setSearched(true);
    });
  const inspect = (link?: string) =>
    act(async () => {
      setPreview(null);
      setJob(null);
      setPreview(
        await post<Preview>("preview", { viewer, ...(link ? { url: link } : (file ?? { url })) }),
      );
    });
  const connect = () =>
    act(async () => {
      const r = await post<{ url: string }>("spotify/connect", {
        viewer,
        clientID: clientID.trim(),
        redirectURI,
      });
      const state = new URL(r.url).searchParams.get("state");
      sessionStorage.setItem("remix.spotify.pending", JSON.stringify({ viewer, state }));
      window.location.assign(r.url);
    });
  if (!viewers.length)
    return (
      <p className="text-sm text-fg-muted">
        {L(
          "Ajoutez un spectateur pour importer des playlists.",
          "Add a viewer to import playlists.",
        )}
      </p>
    );
  return (
    <div className="space-y-4">
      <label className="block space-y-1 text-sm">
        <span>{L("Importer pour", "Import for")}</span>
        <select
          className="block w-full rounded-md border border-border bg-surface p-2"
          aria-label={L("Spectateur destinataire", "Target viewer")}
          value={viewer}
          disabled={locked}
          onChange={(e) => {
            setViewer(Number(e.target.value));
            setPreview(null);
            setJob(null);
            setItems([]);
            setSearched(false);
            setError("");
          }}
        >
          {viewers.map((v) => (
            <option key={v.id} value={v.id}>
              {v.name}
            </option>
          ))}
        </select>
      </label>
      {mode === "discover" ? (
        <>
          <div className="flex flex-wrap gap-2">
            {["deezer", "spotify"].map((s) => (
              <Button
                key={s}
                size="sm"
                variant={service === s ? "default" : "outline"}
                disabled={locked}
                aria-pressed={service === s}
                onClick={() => {
                  setService(s);
                  setItems([]);
                  setSearched(false);
                  setPreview(null);
                  setError("");
                }}
              >
                {s === "deezer" ? "Deezer" : "Spotify"}
              </Button>
            ))}
          </div>
          {service === "spotify" && (
            <div className="space-y-3 rounded-lg border border-border p-3">
              <p className="text-sm text-fg-muted">
                {L(
                  "Playlists personnelles et collaboratives. Spotify peut refuser les titres d’autres playlists publiques. En mode développement, le propriétaire de l’application doit avoir Premium et autoriser les utilisateurs dans son tableau de bord.",
                  "Personal and collaborative playlists. Spotify may deny access to other public playlists. In development mode, the app owner needs Premium and users must be allowed in its dashboard.",
                )}
              </p>
              {selected?.spotify ? (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={locked}
                  onClick={() =>
                    void act(async () => {
                      await post("spotify/disconnect", { viewer });
                      setItems([]);
                      setPreview(null);
                      refresh();
                    })
                  }
                >
                  {L("Déconnecter Spotify", "Disconnect Spotify")}
                </Button>
              ) : (
                <>
                  <label className="block space-y-1 text-sm">
                    Client ID Spotify
                    <Input
                      aria-label="Client ID Spotify"
                      value={clientID}
                      onChange={(e) => setClientID(e.target.value)}
                      autoComplete="off"
                    />
                  </label>
                  <p className="break-all text-xs text-fg-muted">
                    {L(
                      "URI de redirection à ajouter dans votre application Spotify :",
                      "Redirect URI to add to your Spotify application:",
                    )}{" "}
                    <code>{redirectURI}</code>
                  </p>
                  <Button
                    size="sm"
                    disabled={locked || !clientID.trim()}
                    onClick={() => void connect()}
                  >
                    {L("Connecter Spotify", "Connect Spotify")}
                  </Button>
                </>
              )}
            </div>
          )}
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              void discover();
            }}
          >
            {service === "deezer" && (
              <Input
                aria-label={L("Rechercher des playlists Deezer", "Search Deezer playlists")}
                placeholder={L("Genre, ambiance, artiste…", "Genre, mood, artist…")}
                value={query}
                disabled={locked}
                onChange={(e) => setQuery(e.target.value)}
              />
            )}
            <Button size="sm" disabled={locked || (service === "spotify" && !selected?.spotify)}>
              {service === "deezer"
                ? L("Rechercher / tendances", "Search / trending")
                : L("Mes playlists", "My playlists")}
            </Button>
          </form>
          {searched && !items.length && (
            <p className="text-sm text-fg-muted">
              {L("Aucune playlist trouvée.", "No playlists found.")}
            </p>
          )}
          <ul className="grid gap-2 sm:grid-cols-2">
            {items.map((p) => (
              <li
                key={p.url}
                className="flex items-center justify-between gap-2 rounded-lg border border-border p-3"
              >
                <a href={p.url} target="_blank" rel="noreferrer" className="text-sm underline">
                  {p.name}
                </a>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={locked}
                  onClick={() => void inspect(p.url)}
                >
                  {L("Aperçu", "Preview")}
                </Button>
              </li>
            ))}
          </ul>
          {searched && (offset > 0 || more) && (
            <div className="flex gap-2">
              <Button
                size="sm"
                disabled={locked || offset === 0}
                onClick={() => void discover(offset - 20)}
              >
                {L("Précédent", "Previous")}
              </Button>
              <Button
                size="sm"
                disabled={locked || !more}
                onClick={() => void discover(offset + 20)}
              >
                {L("Suivant", "Next")}
              </Button>
            </div>
          )}
          <details className="rounded-lg border border-border p-3">
            <summary className="cursor-pointer text-sm font-medium">
              {L("D’autres idées de playlists", "More playlist ideas")}
            </summary>
            <p className="my-2 text-xs text-fg-muted">
              {L(
                "Explorez ces sites, puis importez le lien Deezer, Spotify ou ListenBrainz depuis « Importer ». Les annuaires ne garantissent pas l’accès aux titres Spotify.",
                "Explore these sites, then use Import with a Deezer, Spotify or ListenBrainz link. Directories do not guarantee access to Spotify tracks.",
              )}
            </p>
            <div className="flex flex-wrap gap-4 text-sm underline">
              {[
                ["ListenBrainz", "https://listenbrainz.org/"],
                ["Soundplate", "https://soundplate.com/spotify-playlist-search/"],
                ["DailyPlaylists", "https://dailyplaylists.com/"],
              ].map(([name, href]) => (
                <a key={href} href={href} target="_blank" rel="noreferrer">
                  {name} ↗
                </a>
              ))}
            </div>
          </details>
        </>
      ) : (
        <div className="space-y-3">
          <label className="block space-y-1 text-sm">
            {L("Lien Deezer, Spotify ou ListenBrainz", "Deezer, Spotify or ListenBrainz link")}
            <Input
              value={url}
              disabled={locked}
              onChange={(e) => {
                setURL(e.target.value);
                setFile(null);
                setPreview(null);
              }}
              placeholder="https://www.deezer.com/playlist/…"
            />
          </label>
          <label className="block space-y-1 text-sm">
            {L("Ou un fichier CSV / XSPF (2 Mo maximum)", "Or a CSV / XSPF file (2 MB maximum)")}
            <input
              className="block w-full text-sm"
              type="file"
              accept=".csv,.xspf"
              disabled={locked}
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (!f) return;
                void act(async () => {
                  if (f.size > 1800000)
                    throw new Error(L("Fichier trop volumineux", "File too large"));
                  const format = f.name.split(".").pop()?.toLowerCase() ?? "";
                  if (!["csv", "xspf"].includes(format)) throw new Error("CSV / XSPF");
                  setFile({ name: f.name.replace(/\.[^.]+$/, ""), format, file: await f.text() });
                  setURL("");
                  setPreview(null);
                });
              }}
            />
          </label>
          <p className="text-xs text-fg-muted">
            {L(
              "CSV : colonnes title et artist (exports Soundiiz, Exportify…). L’import crée une copie personnelle ; les comptes liés dans « Mes comptes » restent synchronisés automatiquement. Spotify se connecte dans « Découvrir ».",
              "CSV: title and artist columns (Soundiiz, Exportify exports…). Import creates a personal copy; linked accounts in My accounts keep syncing automatically. Connect Spotify under Discover.",
            )}
          </p>
          <Button
            size="sm"
            disabled={locked || (!url.trim() && !file)}
            onClick={() => void inspect()}
          >
            {L("Afficher l’aperçu", "Show preview")}
          </Button>
        </div>
      )}
      {error && (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      )}
      {busy && (
        <p role="status" className="text-sm text-fg-muted">
          {L("Chargement…", "Loading…")}
        </p>
      )}
      {preview && (
        <section className="space-y-3 rounded-lg border border-border p-4">
          <h3 className="font-semibold">{preview.name}</h3>
          <p className="text-sm text-fg-muted">
            {preview.tracks.length} / {preview.total} {L("titres accessibles", "accessible tracks")}{" "}
            · {preview.source}
            {preview.skipped > 0 ? ` · ${preview.skipped} ${L("ignorés", "skipped")}` : ""}
          </p>
          {preview.url && (
            <a className="text-sm underline" href={preview.url} target="_blank" rel="noreferrer">
              {L("Ouvrir la playlist d’origine", "Open original playlist")} ↗
            </a>
          )}
          {preview.truncated && (
            <p className="text-sm text-warning">
              {L(
                "Import limité aux 2 000 premiers éléments.",
                "Import limited to the first 2,000 items.",
              )}
            </p>
          )}
          <ol className="max-h-52 list-inside list-decimal overflow-auto text-sm">
            {preview.tracks.map((t, i) => (
              <li key={i}>
                {t.artist} — {t.title}
              </li>
            ))}
          </ol>
          <p className="text-xs text-fg-muted">
            {L(
              "La correspondance avec le catalogue sera recherchée après validation. Aucun fichier audio n’est téléchargé.",
              "Catalogue matches will be searched after confirmation. No audio is downloaded.",
            )}
          </p>
          <Button
            size="sm"
            disabled={locked || Boolean(job)}
            onClick={() =>
              void act(async () =>
                setJob(await post<Job>("import", { viewer, token: preview.token })),
              )
            }
          >
            {L("Importer pour", "Import for")} {selected?.name}
          </Button>
        </section>
      )}
      {job && (
        <section aria-live="polite" className="space-y-2 rounded-lg border border-border p-3">
          <p className="text-sm">
            {job.done
              ? job.error
                ? L("Import interrompu", "Import interrupted")
                : L("Import terminé", "Import finished")
              : L("Import en cours", "Importing")}{" "}
            · {job.processed}/{job.total} · {job.matched} {L("retrouvés", "matched")}
          </p>
          <progress className="w-full" value={job.processed} max={job.total} />
          {job.error && (
            <p role="alert" className="text-sm text-danger">
              {job.error}
            </p>
          )}
          {job.missing.length > 0 && (
            <details>
              <summary className="cursor-pointer text-sm">
                {job.missing.length} {L("titres non retrouvés", "unmatched tracks")}
              </summary>
              <ul className="max-h-52 overflow-auto text-xs">
                {job.missing.map((t, i) => (
                  <li key={i}>
                    {t.artist} — {t.title}
                  </li>
                ))}
              </ul>
            </details>
          )}
        </section>
      )}
    </div>
  );
}
