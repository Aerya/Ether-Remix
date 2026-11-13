import { LocalMedia } from "./OwnedMedia";
import { CollapsibleSections, CollapsibleBlock } from "../components/CollapsibleSections";
import { AddSourceGallery, type GalleryChoice } from "../pages/sources/cards";
import type { DebridKind } from "../lib/types";
import { Badge, Button } from "@braedonsaunders/appkit-ui";
import { useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
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
  ["indexeurs", "Indexeurs", "Indexers"],
  ["usenet", "Usenet", "Usenet"],
  ["debrid", "Debrid", "Debrid"],
  ["torrent", "Client torrent", "Torrent client"],
  ["musique", "Streaming musical", "Music streaming"],
  ["local", "Fichiers locaux", "Local files"],
  ["iptv", "IPTV", "IPTV"],
  ["indexation", "Indexation", "Indexing"],
] as const;
export function RemixSources({
  music,
  indexing,
  stremio,
  torrent,
}: {
  music: ReactNode;
  indexing: ReactNode;
  stremio?: ReactNode;
  torrent?: ReactNode;
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
        <h2 className="font-semibold">{L("Sources", "Sources")}</h2>
        <p className="text-sm text-fg-muted">
          {L(
            "Une configuration unique pour Ether et Remix. Les comptes, manifests et priorités sont partagés avec la lecture ; l’indexation en arrière-plan se règle séparément.",
            "One configuration shared by Ether and Remix. Accounts, manifests and priorities are shared with playback; background indexing is configured separately.",
          )}
        </p>
      </div>
      <Button onClick={() => setGallery(true)}>{L("Ajouter une source", "Add a source")}</Button>
      <nav
        role="tablist"
        className="flex flex-wrap gap-2"
        aria-label={L("Catégories de sources", "Source categories")}
      >
        {CATEGORIES.map(([id, fr, en]) => (
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
      </nav>
      <div className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
        <span>{L("Contenus concernés :", "Relevant content:")}</span>
        {(category === "local"
          ? [L("Vidéos", "Videos"), L("Musique", "Music")]
          : category === "musique"
            ? [L("Musique", "Music")]
            : category === "iptv"
              ? [L("Télévision en direct", "Live TV")]
              : [
                  L("Films", "Movies"),
                  L("Séries", "Shows"),
                  L("Animes", "Anime"),
                  ...(category === "debrid" || (category === "indexeurs" && protocol !== "usenet")
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
      <CollapsibleSections.Provider value={true}>
        {category === "stremio" ? (
          <>
            <IndexerList view="stremio" addRequest={add} />
            {stremio}
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
                ["usenet", "Usenet", "Usenet"],
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
            <IndexerList
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
        {category === "musique" ? <>{music}</> : null}
        {category === "local" ? (
          <CollapsibleBlock title={L("Fichiers locaux", "Local files")}>
            <LocalMedia />
          </CollapsibleBlock>
        ) : null}
        {category === "indexation" ? indexing : null}
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
