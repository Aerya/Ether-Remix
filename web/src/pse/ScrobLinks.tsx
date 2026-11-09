import { Badge, Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
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
  const current = data ?? doc.data ?? null;
  if (!current) return null;

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
                <Badge variant="success">{L("Lié", "Linked")}</Badge>
              ) : (
                <Badge variant="secondary">{L("Non lié", "Not linked")}</Badge>
              )}
            </div>
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
