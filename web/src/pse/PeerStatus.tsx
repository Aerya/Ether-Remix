import { useState } from "react";
import { Button, Input } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";
import { formatSize } from "./TorrentStatus";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
interface Hit {
  id: string;
  name: string;
  size: number;
  user?: string;
}
interface Transfer {
  hit: Hit;
  state: string;
  received: number;
}
interface Reply<T> {
  ok: boolean;
  message?: string;
  data?: T;
}
export function PeerStatus({ kind }: { kind: "amule" | "slskd" }) {
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<Hit[]>([]);
  const [transfers, setTransfers] = useState<Transfer[]>([]);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  async function action<T>(name: string, body: object = {}): Promise<T | undefined> {
    setBusy(true);
    setMessage("");
    try {
      const r = await apiFetch<Reply<T>>(`/pse/peer/${kind}/${name}`, {
        method: "POST",
        body: JSON.stringify(body),
      });
      if (!r.ok) throw new Error(r.message || L("Échec de la demande", "Request failed"));
      setMessage(
        name === "test"
          ? L("Connexion réussie.", "Connection successful.")
          : name === "download"
            ? L(
                "Demande prise en compte. Actualisez les transferts pour suivre sa progression.",
                "Request accepted. Refresh transfers to follow progress.",
              )
            : "",
      );
      return r.data;
    } catch (e) {
      setMessage(e instanceof Error ? e.message : L("Service indisponible", "Service unavailable"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-4 border-t border-border p-4">
      <p className="text-sm text-fg-muted">
        {L(
          "Enregistrez les réglages avant de tester. Les fichiers seront lisibles après téléchargement complet. Le partage et les transferts se gèrent aussi dans le client externe.",
          "Save settings before testing. Files become playable after download completion. Sharing and transfers can also be managed in the external client.",
        )}
      </p>
      <div className="flex flex-wrap gap-2">
        <Button disabled={busy} onClick={() => void action("test")}>
          {L("Tester la connexion", "Test connection")}
        </Button>
        <Button
          disabled={busy}
          onClick={async () => {
            const data = await action<Transfer[]>("transfers");
            if (data) setTransfers(data);
          }}
        >
          {L("Actualiser les transferts", "Refresh transfers")}
        </Button>
      </div>
      <form
        className="flex gap-2"
        onSubmit={async (e) => {
          e.preventDefault();
          setHits([]);
          const data = await action<Hit[]>("search", { query });
          if (data) setHits(data);
        }}
      >
        <Input
          aria-label={L("Recherche manuelle", "Manual search") + ` ${kind}`}
          value={query}
          maxLength={300}
          onChange={(e) => setQuery(e.target.value)}
        />
        <Button type="submit" disabled={busy || !query.trim()}>
          {L("Rechercher", "Search")}
        </Button>
      </form>
      {busy && <p role="status">{L("Interrogation du service…", "Querying service…")}</p>}
      {message && <p role="status">{message}</p>}
      {hits.length > 0 && (
        <ul className="space-y-2">
          {hits.map((h) => (
            <li key={h.id} className="flex flex-wrap items-center justify-between gap-2 text-sm">
              <span className="break-all">
                {h.name} · {formatSize(h.size)}
                {h.user ? ` · ${h.user}` : ""}
              </span>
              <Button disabled={busy} onClick={() => void action("download", { id: h.id })}>
                {L("Télécharger", "Download")}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {transfers.length > 0 && (
        <ul className="space-y-2">
          {transfers.map((t) => (
            <li key={t.hit.id} className="text-sm break-all">
              {t.hit.name} ·{" "}
              {t.state === "completed"
                ? L("Terminé", "Completed")
                : t.state === "failed"
                  ? L("Échec", "Failed")
                  : L("En attente / en cours", "Queued / in progress")}{" "}
              · {formatSize(t.received)} / {formatSize(t.hit.size)}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
