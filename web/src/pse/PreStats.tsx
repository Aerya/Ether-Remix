import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { PRE_PUBLIC_FEEDS } from "./PreFeeds";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const num = (n: number) => n.toLocaleString(getLocale());

export interface PreFeedStat {
  feed: string;
  seen: number;
  indexed: number;
  pending: number;
  unmatched: number;
  /** Dernière lecture du flux : « ok » ou « error », annonces reçues et retenues. */
  label?: string;
  state?: string;
  readAt?: number;
  fetched?: number;
  kept?: number;
  error?: string;
}
export interface PreStatus {
  active?: boolean;
  import?: {
    state?: string;
    last?: number;
    added?: number;
    pending?: number;
    error?: string;
    feeds?: PreFeedStat[];
  };
}

/** Nom d’un flux : sa description s’il est public, « Flux personnel N » sinon (adresse jamais exposée). */
export function preFeedLabel(feed: string, name?: string): string {
  const pub = PRE_PUBLIC_FEEDS.find((f) => f.url === feed);
  if (pub) return L(pub.fr, pub.en);
  const personal = /^perso:(\d+)$/.exec(feed);
  if (personal) return name || L(`Site ${personal[1]}`, `Site ${personal[1]}`);
  return L("Plus anciennes ou flux inconnu", "Older or unknown feed");
}

/**
 * Compteurs des pre, affichés dans Statistiques seulement quand l’option est
 * activée : releases indexées au total, releases venues des pre et pre en attente.
 */
