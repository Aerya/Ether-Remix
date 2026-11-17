import { Button } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { DiscoveryCardsPicker } from "./DiscoveryCardsPicker";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const endpoint = "/pse/discovery/";
interface Card {
  id: string;
  name: string;
  group: string;
  accent: string;
}
interface State {
  enabled: boolean;
  cards: Card[];
}

export function DiscoveryCardsSetup() {
  const [open, setOpen] = useState(false);
  return (
    <section className="rounded-lg border border-border p-4 space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 className="font-medium">
            {L("Cartes inspirées de Nuvio", "Nuvio-inspired cards")}{" "}
            <span className="ml-2 rounded bg-primary-subtle px-2 py-1 text-xs text-primary">
              {L("Bêta", "Beta")}
            </span>
          </h3>
          <p className="text-sm text-fg-muted">
            {L(
              "Studios & labels, Services et Genres : des cartes qui ouvrent vos collections.",
              "Studios & labels, Services and Genres: cards that open your collections.",
            )}
          </p>
        </div>
        <Button variant="outline" onClick={() => setOpen(!open)} aria-expanded={open}>
          {L("Configuration rapide", "Quick setup")}
        </Button>
      </div>
      {open ? <DiscoveryCardsPicker /> : null}
    </section>
  );
}
export function DiscoveryCardsNotice() {
  const state = useAsync(() => apiFetch<State>(endpoint), []);
  if (!state.data?.enabled) return null;
  return (
    <div
      role="status"
      className="rounded-lg border border-primary/40 bg-primary-subtle p-3 text-sm"
    >
      {L(
        "Le préréglage Cartes inspirées de Nuvio remplace actuellement cet accueil sur Plex et Jellyfin/Emby. Cette page conserve votre disposition précédente.",
        "The Nuvio-inspired cards preset currently replaces this home on Plex and Jellyfin/Emby. This page retains your previous layout.",
      )}{" "}
      <Link to="/remix/visuels" className="underline">
        {L("Gérer les cartes ou rétablir cet accueil", "Manage cards or restore this home")}
      </Link>
    </div>
  );
}
