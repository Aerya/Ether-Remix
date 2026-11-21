import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

/**
 * Flux publics lus par défaut par l’index des pre. La liste doit rester identique à
 * `preDBPublicFeeds` (internal/pse/releasedb/predb.go) : un test le vérifie.
 */
export const PRE_PUBLIC_FEEDS = [
  {
    url: "https://api.predb.net/feed/",
    site: "https://predb.net/",
    fr: "predb.net · toutes catégories",
    en: "predb.net · all categories",
  },
  {
    url: "https://api.predb.net/?type=section&q=ANiME",
    site: "https://predb.net/",
    fr: "predb.net · animés (API)",
    en: "predb.net · anime (API)",
  },
  {
    url: "https://predb.me/?cats=movies&rss=1",
    site: "https://predb.me/",
    fr: "predb.me · films",
    en: "predb.me · movies",
  },
  {
    url: "https://predb.me/?cats=tv&rss=1",
    site: "https://predb.me/",
    fr: "predb.me · séries",
    en: "predb.me · shows",
  },
  {
    url: "https://predb.me/?cats=anime&rss=1",
    site: "https://predb.me/",
    fr: "predb.me · animés",
    en: "predb.me · anime",
  },
] as const;

const DOC = "https://github.com/Aerya/Ether-Remix/blob/main/docs/pse/";

/** Sources lues par défaut, avec leurs liens, en tête de la vue Pre (bêta). */
export function PreFeeds() {
  const fr = getLocale().startsWith("fr");
  return (
    <aside className="rounded-xl border border-border p-4 text-sm">
      <h2 className="font-semibold">
        {L("Flux publics lus par défaut", "Public feeds read by default")}
      </h2>
      <p className="mt-1 text-fg-muted">
        {L(
          "Lus quand l’option est activée et que « Flux publics » est coché. Seules les annonces sont lues : aucun lien de téléchargement n’est conservé.",
          "Read when the option is on and “Public feeds” is ticked. Only announcements are read: no download link is kept.",
        )}
      </p>
      <ul className="mt-2 space-y-1">
        {PRE_PUBLIC_FEEDS.map((feed) => (
          <li key={feed.url}>
            <a className="underline" href={feed.site} target="_blank" rel="noreferrer">
              {L(feed.fr, feed.en)}
            </a>
            <span className="text-fg-muted">
              {" — "}
              <a className="underline" href={feed.url} target="_blank" rel="noreferrer">
                {L("flux", "feed")}
              </a>
            </span>
          </li>
        ))}
      </ul>
      <p className="mt-3">
        <a
          className="underline"
          href={DOC + (fr ? "PRE.fr.md" : "PRE.en.md")}
          target="_blank"
          rel="noreferrer"
        >
          {L("Documentation : Pre (bêta)", "Documentation: Pre (beta)")}
        </a>
      </p>
    </aside>
  );
}