export function PreStats({
  status,
  total: givenTotal,
}: {
  status: PreStatus | undefined;
  /** Total des releases indexées, fourni par Statistiques ; sinon lu ici. */
  total?: number | null;
}) {
  const [fetched, setFetched] = useState<number | null>(null);
  const total = givenTotal !== undefined ? givenTotal : fetched;
  useEffect(() => {
    if (!status?.active || givenTotal !== undefined) return;
    let live = true;
    apiFetch<{ references?: number }>("/pse/release-library?overview=1&summary=1")
      .then((d) => {
        if (live && typeof d.references === "number") setFetched(d.references);
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [status?.active, givenTotal]);
  if (!status) return null;
  if (!status.active)
    return (
      <section
        className="rounded-xl border border-border p-4 text-sm"
        aria-label={L("Pre (bêta)", "Pre (beta)")}
      >
        <h3 className="font-semibold">{L("Pre (bêta)", "Pre (beta)")}</h3>
        <p className="mt-1 text-fg-muted">
          {L(
            "Désactivée. Lit les annonces de releases (pre) françaises, MULTi et VOSTFR pour alimenter l’index ; les compteurs apparaissent ici une fois activée.",
            "Off. Reads French, MULTi and VOSTFR release announcements (pre) to feed the index; counters appear here once it is on.",
          )}{" "}
          <Link className="underline" to="/remix/indexation?view=pre">
            {L("Activer dans Indexation → Pre (bêta)", "Enable under Indexing → Pre (beta)")}
          </Link>
        </p>
      </section>
    );
  const added = status.import?.added ?? 0;
  const share = total && total > 0 ? Math.round((added * 1000) / total) / 10 : null;
  const last = status.import?.last
    ? new Date(status.import.last * 1000).toLocaleString(getLocale(), {
        dateStyle: "medium",
        timeStyle: "short",
      })
    : null;
  const globalCard: [string, string, string] = [
    L("Releases indexées (global)", "Indexed releases (overall)"),
    total === null ? "—" : num(total),
    L("Toutes sources confondues", "All sources combined"),
  ];
  const cards: [string, string, string][] = [
    // Statistiques affiche déjà le total au-dessus : pas de doublon.
    ...(givenTotal !== undefined ? [] : [globalCard]),
    [
      L("Releases issues des pre", "Releases from pre"),
      num(added),
      share === null ? "" : L(`${share} % du total`, `${share}% of the total`),
    ],
    [
      L("Pre en attente", "Pending pre"),
      num(status.import?.pending ?? 0),
      L("Délai ou titre pas encore trouvé", "Waiting for the delay or a title"),
    ],
  ];
  return (
    <section className="space-y-3" aria-label={L("Pre (bêta)", "Pre (beta)")}>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h3 className="font-semibold">{L("Pre (bêta)", "Pre (beta)")}</h3>
        <p className="text-xs text-fg-muted">
          {last
            ? L(`Dernière lecture : ${last}`, `Last read: ${last}`)
            : L("Aucune lecture pour le moment", "Nothing read yet")}
          {" · "}
          <Link className="underline" to="/remix/indexation?view=pre">
            {L("Réglages", "Settings")}
          </Link>
        </p>
      </div>
      <div className="grid gap-3 md:grid-cols-3">
        {cards.map(([label, value, hint]) => (
          <div key={label} className="rounded-xl border border-border p-4">
            <p className="text-xs text-fg-muted">{label}</p>
            <p className="mt-1 text-xl font-semibold tabular-nums">{value}</p>
            {hint ? <p className="mt-1 text-xs text-fg-muted">{hint}</p> : null}
          </div>
        ))}
      </div>
      {status.import?.feeds?.length ? <PreFeedTable feeds={status.import.feeds} /> : null}
      {status.import?.error ? (
        <p role="alert" className="text-sm text-warning">
          {status.import.error}
        </p>
      ) : null}
      <p className="text-xs text-fg-muted">
        {L(
          "Le détail des releases par source, dont les pre, se trouve dans Recherches.",
          "The per-source release detail, including pre, is under Searches.",
        )}{" "}
        <Link className="underline" to="/remix/apercu?view=recherches">
          {L("Voir Recherches", "View Searches")}
        </Link>
      </p>
    </section>
  );
}

function PreFeedTable({ feeds }: { feeds: PreFeedStat[] }) {
  const total = feeds.reduce(
    (acc, f) => ({
      seen: acc.seen + f.seen,
      indexed: acc.indexed + f.indexed,
      pending: acc.pending + f.pending,
      unmatched: acc.unmatched + f.unmatched,
    }),
    { seen: 0, indexed: 0, pending: 0, unmatched: 0 },
  );
  const head = [
    L("Pre lues (30 j)", "Pre read (30 d)"),
    L("Releases indexées", "Indexed releases"),
    L("En attente", "Pending"),
    L("Non trouvées", "Not matched"),
  ];
  const lastRead = (f: PreFeedStat) =>
    f.state === "error" ? (
      <span className="text-warning">
        {L("Erreur", "Error")} · {f.error || L("flux inaccessible", "feed unreachable")}
      </span>
    ) : f.state === "ok" ? (
      L(
        `OK · ${num(f.fetched ?? 0)} annonces · ${num(f.kept ?? 0)} retenues`,
        `OK · ${num(f.fetched ?? 0)} announcements · ${num(f.kept ?? 0)} kept`,
      )
    ) : (
      "—"
    );
  const cell = "px-4 py-3 text-right tabular-nums";
  return (
    <div className="overflow-x-auto rounded-xl border border-border">
      <table className="w-full min-w-[760px] text-sm">
        <caption className="sr-only">{L("Détail par pre", "Detail by pre")}</caption>
        <thead>
          <tr className="border-b border-border text-xs text-fg-muted">
            <th scope="col" className="px-4 py-3 text-left font-medium">
              {L("Pre (flux)", "Pre (feed)")}
            </th>
            {head.map((h) => (
              <th key={h} scope="col" className="px-4 py-3 text-right font-medium">
                {h}
              </th>
            ))}
            <th scope="col" className="px-4 py-3 text-left font-medium">
              {L("Dernière lecture", "Last read")}
            </th>
          </tr>
        </thead>
        <tbody>
          {feeds.map((f) => (
            <tr key={f.feed || "none"} className="border-b border-border">
              <th scope="row" className="px-4 py-3 text-left font-medium">
                {preFeedLabel(f.feed, f.label)}
              </th>
              <td className={cell}>{num(f.seen)}</td>
              <td className={cell}>{num(f.indexed)}</td>
              <td className={cell}>{num(f.pending)}</td>
              <td className={`${cell} ${f.unmatched > 0 ? "text-warning" : ""}`}>
                {num(f.unmatched)}
              </td>
              <td className="px-4 py-3 text-left text-xs text-fg-muted">{lastRead(f)}</td>
            </tr>
          ))}
          <tr>
            <th scope="row" className="px-4 py-3 text-left font-semibold">
              {L("Total", "Total")}
            </th>
            <td className={`${cell} font-semibold`}>{num(total.seen)}</td>
            <td className={`${cell} font-semibold`}>{num(total.indexed)}</td>
            <td className={`${cell} font-semibold`}>{num(total.pending)}</td>
            <td className={`${cell} font-semibold`}>{num(total.unmatched)}</td>
            <td />
          </tr>
        </tbody>
      </table>
    </div>
  );
}
