import { Badge, Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";

/**
 * Liens webhook Scrob par spectateur (internal/pse/scrobpush). Chaque
 * spectateur colle l'URL de webhook Jellyfin que Scrob affiche dans
 * Connexions ; la clé API reste masquée une fois enregistrée.
 */

interface ScrobViewer {
  id: number;
  name: string;
  url: string;
  configured: boolean;
  lastStatus?: string;
  lastAt?: number;
  jellyfinLastAt?: number;
}

export interface ScrobData {
  enabled: boolean;
  viewers: ScrobViewer[];
}

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

function when(ts?: number) {
  if (!ts) return "";
  return new Date(ts * 1000).toLocaleString(getLocale(), {
    dateStyle: "short",
    timeStyle: "short",
  });
}

export function ScrobLinks({
  bare = false,
  onChange,
}: {
  /** Sans cadre, pour le panneau latéral de Services. */
  bare?: boolean;
  /** Appelé après chaque enregistrement, avec le nouvel état. */
  onChange?: (data: ScrobData) => void;
} = {}) {
  const doc = useAsync(() => apiFetch<ScrobData>("/pse/scrob/"), []);
  const [data, setData] = useState<ScrobData | null>(null);
  const [drafts, setDrafts] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState<number | null>(null);
  const [connections, setConnections] = useState<
    Record<number, { url: string; password: string; userId: string }>
  >({});
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        if (!document.hidden) {
          const next = await apiFetch<ScrobData>("/pse/scrob/");
          if (!stopped) setData(next);
        }
      } catch {
        /* Keep the last known status; never clear unsaved inputs. */
      } finally {
        if (!stopped) timer = setTimeout(poll, 5000);
      }
    };
    timer = setTimeout(poll, 5000);
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, []);
  const current = data ?? doc.data ?? null;
  if (!current) return null;

  const connect = async (id: number) => {
    setBusy(id);
    try {
      const c = await apiFetch<{ url: string; password: string; userId: string }>(
        `/viewers/${id}/apps/jellyfin`,
        { method: "POST" },
      );
      setConnections((prev) => ({ ...prev, [id]: c }));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const save = async (v: ScrobViewer, value: string) => {
    setBusy(v.id);
    try {
      const next = await apiFetch<ScrobData>("/pse/scrob/", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ links: { [v.id]: value } }),
      });
      setData(next);
      onChange?.(next);
      setDrafts((d) => {
        const { [v.id]: _, ...rest } = d;
        return rest;
      });
      toast.success(
        value
          ? L("Lien Scrob enregistré", "Scrob link saved")
          : L("Lien Scrob retiré", "Scrob link removed"),
      );
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  const test = async (v: ScrobViewer, value: string) => {
    setBusy(v.id);
    try {
      const r = await apiFetch<{ ok: boolean; message: string }>("/pse/scrob/test", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ viewer: v.id, url: value }),
      });
      if (r.ok) toast.success(r.message);
      else toast.error(r.message);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  };

  return (
    <div className={bare ? "space-y-3" : "space-y-3 border-t border-border px-4 py-4"}>
      <div className="text-sm font-medium">{L("Liens webhook Scrob", "Scrob webhook links")}</div>
      <p className="text-sm text-fg-muted">
        {L(
          "Dans Scrob : Connexions → votre connexion Jellyfin (celle qui pointe vers Ether-Remix) → copiez l'URL du webhook, puis collez-la ici pour le spectateur correspondant.",
          "In Scrob: Connections → your Jellyfin connection (the one pointing at Ether-Remix) → copy the webhook URL, then paste it here for the matching viewer.",
        )}
      </p>
      <p className="text-sm text-fg-muted">
        {L(
          "Pour les deux sens, ajoutez une connexion Jellyfin dans Scrob → Media Players, avec les champs générés ci-dessous. Activez les options Scrob → Server : Watched status et Ratings, puis choisissez la synchronisation automatique. Une connexion Scrobble-only ne permet que Remix → Scrob. La position de reprise n’est pas renvoyée par Scrob.",
          "For both directions, add a Jellyfin connection under Scrob → Media Players using the fields generated below. Enable Scrob → Server: Watched status and Ratings, then choose automatic sync. A Scrobble-only connection only supports Remix → Scrob. Scrob does not push resume positions.",
        )}
      </p>
      {current.viewers.length === 0 ? (
        <p className="text-sm text-fg-muted">
          {L(
            "Aucun spectateur pour l'instant : ajoutez-en dans Spectateurs, puis revenez ici.",
            "No viewers yet: add some in Viewers, then come back here.",
          )}
        </p>
      ) : null}
      {current.viewers.map((v) => {
        const value = drafts[v.id] ?? v.url;
        const changed = value !== v.url;
        return (
          <div key={v.id} className="space-y-2 rounded-lg border border-border p-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="truncate text-sm font-medium">{v.name}</span>
              {v.lastStatus ? (
                <Badge variant={v.lastStatus === "ok" ? "success" : "warning"}>
                  {v.lastStatus === "ok"
                    ? L(`Dernier envoi réussi ${when(v.lastAt)}`, `Last sent ${when(v.lastAt)}`)
                    : v.lastStatus}
                </Badge>
              ) : v.configured ? (
                <Badge variant="success">{L("Webhook configuré", "Webhook configured")}</Badge>
              ) : (
                <Badge variant="secondary">
                  {L("Webhook non configuré", "Webhook not configured")}
                </Badge>
              )}
            </div>
            <p className="text-xs text-fg-muted" role="status">
              {v.jellyfinLastAt
                ? L(
                    `Dernier accès Jellyfin : ${when(v.jellyfinLastAt)}`,
                    `Last Jellyfin access: ${when(v.jellyfinLastAt)}`,
                  )
                : L(
                    "Aucun accès Jellyfin observé avec les identifiants actifs.",
                    "No Jellyfin access observed with active credentials.",
                  )}{" "}
              {L(
                "Inclut Scrob et les autres clients Jellyfin de ce spectateur ; ne confirme pas une synchronisation terminée.",
                "Includes Scrob and this viewer’s other Jellyfin clients; does not confirm a completed sync.",
              )}
            </p>
            <Button
              size="sm"
              variant="outline"
              disabled={busy === v.id || !!connections[v.id]}
              onClick={() => void connect(v.id)}
            >
              {L(
                "Créer les identifiants Jellyfin pour Scrob",
                "Create Jellyfin credentials for Scrob",
              )}
            </Button>
            {connections[v.id] ? (
              <div className="space-y-2 rounded border border-border p-3">
                {(
                  [
                    ["Server URL", connections[v.id]!.url],
                    ["Token", connections[v.id]!.password],
                    ["User ID", connections[v.id]!.userId],
                  ] as const
                ).map(([label, field]) => (
                  <label key={label} className="block text-xs">
                    {label}
                    <Input
                      readOnly
                      type={label === "Token" ? "password" : "text"}
                      value={field}
                      onFocus={(e) => e.currentTarget.select()}
                    />
                  </label>
                ))}
                <p className="text-xs text-fg-muted">
                  {L(
                    "Copiez ces champs dans Scrob. Le jeton est propre à ce spectateur et révocable dans ses appareils. Il ne sera plus affiché après fermeture de cette page.",
                    "Copy these fields into Scrob. The token belongs to this viewer and can be revoked in their devices. It will no longer be displayed after leaving this page.",
                  )}
                </p>
              </div>
            ) : null}
            <Input
              className="h-9 w-full"
              aria-label={L(`Lien webhook Scrob de ${v.name}`, `${v.name} Scrob webhook link`)}
              value={value}
              placeholder="https://scrob…/api/proxy/webhooks/jellyfin/1?api_key=…"
              onChange={(e) => setDrafts({ ...drafts, [v.id]: e.target.value })}
            />
            <div className="flex flex-wrap gap-2">
              <Button
                size="sm"
                disabled={busy === v.id || !changed}
                onClick={() => void save(v, value)}
              >
                {L("Enregistrer", "Save")}
              </Button>
              <Button
                size="sm"
                variant="outline"
                disabled={busy === v.id || !value}
                onClick={() => void test(v, value)}
              >
                {L("Tester", "Test")}
              </Button>
              {v.configured && !changed ? (
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={busy === v.id}
                  onClick={() => void save(v, "")}
                >
                  {L("Retirer", "Remove")}
                </Button>
              ) : null}
            </div>
          </div>
        );
      })}
    </div>
  );
}
