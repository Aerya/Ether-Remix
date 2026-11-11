import { useEffect, useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const field = "rounded border border-border bg-surface p-2 w-full";
type Root = { id: string; name: string; path: string; kind: string };
type LocalData = {
  roots: Root[];
  status: {
    running: boolean;
    scanned: number;
    imported: number;
    errors: number;
    lastError?: string;
    finished: number;
  };
};
type IBData = {
  connected: boolean;
  clientId?: string;
  tracks: number;
  syncing: boolean;
  error?: string;
  lastSync: number;
  userCode?: string;
  url?: string;
};
const base = "/pse/owned/";
export function LocalMedia() {
  const [data, setData] = useState<LocalData>();
  const [draft, setDraft] = useState({ name: "", path: "", kind: "video" });
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  async function refresh() {
    setData(await apiFetch<LocalData>(base + "local"));
  }
  useEffect(() => {
    let active = true;
    const load = () =>
      apiFetch<LocalData>(base + "local")
        .then((d) => {
          if (active) {
            setData(d);
            setError("");
          }
        })
        .catch((e) => {
          if (active) setError(String(e));
        });
    void load();
    const t = setInterval(() => void load(), 5000);
    return () => {
      active = false;
      clearInterval(t);
    };
  }, []);
  async function act(path: string, method: string, body?: unknown) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await apiFetch(base + path, { method, ...(body ? { body: JSON.stringify(body) } : {}) });
      await refresh();
      setNotice(
        method === "DELETE"
          ? L("Source retirée ; fichiers conservés.", "Source removed; files preserved.")
          : L("Analyse mise en file.", "Scan queued."),
      );
      if (body) setDraft({ name: "", path: "", kind: "video" });
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="rounded-xl border border-border bg-surface p-5 space-y-4">
      <h3 className="font-semibold">{L("Fichiers locaux", "Local files")}</h3>
      <p className="text-sm text-fg-muted">
        {L(
          "Ajoutez les dossiers visibles depuis le conteneur. Les fichiers sont lus sur place, sans déplacement ni suppression. Chaque dossier vidéo crée une bibliothèque dédiée ; les dossiers audio rejoignent Musique.",
          "Add folders visible inside the container. Files are played in place, never moved or deleted. Each video folder creates a dedicated library; audio folders join Music.",
        )}
      </p>
      <p className="text-sm text-fg-muted">
        {L(
          "Films, épisodes, animés, concerts et vidéos personnelles : aucune fiche TMDB n’est requise. Les vidéos sont présentées individuellement, avec leur titre local. Les sources ajoutées sont partagées avec les spectateurs de Remix.",
          "Movies, episodes, anime, concerts and home videos need no TMDB entry. Videos appear individually with their local title. Added sources are shared with Remix viewers.",
        )}
      </p>
      <details className="text-sm">
        <summary className="cursor-pointer">
          {L("Exemple Docker Compose", "Docker Compose example")}
        </summary>
        <pre className="mt-2 overflow-auto rounded bg-surface-raised p-3">
          {
            "volumes:\n  - /chemin/sur/hote/videos:/media/videos:ro\n  - /chemin/sur/hote/musique:/media/music:ro"
          }
        </pre>
        <p className="mt-2">
          {L(
            "Saisissez ensuite /media/videos ou /media/music. Les montages réseau doivent déjà être accessibles sur l’hôte et lisibles par l’utilisateur du conteneur.",
            "Then enter /media/videos or /media/music. Network mounts must already be available on the host and readable by the container user.",
          )}
        </p>
      </details>
      <form
        className="grid gap-3 sm:grid-cols-2"
        onSubmit={(e) => {
          e.preventDefault();
          void act("local", "POST", draft);
        }}
      >
        <label className="text-sm">
          {L("Nom de la source", "Source name")}
          <input
            className={field}
            required
            maxLength={60}
            value={draft.name}
            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          />
        </label>
        <label className="text-sm">
          {L("Dossier dans le conteneur", "Folder inside the container")}
          <input
            className={field}
            required
            placeholder="/media/videos"
            value={draft.path}
            onChange={(e) => setDraft({ ...draft, path: e.target.value })}
          />
        </label>
        <label className="text-sm">
          {L("Contenu", "Content")}
          <select
            className={field}
            value={draft.kind}
            onChange={(e) => setDraft({ ...draft, kind: e.target.value })}
          >
            <option value="video">{L("Vidéos", "Videos")}</option>
            <option value="audio">{L("Musique et audio", "Music and audio")}</option>
          </select>
        </label>
        <div className="flex items-end">
          <Button type="submit" disabled={busy}>
            {L("Ajouter et analyser", "Add and scan")}
          </Button>
        </div>
      </form>
      {data?.roots.map((r) => (
        <div
          className="flex flex-wrap items-center justify-between gap-3 rounded border border-border p-3"
          key={r.id}
        >
          <div>
            <strong>{r.name}</strong>
            <span className="ml-2 text-xs text-fg-muted">
              {r.kind === "audio" ? L("Musique", "Music") : L("Vidéos", "Videos")}
            </span>
            <p className="break-all text-sm text-fg-muted">{r.path}</p>
          </div>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => void act("local?id=" + encodeURIComponent(r.id), "DELETE")}
          >
            {L("Retirer la source", "Remove source")}
          </Button>
        </div>
      ))}
      <div className="flex flex-wrap items-center gap-3">
        <Button
          variant="outline"
          disabled={busy || data?.status.running}
          onClick={() => void act("local/scan", "POST")}
        >
          {L("Analyser les dossiers", "Scan folders")}
        </Button>
        <span className="text-sm text-fg-muted" role="status">
          {data?.status.running
            ? L("Analyse en cours", "Scanning")
            : L(
                "Analyse automatique au démarrage et toutes les heures",
                "Automatic scan at startup and hourly",
              )}
          {data
            ? ` · ${data.status.scanned} ${L("examinés", "examined")} · ${data.status.imported} ${L("importés", "imported")} · ${data.status.errors} ${L("erreurs", "errors")}`
            : ""}
        </span>
      </div>
      {notice && (
        <p role="status" className="text-sm">
          {notice}
        </p>
      )}
      {(error || data?.status.lastError) && (
        <p role="alert" className="text-sm text-red-500">
          {error || data?.status.lastError}
        </p>
      )}
    </section>
  );
}
export function IBroadcastSource() {
  const [data, setData] = useState<IBData>();
  const [client, setClient] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let active = true;
    let running = false;
    async function load() {
      if (running) return;
      running = true;
      try {
        let d = await apiFetch<IBData>(base + "ibroadcast");
        if (d.userCode) d = await apiFetch<IBData>(base + "ibroadcast/poll", { method: "POST" });
        if (active) {
          setData(d);
          setError("");
        }
      } catch (e) {
        if (active) setError(String(e));
      } finally {
        running = false;
      }
    }
    void load();
    const t = setInterval(() => void load(), 5000);
    return () => {
      active = false;
      clearInterval(t);
    };
  }, []);
  async function act(path: string, method: string, body?: unknown) {
    setBusy(true);
    setError("");
    try {
      await apiFetch(base + path, { method, ...(body ? { body: JSON.stringify(body) } : {}) });
      setData(await apiFetch<IBData>(base + "ibroadcast"));
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="rounded-xl border border-border bg-surface p-5 space-y-4">
      <h3 className="font-semibold">
        iBroadcast · {L("Votre bibliothèque musicale", "Your music library")}
      </h3>
      <p className="text-sm text-fg-muted">
        {L(
          "Retrouvez et écoutez votre musique iBroadcast dans Remix. Cette bibliothèque est partagée avec les spectateurs du serveur. La connexion se fait sur iBroadcast ; Remix ne conserve pas votre mot de passe.",
          "Browse and play your iBroadcast music in Remix. This library is shared with the server’s viewers. Sign in on iBroadcast; Remix never stores your password.",
        )}
      </p>
      <p className="text-sm text-fg-muted">
        {L(
          "Dans iBroadcast : menu → Apps → developer, créez une application, sélectionnez Grant Type → Device Code et enregistrez, puis copiez son Client ID ci-dessous. Aucun Client Secret ni URL de retour n’est nécessaire pour la connexion par code.",
          "In iBroadcast: menu → Apps → developer, create an application, select Grant Type → Device Code and save, then copy its Client ID below. Device-code login needs no Client Secret or redirect URL.",
        )}{" "}
        <a
          className="underline"
          href="https://media.ibroadcast.com/"
          target="_blank"
          rel="noreferrer"
        >
          {L("Ouvrir iBroadcast", "Open iBroadcast")} ↗
        </a>
      </p>
      <form
        className="flex flex-wrap items-end gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          void act("ibroadcast/connect", "POST", { clientId: client ?? data?.clientId });
        }}
      >
        <label className="flex-1 text-sm">
          Client ID
          <input
            className={field}
            required
            value={client ?? data?.clientId ?? ""}
            onChange={(e) => setClient(e.target.value)}
            autoComplete="off"
          />
        </label>
        <Button type="submit" disabled={busy}>
          {data?.connected
            ? L("Reconnecter", "Reconnect")
            : L("Connecter iBroadcast", "Connect iBroadcast")}
        </Button>
      </form>
      {data?.userCode && (
        <div className="rounded border border-border p-4 space-y-2" role="status">
          <p>
            {L(
              "Autorisez la connexion sur iBroadcast avec ce code :",
              "Authorize the connection on iBroadcast with this code:",
            )}{" "}
            <strong className="font-mono text-lg">{data.userCode}</strong>
          </p>
          <a className="underline" href={data.url} target="_blank" rel="noreferrer">
            {L("Autoriser Ether-Remix", "Authorize Ether-Remix")} ↗
          </a>
          <p className="text-sm text-fg-muted">
            {L(
              "Ce panneau se met à jour automatiquement après autorisation.",
              "This panel updates automatically after authorization.",
            )}
          </p>
        </div>
      )}
      <p className="text-sm" role="status">
        {data?.connected
          ? `${L("Connecté", "Connected")} · ${data.tracks} ${L("titres", "tracks")}`
          : L("Non connecté", "Not connected")}
        {data?.syncing ? ` · ${L("Synchronisation…", "Syncing…")}` : ""}
      </p>
      {data?.connected && (
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            disabled={busy || data.syncing}
            onClick={() => void act("ibroadcast/sync", "POST")}
          >
            {L("Actualiser la bibliothèque", "Refresh library")}
          </Button>
          <Button
            variant="outline"
            disabled={busy}
            onClick={() => void act("ibroadcast", "DELETE")}
          >
            {L("Déconnecter", "Disconnect")}
          </Button>
        </div>
      )}
      <p className="text-xs text-fg-muted">
        {L(
          "Actualisation automatique chaque heure. Lecture MP3 128 kb/s, compatible avec les comptes gratuits. Les favoris et écoutes restent gérés dans Remix.",
          "Automatic hourly refresh. MP3 128 kb/s playback, compatible with free accounts. Favourites and listening history remain managed in Remix.",
        )}
      </p>
      {(error || data?.error) && (
        <p role="alert" className="text-sm text-red-500">
          {error || data?.error}
        </p>
      )}
    </section>
  );
}
