import { Badge, Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { RefreshCw } from "lucide-react";
import { useEffect, useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { LastFMLink, useLastFM } from "./LastFMLink";

/**
 * Comptes Deezer et Last.fm de chaque spectateur (internal/pse/music,
 * playlists_api.go). Leurs playlists apparaissent dans Plex, Jellyfin et
 * les clients Subsonic ; rien n'est téléchargé.
 */

type Service = "deezer" | "lastfm" | "listenbrainz";
const SERVICES: Service[] = ["deezer", "lastfm", "listenbrainz"];

interface Account {
  service: Service;
  account: string;
  syncedAt?: number;
  error?: string;
}

interface PlaylistViewer {
  id: number;
  name: string;
  accounts: Partial<Record<Service, Account>>;
  playlists: { id: number; name: string; source: Service; tracks: number }[];
  syncing: boolean;
}

interface PlaylistData {
  lastfmKey: boolean;
  hours: number;
  viewers: PlaylistViewer[];
}

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

function when(ts?: number) {
  if (!ts) return "";
  return new Date(ts * 1000).toLocaleString(getLocale(), {
    dateStyle: "short",
    timeStyle: "short",
  });
}

/** « user:1 playlist:2 » → liens lisibles. */
export function deezerLinks(account: string) {
  return account
    .split(/\s+/)
    .filter(Boolean)
    .map((tok) => {
      const [kind, id] = tok.split(":");
      if (kind === "user") return `https://www.deezer.com/profile/${id}`;
      if (kind === "playlist") return `https://www.deezer.com/playlist/${id}`;
      return tok;
    })
    .join(" ");
}

const ENDPOINT = "/pse/music/playlists/";

export function MusicPlaylists() {
  const doc = useAsync(() => apiFetch<PlaylistData>(ENDPOINT), []);
  const [data, setData] = useState<PlaylistData | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<number | null>(null);
  const current = data ?? doc.data ?? null;
  const lastfm = useLastFM();
  const syncing = current?.viewers.some((v) => v.syncing) ?? false;

  // Suit l'import en cours.
  useEffect(() => {
    if (!syncing) return;
    const t = window.setInterval(() => {
      apiFetch<PlaylistData>(ENDPOINT)
        .then(setData)
        .catch(() => undefined);
    }, 3000);
    return () => window.clearInterval(t);
  }, [syncing]);

  if (!current) return null;

  const stored = (v: PlaylistViewer, s: Service) => {
    const a = v.accounts[s]?.account ?? "";
    return s === "deezer" ? deezerLinks(a) : a;
  };
  const key = (v: PlaylistViewer, s: Service) => `${v.id}:${s}`;
  const value = (v: PlaylistViewer, s: Service) => drafts[key(v, s)] ?? stored(v, s);

  const send = async (path: string, method: string, body: object, ok: string) => {
    try {
      const next = await apiFetch<PlaylistData>(ENDPOINT + path, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      setData(next);
      toast.success(ok);
      return true;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return false;
    }
  };

  const save = async (v: PlaylistViewer) => {
    setBusy(v.id);
    const body: Record<string, unknown> = { viewer: v.id };
    for (const s of SERVICES) {
      if (drafts[key(v, s)] !== undefined) body[s] = drafts[key(v, s)];
    }
    if (
      await send(
        "",
        "PUT",
        body,
        L(
          "Comptes enregistrés, import des playlists en cours",
          "Accounts saved, importing playlists",
        ),
      )
    ) {
      setDrafts((d) => {
        const next = { ...d };
        for (const s of SERVICES) delete next[key(v, s)];
        return next;
      });
    }
    setBusy(null);
  };

  const sync = async (v: PlaylistViewer) => {
    setBusy(v.id);
    await send(
      "sync",
      "POST",
      { viewer: v.id },
      L("Import des playlists lancé", "Playlist import started"),
    );
    setBusy(null);
  };

  return (
    <div className="space-y-3 border-t border-border px-4 py-4">
      <div className="text-sm font-medium">
        {L(
          "Playlists Deezer, Last.fm et ListenBrainz",
          "Deezer, Last.fm and ListenBrainz playlists",
        )}
      </div>
      <p className="text-sm text-fg-muted">
        {L(
          `Les playlists publiques d'un profil Deezer (dont Coups de cœur), ou des liens de playlists Deezer, les titres aimés et tops Last.fm, et les playlists ListenBrainz (Daily Jams, Weekly Exploration…) apparaissent dans Plex, Jellyfin et les clients Subsonic du spectateur. Mise à jour toutes les ${current.hours} h ; rien n'est téléchargé.`,
          `Public playlists of a Deezer profile (including Favourite tracks), or Deezer playlist links, Last.fm loved and top tracks, and ListenBrainz playlists (Daily Jams, Weekly Exploration…) show up in the viewer's Plex, Jellyfin and Subsonic clients. Refreshed every ${current.hours} h; nothing is downloaded.`,
        )}
      </p>
      {!current.lastfmKey ? (
        <p className="text-sm text-fg-muted">
          {L(
            "Last.fm : renseignez d'abord la clé API Last.fm ci-dessus.",
            "Last.fm: set the Last.fm API key above first.",
          )}
        </p>
      ) : null}
      {current.viewers.length === 0 ? (
        <p className="text-sm text-fg-muted">
          {L(
            "Aucun spectateur pour l'instant : ajoutez-en dans Spectateurs, puis revenez ici.",
            "No viewers yet: add some in Viewers, then come back here.",
          )}
        </p>
      ) : null}
      {current.viewers.map((v) => {
        const changed = SERVICES.some(
          (s) => drafts[key(v, s)] !== undefined && drafts[key(v, s)] !== stored(v, s),
        );
        const linked = SERVICES.some((s) => Boolean(v.accounts[s]));
        const errors = SERVICES.map((s) => v.accounts[s]?.error).filter(Boolean);
        const last = Math.max(...SERVICES.map((s) => v.accounts[s]?.syncedAt ?? 0));
        return (
          <div key={v.id} className="space-y-2 rounded-lg border border-border p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="truncate text-sm font-medium">{v.name}</span>
              {v.syncing ? (
                <Badge variant="info">{L("Import en cours…", "Importing…")}</Badge>
              ) : linked ? (
                <Badge variant={errors.length ? "warning" : "success"}>
                  {L(`${v.playlists.length} playlist(s)`, `${v.playlists.length} playlist(s)`)}
                  {last ? ` · ${when(last)}` : ""}
                </Badge>
              ) : (
                <Badge variant="secondary">{L("Non lié", "Not linked")}</Badge>
              )}
            </div>
            <label className="block space-y-1 text-xs text-fg-muted">
              <span>
                {L("Deezer : lien du profil ou de playlists", "Deezer: profile or playlist links")}
              </span>
              <Input
                className="h-9 w-full"
                aria-label={L(`Deezer de ${v.name}`, `${v.name} Deezer`)}
                value={value(v, "deezer")}
                placeholder="https://www.deezer.com/fr/profile/123456"
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => setDrafts({ ...drafts, [key(v, "deezer")]: e.target.value })}
              />
            </label>
            <label className="block space-y-1 text-xs text-fg-muted">
              <span>{L("Last.fm : nom d'utilisateur", "Last.fm: username")}</span>
              <Input
                className="h-9 w-full"
                aria-label={L(`Last.fm de ${v.name}`, `${v.name} Last.fm`)}
                value={value(v, "lastfm")}
                placeholder="https://www.last.fm/user/…"
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => setDrafts({ ...drafts, [key(v, "lastfm")]: e.target.value })}
              />
            </label>
            <label className="block space-y-1 text-xs text-fg-muted">
              <span>
                {L(
                  "ListenBrainz : nom d'utilisateur (gratuit, sans clé)",
                  "ListenBrainz: username (free, no key)",
                )}
              </span>
              <Input
                className="h-9 w-full"
                aria-label={L(`ListenBrainz de ${v.name}`, `${v.name} ListenBrainz`)}
                value={value(v, "listenbrainz")}
                placeholder="https://listenbrainz.org/user/…"
                spellCheck={false}
                autoComplete="off"
                onChange={(e) => setDrafts({ ...drafts, [key(v, "listenbrainz")]: e.target.value })}
              />
            </label>
            {errors.length ? <p className="text-xs text-warning">{errors.join(" · ")}</p> : null}
            {lastfm.data ? (
              <LastFMLink viewer={v.id} data={lastfm.data} setData={lastfm.setData} />
            ) : null}
            {v.playlists.length ? (
              <ul className="flex flex-wrap gap-1.5">
                {v.playlists.map((p) => (
                  <li key={p.id}>
                    <Badge variant="outline">
                      {p.name} ({p.tracks})
                    </Badge>
                  </li>
                ))}
              </ul>
            ) : null}
            <div className="flex flex-wrap gap-2">
              <Button size="sm" disabled={busy === v.id || !changed} onClick={() => void save(v)}>
                {L("Enregistrer", "Save")}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={busy === v.id || !linked || v.syncing || changed}
                onClick={() => void sync(v)}
              >
                <RefreshCw size={14} aria-hidden /> {L("Mettre à jour", "Refresh")}
              </Button>
            </div>
          </div>
        );
      })}
    </div>
  );
}
