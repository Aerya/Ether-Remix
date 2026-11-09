import { Button } from "@braedonsaunders/appkit-ui";
import { Sparkles, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const SEEN_KEY = "ether-remix.updates.seen";
// Ajouter les nouveautés ici à chaque livraison, de la plus récente à la plus ancienne.
export const REMIX_UPDATES = [
  {
    id: "2026-11-09-sources-sync",
    date: "2026-11-09",
    title: ["Sources réunies et navigation simplifiée", "Unified sources and simpler navigation"],
    items: [
      [
        "Un seul accès aux sources dans Remix : les comptes et addons Ether sont réutilisés, avec les sources musicales et l’indexation.",
        "One source entry in Remix reuses Ether accounts and addons, alongside music sources and background indexing.",
      ],
      [
        "Six onglets : Sources, Langues et qualité, VOSTFR à la demande, Contenus et bibliothèques, Statistiques, Notifications et suivi.",
        "Six tabs: Sources, Languages & quality, On-demand subtitles, Content & libraries, Statistics, Notifications & tracking.",
      ],
      [
        "Sélection des sources d’indexation regroupée par catégorie, avec tri et indication des contenus concernés.",
        "Background indexing sources can be filtered and sorted by category, with their relevant content shown.",
      ],
      [
        "Les anciens liens restent utilisables. Les nouveautés d’Ether sont intégrées ; la synchronisation conserve mieux les traductions du fork.",
        "Existing links keep working. Ether changes are integrated; synchronization better preserves fork translations.",
      ],
    ],
  },
  {
    id: "2026-11-09-animes",
    date: "2026-11-09",
    title: ["Les animés rejoignent Ether-Remix", "Anime joins Ether-Remix"],
    items: [
      [
        "Les sources distinguent les instances publiques et proposent les liens d’auto-hébergement disponibles.",
        "Sources identify public instances and link to available self-hosting projects.",
      ],
      [
        "Onglet À la demande : les exceptions VOSTFR sont séparées du catalogue FR/MULTi. Chaque ajout lance une recherche prioritaire si l’indexation est active.",
        "On demand tab: subtitled exceptions are separate from the French/MULTi catalogue. Each addition queues a priority search when indexing is enabled.",
      ],
      [
        "Onglet Animes : bibliothèques et collections TMDB, avec vos critères de langue FR/MULTi.",
        "Anime tab: TMDB libraries and collections, respecting your language criteria.",
      ],
      [
        "UwU+ et ajout libre d’autres addons Stremio, avec priorité et sources partagées.",
        "UwU+ and any other Stremio addon, with shared sources and priorities.",
      ],
      [
        "Ce panneau présente les nouveautés après chaque livraison et reste accessible avec le bouton Nouveautés.",
        "This panel presents changes after each release and remains available from the What's new button.",
      ],
    ],
  },
  {
    id: "2026-11-09-remix",
    date: "2026-11-09",
    title: ["Accent devient Ether-Remix", "Accent becomes Ether-Remix"],
    items: [
      [
        "Nouvelle identité, nouveaux logos et image Docker publique pour amd64 et arm64.",
        "New identity, new logos and public Docker image for amd64 and arm64.",
      ],
      [
        "Stats : comparaison TMDB, historique par jour, semaine, mois et année, par média et source, en valeurs simples ou cumulées.",
        "Stats: TMDB comparison, daily, weekly, monthly and yearly history by media and source, with period totals or cumulative values.",
      ],
      [
        "Sources musicales communautaires, DMM avec Real-Debrid, playlists et API Subsonic.",
        "Community music sources, DMM with Real-Debrid, playlists and Subsonic API.",
      ],
    ],
  },
] as const;

export function RemixUpdates() {
  const dialog = useRef<HTMLDialogElement>(null);
  const [open, setOpen] = useState(false);
  useEffect(() => {
    try {
      if (localStorage.getItem(SEEN_KEY) !== REMIX_UPDATES[0].id) setOpen(true);
    } catch {
      /* L'accès manuel reste disponible si le stockage est bloqué. */
    }
  }, []);
  useEffect(() => {
    if (open && !dialog.current?.open) dialog.current?.showModal();
  }, [open]);
  const close = () => {
    try {
      localStorage.setItem(SEEN_KEY, REMIX_UPDATES[0].id);
    } catch {
      /* Stockage facultatif. */
    }
    setOpen(false);
    dialog.current?.close();
  };
  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setOpen(true)}
        aria-label={L("Nouveautés Ether-Remix", "Ether-Remix what's new")}
      >
        <Sparkles size={16} aria-hidden />{" "}
        <span className="hidden sm:inline">{L("Nouveautés", "What's new")}</span>
      </Button>
      <dialog
        ref={dialog}
        aria-labelledby="remix-updates-title"
        onCancel={(event) => {
          event.preventDefault();
          close();
        }}
        onClose={() => setOpen(false)}
        className="m-auto w-[calc(100%-2rem)] max-w-2xl max-h-[85dvh] overflow-y-auto rounded-2xl border border-border bg-bg p-0 text-fg shadow-xl backdrop:bg-black/60"
      >
        <div className="flex items-center justify-between gap-4 border-b border-border p-5">
          <h2 id="remix-updates-title" className="text-lg font-semibold">
            {L("Nouveautés Ether-Remix", "What's new in Ether-Remix")}
          </h2>
          <Button variant="ghost" size="sm" onClick={close} aria-label={L("Fermer", "Close")}>
            <X size={18} />
          </Button>
        </div>
        <div className="space-y-6 p-5">
          {REMIX_UPDATES.map((release) => (
            <section key={release.id}>
              <time className="text-xs text-fg-muted" dateTime={release.date}>
                {new Date(`${release.date}T12:00:00`).toLocaleDateString(getLocale(), {
                  dateStyle: "long",
                })}
              </time>
              <h3 className="mt-1 font-semibold">{L(release.title[0], release.title[1])}</h3>
              <ul className="mt-2 list-disc space-y-2 pl-5 text-sm text-fg-muted">
                {release.items.map((item) => (
                  <li key={item[1]}>{L(item[0], item[1])}</li>
                ))}
              </ul>
            </section>
          ))}
        </div>
        <div className="flex justify-end border-t border-border p-4">
          <Button onClick={close}>{L("J’ai compris", "Got it")}</Button>
        </div>
      </dialog>
    </>
  );
}
