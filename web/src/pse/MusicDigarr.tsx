import { Button } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";

const BASE = "/pse/music/playlists/digarr";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
interface Recommendation {
  id: number;
  kind: string;
  artist: { name: string };
  recommendedReleaseGroupTitle?: string;
  aiReasoning?: string;
}
interface Album {
  id: number;
  title: string;
  artist: { name: string };
  releaseDate?: string;
}
interface Page {
  configured: boolean;
  items: Recommendation[];
  total: number;
}

export function MusicDigarr({ viewers }: { viewers: { id: number; name: string }[] }) {
  const [viewer, setViewer] = useState(viewers[0]?.id ?? 0);
  return (
    <div className="space-y-4">
      <label className="block text-sm">
        {L("Spectateur", "Viewer")}
        <select
          className="block w-full rounded-md border border-border bg-surface p-2"
          value={viewer}
          onChange={(e) => setViewer(Number(e.target.value))}
        >
          {viewers.map((v) => (
            <option key={v.id} value={v.id}>
              {v.name} (#{v.id})
            </option>
          ))}
        </select>
      </label>
      {viewer > 0 ? (
        <DigarrViewer key={viewer} viewer={viewer} />
      ) : (
        <p>
          {L(
            "Ajoutez un spectateur pour consulter ses découvertes.",
            "Add a viewer to browse discoveries.",
          )}
        </p>
      )}
    </div>
  );
}
function DigarrViewer({ viewer }: { viewer: number }) {
  const [page, setPage] = useState<Page | null>(null);
  const [offset, setOffset] = useState(0);
  const [refresh, setRefresh] = useState(0);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    setPage(null);
    setError("");
    void apiFetch<Page>(`${BASE}?viewer=${viewer}&offset=${offset}`)
      .then((p) => {
        if (active) setPage(p);
      })
      .catch(() => {
        if (active)
          setError(
            L(
              "Impossible de joindre Digarr. Vérifiez la connexion et le jeton de ce spectateur.",
              "Could not reach Digarr. Check this viewer's connection and token.",
            ),
          );
      });
    return () => {
      active = false;
    };
  }, [viewer, offset, refresh]);
  return (
    <div className="space-y-4">
      <p className="text-sm text-fg-muted">
        {L(
          "Recommandations en attente dans Digarr. Retrouvez un album dans le catalogue Deezer, puis créez une playlist personnelle. La disponibilité à la lecture dépend de vos sources musicales.",
          "Pending Digarr recommendations. Find an album in the Deezer catalogue, then create a personal playlist. Playback availability depends on your music sources.",
        )}
      </p>
      <Button variant="outline" size="sm" onClick={() => setRefresh((n) => n + 1)}>
        {L("Actualiser", "Refresh")}
      </Button>
      {error && <p role="alert">{error}</p>}
      {!page && !error && <p role="status">{L("Chargement…", "Loading…")}</p>}
      {page && !page.configured && (
        <p>
          {L(
            "Connecteur facultatif non configuré. Sur le serveur, renseignez PSE_DIGARR_URL et PSE_DIGARR_TOKENS avec un jeton personnel Digarr en lecture seule pour ce spectateur, puis redémarrez Remix. Guide : docs/pse/DIGARR.fr.md.",
            "Optional connector not configured. On the server, set PSE_DIGARR_URL and PSE_DIGARR_TOKENS with a personal read-only Digarr token for this viewer, then restart Remix. Guide: docs/pse/DIGARR.fr.md.",
          )}
        </p>
      )}
      {page?.configured && page.items.length === 0 && (
        <p>
          {L(
            "Aucune recommandation sur cette page. Lancez vos découvertes dans Digarr, puis actualisez ici.",
            "No recommendations on this page. Run discovery in Digarr, then refresh here.",
          )}
        </p>
      )}
      {page?.items.map((rec) => (
        <RecommendationCard key={`${offset}:${refresh}:${rec.id}`} rec={rec} viewer={viewer} />
      ))}
      {page?.configured && (
        <div className="flex gap-2">
          <Button
            variant="outline"
            disabled={offset === 0}
            onClick={() => setOffset((n) => n - 20)}
          >
            {L("Précédent", "Previous")}
          </Button>
          <Button
            variant="outline"
            disabled={offset + 20 >= page.total || offset >= 10000}
            onClick={() => setOffset((n) => n + 20)}
          >
            {L("Suivant", "Next")}
          </Button>
        </div>
      )}
    </div>
  );
}
function RecommendationCard({ rec, viewer }: { rec: Recommendation; viewer: number }) {
  const [albums, setAlbums] = useState<Album[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [imported, setImported] = useState<number[]>([]);
  const [message, setMessage] = useState("");
  const find = async () => {
    setBusy(true);
    setError("");
    try {
      setAlbums(await apiFetch<Album[]>(`${BASE}/albums?viewer=${viewer}&id=${rec.id}`));
    } catch {
      setError(L("Recherche impossible. Réessayez.", "Search failed. Try again."));
    } finally {
      setBusy(false);
    }
  };
  const add = async (album: Album) => {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const result = await apiFetch<{ tracks: number }>(`${BASE}/import`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ viewer, recommendation: rec.id, album: album.id }),
      });
      setImported((ids) => [...ids, album.id]);
      setMessage(
        L(
          `Playlist créée : ${result.tracks} titres. Retrouvez-la dans vos applications musicales.`,
          `Playlist created: ${result.tracks} tracks. Find it in your music apps.`,
        ),
      );
    } catch {
      setError(
        L(
          "Import impossible. Vérifiez vos playlists avant de réessayer si la connexion a été interrompue.",
          "Import failed. Check your playlists before retrying if the connection was interrupted.",
        ),
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="rounded-lg border border-border p-4 space-y-3">
      <h3 className="font-medium">
        {rec.kind === "album"
          ? `${rec.recommendedReleaseGroupTitle} — ${rec.artist.name}`
          : rec.artist.name}
      </h3>
      {rec.aiReasoning && <p className="text-sm text-fg-muted">{rec.aiReasoning}</p>}
      <Button size="sm" variant="outline" disabled={busy} onClick={() => void find()}>
        {L("Retrouver dans Remix", "Find in Remix")}
      </Button>
      {error && <p role="alert">{error}</p>}
      {message && <p role="status">{message}</p>}
      {albums?.length === 0 && (
        <p>
          {L(
            "Aucune correspondance exacte parmi les 50 premiers résultats du catalogue.",
            "No exact match among the first 50 catalogue results.",
          )}
        </p>
      )}
      {albums?.map((album) => (
        <div key={album.id} className="flex flex-wrap items-center gap-3 text-sm">
          <span>
            {album.title}
            {album.releaseDate ? ` (${album.releaseDate})` : ""}
          </span>
          <Button
            size="sm"
            disabled={busy || imported.includes(album.id)}
            onClick={() => void add(album)}
          >
            {imported.includes(album.id)
              ? L("Playlist créée", "Playlist created")
              : L("Créer une playlist de cet album", "Create a playlist from this album")}
          </Button>
        </div>
      ))}
    </section>
  );
}
