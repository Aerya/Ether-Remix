import { Badge, Button } from "@braedonsaunders/appkit-ui";
import { SettingsSection } from "../components/CollapsibleSections";
import { Link, useSearchParams, useLocation } from "react-router-dom";
import { Fragment, type ReactNode } from "react";
import { getLocale } from "../i18n";
import { api } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { MusicAddons } from "./MusicAddons";
import { IBroadcastSource } from "./OwnedMedia";
import { CollapsibleBlock } from "../components/CollapsibleSections";
import { MusicSources } from "./MusicSources";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export function musicSourceGroup(key: string, community = false) {
  if (community || key === "PSE_MUSIC_ECLIPSE_URL" || key === "PSE_MUSIC_ADDON_REGISTRY")
    return "eclipse";
  if (key === "PSE_MUSIC_DMM" || key === "PSE_MUSIC_DMM_URL" || key === "PSE_MUSIC_REALDEBRID")
    return "realdebrid";
  return "other";
}
interface SourceField {
  key: string;
  value?: string;
  advanced?: boolean;
  community?: boolean;
  control: ReactNode;
}
function RealDebridAccounts() {
  const config = useAsync(() => api.getConfig(), []);
  const accounts =
    config.data?.debrid?.providers
      ?.filter((p) => p.kind === "realdebrid")
      .sort((a, b) => a.priority - b.priority) ?? [];
  return (
    <div className="space-y-2 border-b border-border p-4 text-sm">
      <p className="font-medium">{L("Comptes Real-Debrid", "Real-Debrid accounts")}</p>
      {config.error ? (
        <p className="text-fg-muted">
          {L(
            "Comptes indisponibles. Ouvrez Sources → Debrid.",
            "Accounts unavailable. Open Sources → Debrid.",
          )}
        </p>
      ) : !config.data ? (
        <p>{L("Chargement…", "Loading…")}</p>
      ) : accounts.length ? (
        accounts.map((p) => (
          <div className="flex flex-wrap items-center gap-2" key={p.id}>
            <span>{p.name || "Real-Debrid"}</span>
            <Badge variant="outline">
              {p.enabled && p.apiKey
                ? L("Activé", "Enabled")
                : L("Inactif ou clé absente", "Inactive or missing key")}
            </Badge>
          </div>
        ))
      ) : (
        <p>{L("Aucun compte Real-Debrid configuré.", "No Real-Debrid account configured.")}</p>
      )}
      <Link className="text-primary underline" to="/remix/sources?source=debrid">
        {L(
          "Configurer les comptes dans Sources → Debrid",
          "Configure accounts under Sources → Debrid",
        )}
      </Link>
      <p className="text-xs text-fg-muted">
        {L(
          "DMM et la recherche par indexeurs partagent ces comptes et leur ordre de priorité.",
          "DMM and indexer searches share these accounts and their priority order.",
        )}
      </p>
    </div>
  );
}
export function MusicSourceSettings({ fields }: { fields: SourceField[] }) {
  const dmmBase =
    fields.find((f) => f.key === "PSE_MUSIC_DMM_URL")?.value?.trim() ||
    "https://debridmediamanager.com";
  let dmmLink: string | undefined;
  try {
    const url = new URL(dmmBase);
    if (
      ["http:", "https:"].includes(url.protocol) &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash
    )
      dmmLink = dmmBase.replace(/\/+$/, "") + "/albums";
  } catch {
    /* Le champ peut être en cours de saisie. */
  }
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const groups = ["eclipse", "realdebrid", "ibroadcast", "other", "diagnostic"];
  const hash = location.hash.toLowerCase();
  const hashField = fields.find((f) => hash.includes(f.key.toLowerCase()));
  const active = groups.includes(params.get("musicGroup") ?? "")
    ? params.get("musicGroup")!
    : (groups.find((id) => hash === `#music-${id}`) ??
      (hashField ? musicSourceGroup(hashField.key, hashField.community) : "eclipse"));
  const controls = (items: SourceField[]) =>
    items.map((f) => <Fragment key={f.key}>{f.control}</Fragment>);
  const group = (name: string) =>
    fields.filter((f) => musicSourceGroup(f.key, f.community) === name);
  const eclipse = group("eclipse"),
    rd = group("realdebrid"),
    others = group("other");
  return (
    <div className="space-y-6" id="pse-music">
      <p className="text-sm text-fg-muted">
        {L(
          "Le catalogue de découverte musicale est fourni par Deezer. Choisissez les sources qui trouvent et lisent les pistes ; un champ de manifest vide désactive la source.",
          "Deezer provides the music discovery catalogue. Choose the sources that find and play tracks; an empty manifest disables that source.",
        )}
      </p>
      <nav
        aria-label={L("Sections des sources musicales", "Music source sections")}
        role="tablist"
        className="flex flex-wrap gap-2"
      >
        {[
          ["eclipse", "Eclipse"],
          ["realdebrid", "DMM / Real-Debrid"],
          ["ibroadcast", "iBroadcast"],
          ["other", L("Autres sources", "Other sources")],
          ["diagnostic", L("Diagnostic audio", "Audio diagnostics")],
        ].map(([id, label]) => (
          <Button
            role="tab"
            id={`music-tab-${id}`}
            aria-controls={`music-${id}`}
            aria-selected={active === id}
            variant={active === id ? "default" : "outline"}
            key={id}
            onClick={() =>
              setParams((previous) => {
                const next = new URLSearchParams(previous);
                next.set("musicGroup", id!);
                return next;
              })
            }
          >
            {label}
          </Button>
        ))}
      </nav>
      <div
        id="music-eclipse"
        role="tabpanel"
        aria-labelledby="music-tab-eclipse"
        hidden={active !== "eclipse"}
      >
        <SettingsSection
          title="Eclipse"
          description={L(
            "Manifests personnels, addons communautaires et mise à jour des liens de configuration.",
            "Personal manifests, community addons and setup-link refresh.",
          )}
        >
          {controls(eclipse.filter((f) => !f.community && f.key !== "PSE_MUSIC_ADDON_REGISTRY"))}
          <MusicAddons
            fields={eclipse
              .filter((f) => f.community)
              .map((f) => ({ key: f.key, control: f.control }))}
          />
          {controls(eclipse.filter((f) => f.key === "PSE_MUSIC_ADDON_REGISTRY"))}
        </SettingsSection>
      </div>
      <div
        id="music-realdebrid"
        role="tabpanel"
        aria-labelledby="music-tab-realdebrid"
        hidden={active !== "realdebrid"}
      >
        <SettingsSection
          title="DMM / Real-Debrid"
          description={L(
            "Albums du catalogue DMM et recherche par indexeurs avec vos comptes Real-Debrid.",
            "DMM catalogue albums and indexer searches using your Real-Debrid accounts.",
          )}
        >
          <div className="space-y-2 border-b border-border p-4 text-sm">
            <a className="text-accent underline" href={dmmLink} target="_blank" rel="noreferrer">
              {L("Ouvrir le catalogue de l’instance DMM ↗", "Open the DMM instance catalogue ↗")}
            </a>
            <p>
              <a
                className="text-accent underline"
                href="https://github.com/debridmediamanager/debrid-media-manager#setup"
                target="_blank"
                rel="noreferrer"
              >
                {L("Auto-héberger DMM : code et installation ↗", "Self-host DMM: code and setup ↗")}
              </a>
            </p>
          </div>
          <RealDebridAccounts />
          {controls(rd)}
        </SettingsSection>
      </div>
      <div
        id="music-ibroadcast"
        role="tabpanel"
        aria-labelledby="music-tab-ibroadcast"
        hidden={active !== "ibroadcast"}
      >
        <CollapsibleBlock title="iBroadcast">
          <div className="p-5">
            <IBroadcastSource />
          </div>
        </CollapsibleBlock>
      </div>
      <div
        id="music-other"
        role="tabpanel"
        aria-labelledby="music-tab-other"
        hidden={active !== "other"}
      >
        <SettingsSection
          title={L("Autres sources", "Other sources")}
          description={L(
            "AllDebrid, TorBox, Audius et HiFi API. Les comptes debrid se règlent dans Sources → Debrid.",
            "AllDebrid, TorBox, Audius and HiFi API. Manage debrid accounts under Sources → Debrid.",
          )}
        >
          {controls(others.filter((f) => !f.advanced))}
          {others.some((f) => f.advanced) ? (
            <details className="border-t border-border">
              <summary className="cursor-pointer p-4 font-medium">
                {L("HiFi API et options avancées", "HiFi API and advanced options")}
              </summary>
              {controls(others.filter((f) => f.advanced))}
            </details>
          ) : null}
        </SettingsSection>
      </div>
      <section
        id="music-diagnostic"
        role="tabpanel"
        aria-labelledby="music-tab-diagnostic"
        hidden={active !== "diagnostic"}
      >
        <CollapsibleBlock title={L("Diagnostic audio", "Audio diagnostics")}>
          <MusicSources />
        </CollapsibleBlock>
      </section>
    </div>
  );
}
