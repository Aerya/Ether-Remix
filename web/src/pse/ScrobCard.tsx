import { Badge, Button, Switch, toast } from "@braedonsaunders/appkit-ui";
import { ArrowRight } from "lucide-react";
import { useState } from "react";
import { Drawer } from "../components/Drawer";
import { IntegrationAppCard } from "../components/IntegrationAppCard";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { ScrobLinks, type ScrobData } from "./ScrobLinks";

/**
 * Carte Scrob de PSE dans Services › Suivi du visionnage, au même format
 * que Trakt et Simkl (internal/pse/scrobpush). « Configurer » ouvre un
 * panneau latéral avec l'activation et les liens webhook par spectateur.
 * Voir docs/pse/FORK.fr.md.
 */

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export function PseScrobCard(_: {
  /** Lu par la grille de Services pour placer la carte avec les autres. */
  identity: { id: string; name: string };
}) {
  const doc = useAsync(() => apiFetch<ScrobData>("/pse/scrob/"), []);
  const [busy, setBusy] = useState(false);
  const [open, setOpen] = useState(false);
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const on = enabled ?? doc.data?.enabled ?? false;
  const linked = (doc.data?.viewers ?? []).filter((v) => v.configured).length;

  const toggle = async (next: boolean) => {
    setBusy(true);
    try {
      await apiFetch("/pse/", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ values: { PSE_SCROB_PUSH: next ? "on" : "off" } }),
      });
      setEnabled(next);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };

  let status;
  if (!on) status = <Badge variant="secondary">{L("Désactivé", "Off")}</Badge>;
  else if (linked === 0)
    status = <Badge variant="warning">{L("Aucun spectateur lié", "No viewer linked")}</Badge>;
  else
    status = (
      <Badge variant="success">
        {L(
          `Actif · ${linked} spectateur${linked > 1 ? "s" : ""}`,
          `On · ${linked} viewer${linked > 1 ? "s" : ""}`,
        )}
      </Badge>
    );

  return (
    <>
      <IntegrationAppCard
        identity={{ id: "pse-scrob", name: "Scrob" }}
        description={L(
          "Envoie chaque lecture à Scrob en temps réel et lui ouvre l'historique « Vus et notés ».",
          "Sends every playback to Scrob in real time and opens the “Vus et notés” history to it.",
        )}
        status={status}
        enabled={on}
        busy={busy || doc.loading}
        onToggle={(next) => {
          void toggle(next);
          if (next && linked === 0) setOpen(true);
        }}
        action={
          <Button size="sm" variant="outline" onClick={() => setOpen(true)}>
            {on && linked > 0
              ? L("Gérer Scrob", "Manage Scrob")
              : L("Configurer Scrob", "Set up Scrob")}
            <ArrowRight aria-hidden className="size-3.5" />
          </Button>
        }
      />
      <Drawer
        open={open}
        onClose={() => setOpen(false)}
        title="Scrob"
        description={L(
          "Chaque spectateur colle le lien webhook que Scrob lui donne.",
          "Each viewer pastes the webhook link Scrob gives them.",
        )}
      >
        <div className="space-y-4">
          <label className="flex items-center justify-between gap-4 rounded-lg border border-border p-3 text-sm">
            {L("Envoyer les lectures à Scrob", "Send playback to Scrob")}
            <Switch
              aria-label={L("Scrob activé", "Scrob enabled")}
              checked={on}
              disabled={busy}
              onChange={(e) => void toggle(e.currentTarget.checked)}
            />
          </label>
          <ScrobLinks bare onChange={(next) => doc.setData(next)} />
        </div>
      </Drawer>
    </>
  );
}
