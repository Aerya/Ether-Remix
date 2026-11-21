import { IndexerGroups } from "./IndexerGroups";
import { LocalMediaHealth } from "./LocalMediaHealth";
import { LocalMedia } from "./OwnedMedia";
import { CollapsibleSections, CollapsibleBlock } from "../components/CollapsibleSections";
import { AddSourceGallery, type GalleryChoice } from "../pages/sources/cards";
import type { DebridKind } from "../lib/types";
import { Badge, Button } from "@braedonsaunders/appkit-ui";
import { useRef, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { getLocale } from "../i18n";
import { IndexerList } from "../pages/sources/Indexers";
import { DebridSources } from "../pages/sources/Debrid";
import { UsenetProviders } from "../pages/sources/Usenet";
import { IPTVProviders } from "../pages/sources/IPTV";
import { ProwlarrImport } from "../components/ProwlarrImport";
import { Drawer } from "../components/Drawer";
import { NewReleaseBudgetSection } from "../pages/libraries/browser/NewReleasesSettings";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const CATEGORIES = [
  ["stremio", "Stremio", "Stremio"],
  [
    "indexeurs",
    "Indexeurs · Prowlarr / Jackett / NZBHydra2",
    "Indexers · Prowlarr / Jackett / NZBHydra2",
  ],
  ["usenet", "Fournisseurs Usenet", "Usenet providers"],
  ["debrid", "Debrid", "Debrid"],
  ["torrent", "Clients BitTorrent", "BitTorrent clients"],
  ["xdcc", "XDCC", "XDCC"],
  ["ftp", "FTP / SFTP", "FTP / SFTP"],
  ["musique", "Streaming musical", "Music streaming"],
  ["local", "Fichiers locaux", "Local files"],
  ["iptv", "IPTV", "IPTV"],
] as const;
/** Catégories regroupées par usage ; celles de `expert` n’apparaissent qu’en mode Expert. */
const GROUPS = [
  {
    fr: "Films, séries et animés",
    en: "Movies, shows and anime",
    ids: ["stremio", "debrid", "usenet", "indexeurs", "torrent", "xdcc", "ftp"],
  },
  { fr: "Musique", en: "Music", ids: ["musique"] },
  { fr: "Fichiers et télévision", en: "Files and TV", ids: ["local", "iptv"] },
] as const;
const EXPERT_ONLY = new Set(["indexeurs", "torrent", "xdcc", "ftp"]);

export function RemixSources({
  simple = false,
  music,
  stremio,
  torrent,
  xdcc,
  ftp,
}: {
  simple?: boolean;
  music: ReactNode;
  stremio?: ReactNode;
  torrent?: ReactNode;
  xdcc?: ReactNode;
  ftp?: ReactNode;
}) {
  const [params, setParams] = useSearchParams();
  const raw = params.get("source");
  const category = CATEGORIES.find(([id]) => id === raw)?.[0] ?? "stremio";
  const protocol = params.get("protocol");
  const [add, setAdd] = useState<{
    nonce: number;
    type: "stremio" | "site" | "newznab";
    name?: string;
  } | null>(null);
  const stremioList = useRef<HTMLDivElement>(null);
  const [savedSource, setSavedSource] = useState<string | null>(null);
  const [importing, setImporting] = useState(false);
  const [version, setVersion] = useState(0);
  const [gallery, setGallery] = useState(false);
  const [debridAdd, setDebridAdd] = useState<{ nonce: number; kind: DebridKind } | null>(null);
  const [usenetAdd, setUsenetAdd] = useState<number | null>(null);
  function pick(choice: GalleryChoice) {
    const nonce = Date.now();
    setGallery(false);
    const target =
      choice.type === "debrid"
        ? "debrid"
        : choice.type === "usenet"
          ? "usenet"
          : choice.type === "iptv"
            ? "iptv"
            : choice.type === "stremio"
              ? "stremio"
              : "indexeurs";
    setParams((p) => {
      const n = new URLSearchParams(p);
      n.set("source", target);
      n.delete("iptvSource");
      n.delete("new");
      if (choice.type === "iptv") n.set("new", choice.kind);
      return n;
    });
    if (choice.type === "debrid") setDebridAdd({ nonce, kind: choice.kind });
    else if (choice.type === "usenet") setUsenetAdd(nonce);
    else if (choice.type === "prowlarr") setImporting(true);
    else if (choice.type !== "iptv") setAdd({ nonce, type: choice.type });
  }
  return (
    <section className="space-y-5" aria-label={L("Gestion des sources", "Source management")}>
      <div>
        <p className="text-sm text-fg-muted">
          {simple
            ? L(
                "Ajoutez ici d’où viennent vos films, séries et musique. Commencez par « Ajouter une source ».",
                "Add where your movies, shows and music come from. Start with “Add a source”.",
              )
            : L(
                "Une configuration unique pour Ether et Remix. Les comptes, manifests et priorités sont partagés avec la lecture ; l’indexation en arrière-plan se règle séparément.",
                "One configuration shared by Ether and Remix. Accounts, manifests and priorities are shared with playback; background indexing is configured separately.",
              )}
        </p>
      </div>
      {simple ? null : (
        <div className="flex flex-wrap gap-4 text-sm">
          <Link className="underline" to="/remix/indexation">
            {L("Choisir les sources d’indexation", "Choose indexing sources")}
          </Link>
          <Link className="underline" to="/remix/lecture">
            {L("Régler les priorités de lecture", "Set playback priorities")}
          </Link>
        </div>
      )}
      <Button onClick={() => setGallery(true)}>{L("Ajouter une source", "Add a source")}</Button>
      <nav
        role="tablist"
        className="flex flex-wrap gap-x-6 gap-y-3"
        aria-label={L("Catégories de sources", "Source categories")}
      >
        {GROUPS.map((group) => {
          const items = group.ids
            .map((id) => CATEGORIES.find(([key]) => key === id)!)
            .filter(([id]) => !simple || !EXPERT_ONLY.has(id) || id === category);
          return items.length ? (
            <div key={group.fr} role="presentation" className="space-y-1">
              <p aria-hidden="true" className="text-xs uppercase tracking-wide text-fg-muted">
                {L(group.fr, group.en)}
              </p>
              <div role="presentation" className="flex flex-wrap gap-2">
                {items.map(([id, fr, en]) => (
                  <Button
                    key={id}
                    variant={id === category ? "default" : "outline"}
                    size="sm"
                    role="tab"
                    aria-selected={id === category}
                    aria-pressed={id === category}
                    onClick={() =>
                      setParams((p) => {
                        const next = new URLSearchParams(p);
                        next.set("source", id);
                        next.delete("iptvSource");
                        next.delete("new");
                        return next;
                      })
                    }
                  >
                    {L(fr, en)}
                  </Button>
                ))}
              </div>
            </div>
          ) : null;
        })}
      </nav>
      {simple ? null : (
        <div className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
          <span>{L("Contenus concernés :", "Relevant content:")}</span>
          {(category === "local"
            ? [L("Vidéos", "Videos"), L("Musique", "Music")]
            : category === "musique"
              ? [L("Musique", "Music")]
              : category === "iptv"
                ? [L("Télévision en direct", "Live TV")]
                : category === "xdcc" || category === "ftp"
                  ? [L("Films", "Movies"), L("Séries (épisodes SxxEyy)", "Shows (SxxEyy episodes)")]
                  : [
                      L("Films", "Movies"),
                      L("Séries", "Shows"),
                      L("Animes", "Anime"),
                      ...(category === "debrid" ||
                      (category === "indexeurs" && protocol !== "usenet")
                        ? [L("Musique via debrid", "Music via debrid")]
                        : category === "torrent"
                          ? [L("Musique", "Music")]
                          : []),
                    ]
          ).map((label) => (
            <Badge key={label} variant="outline">
              {label}
            </Badge>
          ))}
          <span>
            {L(
              "Selon les contenus disponibles chez le fournisseur.",
              "Subject to the provider’s available content.",
            )}
          </span>
        </div>
      )}
      <CollapsibleSections.Provider value={true}>
        {category === "stremio" ? (
          <>
            <div ref={stremioList}>
              {savedSource ? (
                <p role="status" className="mb-3 text-sm text-fg-muted">
                  {L(
                    `Source « ${savedSource} » enregistrée dans Modules Stremio.`,
                    `Source “${savedSource}” saved in Stremio addons.`,
                  )}
                </p>
              ) : null}
              <IndexerList
                view="stremio"
                addRequest={add}
                onSaved={(name) => {
                  setSavedSource(name);
                  const panel = stremioList.current?.querySelector("details");
                  if (panel) panel.open = true;
                  stremioList.current?.scrollIntoView?.({ block: "nearest", behavior: "smooth" });
                }}
              />
            </div>
            {simple ? null : (
              <CollapsibleBlock title="Seedstrem" id="seedstrem-integration">
                <div className="space-y-4 p-4 text-sm">
                  <p className="text-fg-muted">
                    {L(
                      "Addon auto-hébergé pour les films, séries et animés : Seedstrem recherche via Prowlarr et diffuse les fichiers pendant leur téléchargement avec qBittorrent ou Deluge. Le client torrent et les fichiers restent sur l’hôte Seedstrem ; aucun volume de téléchargement à monter dans Remix.",
                      "Self-hosted addon for movies, shows and anime: Seedstrem searches Prowlarr and streams files while qBittorrent or Deluge downloads them. The torrent client and files stay on the Seedstrem host; Remix needs no download volume.",
                    )}
                  </p>
                  <p className="text-fg-muted">
                    {L(
                      "Ajoutez le manifeste de votre instance, accessible depuis Remix. Une fois enregistrée, elle apparaît dans « Modules Stremio » ci-dessus, où vous pouvez la modifier. La recherche ne lance pas de téléchargement ; l’ouverture du flux peut en lancer un. Les liens Seedstrem ne sont ouverts qu’à la lecture, jamais pour les diagnostics ou contrôles de disponibilité. L’option « Source de dernier recours à la lecture » garde les recherches et l’indexation actives.",
                      "Add your instance’s manifest, reachable from Remix. Once saved, it appears in “Stremio addons” above, where you can edit it. Searching does not start downloads; opening a stream can. Seedstrem links open only for playback, never for diagnostics or availability checks. The “Last-resort playback source” option keeps search and indexing enabled.",
                    )}
                  </p>
                  <div className="flex flex-wrap items-center gap-4">
                    <Button
                      variant="outline"
                      onClick={() =>
                        setAdd({ nonce: Date.now(), type: "stremio", name: "Seedstrem" })
                      }
                    >
                      {L("Ajouter mon instance Seedstrem", "Add my Seedstrem instance")}
                    </Button>
                    <a
                      className="underline"
                      href="https://github.com/kipsilabs/seedstrem"
                      target="_blank"
                      rel="noreferrer"
                    >
                      {L("GitHub / auto-hébergement", "GitHub / self-hosting")}
                    </a>
                  </div>
                </div>
              </CollapsibleBlock>
            )}
            {simple ? null : stremio}
          </>
        ) : null}
        {category === "indexeurs" ? (
          <>
            <Button variant="outline" onClick={() => setImporting(true)}>
              {L("Importer depuis Prowlarr", "Import from Prowlarr")}
            </Button>
            <nav
              className="flex gap-2"
              aria-label={L("Protocoles des indexeurs", "Indexer protocols")}
            >
              {[
                ["all", "Tous", "All"],
                ["torrent", "BitTorrent", "BitTorrent"],
                ["usenet", "Fournisseurs Usenet", "Usenet providers"],
              ].map(([id, fr, en]) => (
                <Button
                  key={id}
                  variant={(protocol ?? "all") === id ? "default" : "outline"}
                  onClick={() =>
                    setParams((p) => {
                      const n = new URLSearchParams(p);
                      n.set("protocol", id!);
                      return n;
                    })
                  }
                >
                  {L(fr!, en!)}
                </Button>
              ))}
            </nav>
            <IndexerGroups
              key={protocol ?? "all"}
              view={
                protocol === "torrent"
                  ? "bittorrent"
                  : protocol === "usenet"
                    ? "usenet-indexers"
                    : "indexers"
              }
              version={version}
              addRequest={add}
            />
            <NewReleaseBudgetSection />
          </>
        ) : null}
        {category === "debrid" ? (
          <CollapsibleBlock title="Debrid">
            <DebridSources addKind={debridAdd} />
          </CollapsibleBlock>
        ) : null}
        {category === "usenet" ? (
          <CollapsibleBlock title="Usenet">
            <UsenetProviders addNonce={usenetAdd} />
          </CollapsibleBlock>
        ) : null}
        {category === "iptv" ? (
          <CollapsibleBlock title="IPTV">
            <IPTVProviders sourceParam="iptvSource" />
          </CollapsibleBlock>
        ) : null}
        {category === "torrent" ? torrent : null}
        {category === "xdcc" ? xdcc : null}
        {category === "ftp" ? ftp : null}
        {category === "musique" ? <>{music}</> : null}
        {category === "local" ? (
          <>
            <CollapsibleBlock title={L("Fichiers locaux", "Local files")}>
              <LocalMedia />
            </CollapsibleBlock>
            <CollapsibleBlock
              id="local-media-health"
              lazy
              title={L("Santé des médias locaux", "Local media health")}
            >
              <LocalMediaHealth />
            </CollapsibleBlock>
          </>
        ) : null}
      </CollapsibleSections.Provider>
      <AddSourceGallery open={gallery} onClose={() => setGallery(false)} onPick={pick} />
      <Drawer
        open={importing}
        onClose={() => setImporting(false)}
        title={L("Importer depuis Prowlarr", "Import from Prowlarr")}
        size="lg"
      >
        <ProwlarrImport onImported={() => setVersion((v) => v + 1)} />
      </Drawer>
    </section>
  );
}
