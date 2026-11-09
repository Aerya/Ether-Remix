import { Button, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";

/**
 * Envoi des écoutes à Last.fm (internal/pse/music/lastfm_scrobble.go) :
 * chaque spectateur autorise PSE une fois sur last.fm.
 */

export interface LastFMLinkState {
  username: string;
  linkedAt: number;
  sentAt?: number;
  sent: number;
  error?: string;
}

export interface LastFMData {
  ready: boolean;
  links: Record<string, LastFMLinkState>;
}

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const ENDPOINT = "/pse/music/lastfm/";

export function useLastFM() {
  const doc = useAsync(() => apiFetch<LastFMData>(ENDPOINT), []);
  const [data, setData] = useState<LastFMData | null>(null);
  return { data: data ?? doc.data ?? null, setData };
}

const post = <T,>(path: string, body: object) =>
  apiFetch<T>(ENDPOINT + path, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

export function LastFMLink({
  viewer,
  data,
  setData,
}: {
  viewer: number;
  data: LastFMData;
  setData: (d: LastFMData) => void;
}) {
  const [token, setToken] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const link = data.links?.[String(viewer)];

  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    try {
      await fn();
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  if (!data.ready)
    return (
      <p className="text-xs text-fg-muted">
        {L(
          "Envoi des écoutes à Last.fm : renseignez d'abord la clé API et le secret partagé Last.fm ci-dessus.",
          "Sending plays to Last.fm: set the key and shared secret above first.",
        )}
      </p>
    );

  if (link)
    return (
      <div className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
        <span>
          {L(
            `Écoutes envoyées à Last.fm (${link.username}) : ${link.sent}`,
            `Plays sent to Last.fm (${link.username}): ${link.sent}`,
          )}
        </span>
        {link.error ? <span className="text-warning">{link.error}</span> : null}
        <Button
          size="sm"
          variant="ghost"
          disabled={busy}
          onClick={() =>
            void run(async () => {
              setData(await post<LastFMData>("unlink", { viewer }));
              toast.success(L("Compte Last.fm délié", "Last.fm account unlinked"));
            })
          }
        >
          {L("Délier", "Unlink")}
        </Button>
      </div>
    );

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
      <span>{L("Envoyer les écoutes à Last.fm :", "Send plays to Last.fm:")}</span>
      <Button
        size="sm"
        variant="outline"
        disabled={busy}
        onClick={() => {
          // Fenêtre ouverte tout de suite, sans quoi le navigateur la bloque.
          const win = window.open("", "_blank");
          void run(async () => {
            try {
              const res = await post<{ token: string; url: string }>("connect", { viewer });
              setToken(res.token);
              if (win) win.location.href = res.url;
              else window.open(res.url, "_blank");
            } catch (e) {
              win?.close();
              throw e;
            }
          });
        }}
      >
        {L("Connecter Last.fm", "Connect Last.fm")}
      </Button>
      {token ? (
        <Button
          size="sm"
          disabled={busy}
          onClick={() =>
            void run(async () => {
              setData(await post<LastFMData>("confirm", { viewer, token }));
              setToken(null);
              toast.success(L("Compte Last.fm relié", "Last.fm account linked"));
            })
          }
        >
          {L("J'ai autorisé Ether-Remix", "I authorized Ether-Remix")}
        </Button>
      ) : null}
    </div>
  );
}
