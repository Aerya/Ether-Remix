import { Submaker } from "./Submaker";
import { QualityProfiles } from "./QualityProfiles";
import {
  CollapsibleBlock,
  CollapsibleSections,
  SettingsSection,
} from "../components/CollapsibleSections";
import { SourceHosting } from "./SourceHosting";
import { Badge, Button, Checkbox, Input, Select, Switch, toast } from "@braedonsaunders/appkit-ui";
import { RotateCcw } from "lucide-react";
import { useState, type ReactNode } from "react";
import { ErrorPanel, LoadingRows } from "../components/feedback";
import { DomainPage, useRouteTab } from "../components/layout";
import { SaveBar, useDraft, useUnsavedGuard } from "../components/SaveBar";
import { SettingsRow } from "../components/SettingsRow";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { MusicAccess } from "./MusicAccess";
import { MusicPlaylists } from "./MusicPlaylists";
import { IndexerDetails } from "./IndexerDetails";
import { MusicSourceSettings } from "./MusicSourceSettings";
import { SourceOrder } from "./SourceOrder";
import { SfrHelp } from "./SfrHelp";
import { SeerrCatalogHelp } from "./SeerrCatalogHelp";
import { TorrentStatus } from "./TorrentStatus";
import { XdccStatus } from "./XdccStatus";
import { ScrobLinks } from "./ScrobLinks";
import { ThemeLibraries } from "./ThemeLibraries";
import { SearchPriority } from "./SearchPriority";
import { RemixSources } from "./RemixSources";
import { REMIX_TABS, legacyRemixRoute } from "./navigation";
import { Link, Navigate, useLocation, useParams, useSearchParams } from "react-router-dom";
import { SourceStatistics, StatisticsPeriod } from "../pages/sources/statistics/Statistics";
import { ImportedSources } from "./ImportedSources";
import { VostfrTitles } from "./VostfrTitles";

/**
 * Page « Ether-Remix » de la console (nom public du fork), en onglets : Films et
 * séries, Animes, Musique, Bibliothèques, Stats, Notifications et suivi. Réglages : réglages des ajouts du fork, enregistrés par
 * /ether/api/pse/ (internal/pse/console). Les textes viennent du serveur en
 * français et en anglais ; la page choisit selon la langue de la console.
 * Voir docs/pse/FORK.fr.md.
 */

interface Text {
  fr: string;
  en: string;
}

interface Option {
  value: string;
  label: Text;
}

interface Field {
  key: string;
  kind: "toggle" | "number" | "duration" | "select" | "multi" | "countries" | "filter" | "url";
  label: Text;
  help: Text;
  default: string;
  placeholder?: string;
  advanced?: boolean;
  community?: boolean;
  min?: number;
  max?: number;
  value: string;
  source: "console" | "env" | "";
  env?: string;
  options?: Option[];
}

interface Feature {
  id: string;
  title: Text;
  description: Text;
  fields: Field[];
  status?: Record<string, unknown>;
}

interface PseData {
  features: Feature[];
}

const fr = () => getLocale().toLowerCase().startsWith("fr");
const L = (frText: string, enText: string) => (fr() ? frText : enText);
const T = (text: Text) => (fr() ? text.fr : text.en);

const BASE = "/remix";

/** Onglets de la page ; une section inconnue va dans le dernier. */
const TABS = REMIX_TABS;
type TabKey = (typeof TABS)[number]["key"];

export function featuresOfTab(tab: TabKey, features: Feature[]): Feature[] {
  const known = new Set<string>(TABS.flatMap((t) => [...t.features]));
  const def = TABS.find((t) => t.key === tab)!;
  const own = [...def.features].flatMap((id) => features.filter((f) => f.id === id));
  return tab === TABS[TABS.length - 1]!.key
    ? [...own, ...features.filter((f) => !known.has(f.id))]
    : own;
}

function valuesOf(data: PseData | null): Record<string, string> | null {
  if (!data) return null;
  const out: Record<string, string> = {};
  for (const f of data.features) for (const fd of f.fields) out[fd.key] = fd.value;
  return out;
}

export default function PsePage() {
  const { tab } = useParams();
  const location = useLocation();
  if (location.pathname.startsWith("/accent"))
    return (
      <Navigate
        replace
        to={location.pathname.replace(/^\/accent/, "/remix") + location.search + location.hash}
      />
    );
  if (!tab)
    return <Navigate replace to={"/remix/statistiques" + location.search + location.hash} />;
  let legacy = legacyRemixRoute(tab);
  const field = location.hash.match(/^#pse-(pse_[a-z_]+)$/i)?.[1]?.toUpperCase();
  if (tab === "films" && (field?.startsWith("PSE_INDEXER") || location.hash === "#pse-indexer"))
    legacy = "/remix/sources?source=indexation";
  if (tab === "musique" && field)
    legacy =
      musicFieldPlacement(field) === "source"
        ? "/remix/sources?source=musique"
        : musicFieldPlacement(field) === "tracking"
          ? "/remix/suivi"
          : "/remix/contenus?content=musique";
  return legacy ? <Navigate replace to={legacy + location.hash} /> : <PseSettings />;
}

const MUSIC_CONTENT = new Set([
  "PSE_MUSIC_PLAYLISTS_HOURS",
  "PSE_MUSIC_CHARTS",
  "PSE_MUSIC_DISCOVER",
  "PSE_MUSIC_COVERS_URL",
]);
const MUSIC_TRACKING = new Set(["PSE_LASTFM_API_KEY", "PSE_LASTFM_SECRET"]);
export function musicFieldPlacement(key: string) {
  return MUSIC_TRACKING.has(key) ? "tracking" : MUSIC_CONTENT.has(key) ? "content" : "source";
}

function PseSettings() {
  const [qualityActive, setQualityActive] = useState(false);
  const [params, setParams] = useSearchParams();
  const content = params.get("content") ?? "bibliotheques";
  const stats = params.get("view") ?? "indexation";
  const tab = useRouteTab(TABS);
  const doc = useAsync(() => apiFetch<PseData>("/pse/"), []);
  const [data, setData] = useState<PseData | null>(null);
  const current = data ?? doc.data ?? null;
  const saved = valuesOf(current);
  const { draft, setDraft, dirty, reset } = useDraft(saved);
  const [resets, setResets] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const changed = dirty || resets.length > 0;
  useUnsavedGuard(changed);

  if (doc.error && !current)
    return (
      <DomainPage title="Ether-Remix" base={BASE}>
        <ErrorPanel error={doc.error} onRetry={() => void doc.reload()} />
      </DomainPage>
    );
  if (!current || !draft || !saved)
    return (
      <DomainPage title="Ether-Remix" base={BASE}>
        <LoadingRows rows={6} />
      </DomainPage>
    );

  const save = async () => {
    const values: Record<string, string | null> = {};
    for (const key of Object.keys(draft))
      if (draft[key] !== saved[key]) values[key] = draft[key] ?? "";
    for (const key of resets) values[key] = null;
    setSaving(true);
    setError(null);
    try {
      const next = await apiFetch<PseData>("/pse/", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ values }),
      });
      setResets([]);
      setData(next);
      toast.success(L("Réglages enregistrés", "Settings saved"));
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };

  const renderFeature = (
    feature: Feature,
    placement: "source" | "content" | "tracking" = "content",
  ) => (
    <FeatureSection
      key={feature.id + placement}
      feature={
        qualityActive
          ? { ...feature, fields: feature.fields.filter((fd) => fd.key !== "PSE_PLAYABLE_FILTER") }
          : feature
      }
      placement={placement}
      draft={draft}
      resets={resets}
      onChange={(key, value) => {
        setResets(resets.filter((k) => k !== key));
        setDraft({ ...draft, [key]: value });
      }}
      onReset={(fd) => {
        setResets([...resets.filter((k) => k !== fd.key), fd.key]);
        setDraft({ ...draft, [fd.key]: fd.env ?? fd.default });
      }}
    />
  );
  const music = current.features.find((f) => f.id === "music");
  const musicPart = (placement: "source" | "content" | "tracking") =>
    music
      ? renderFeature(
          {
            ...music,
            fields: music.fields.filter((fd) => musicFieldPlacement(fd.key) === placement),
          },
          placement,
        )
      : null;
  return (
    <DomainPage
      title="Ether-Remix"
      base={BASE}
      tabs={TABS.map((t) => ({ key: t.key, label: L(t.fr, t.en) }))}
      active={tab}
      description={L(
        "Films, séries, animés et musique. Les réglages s'appliquent sans redémarrer.",
        "Movies, shows, anime and music. Settings apply without a restart.",
      )}
    >
      <CollapsibleSections.Provider key={tab} value={tab !== "statistiques"}>
        <div className="space-y-6">
          {tab === "sources" ? (
            <RemixSources
              music={musicPart("source")}
              torrent={current.features
                .filter((f) => ["torrent", "torrent2", "torrent3"].includes(f.id))
                .map((f) => renderFeature(f))}
              xdcc={current.features.filter((f) => f.id === "xdcc").map((f) => renderFeature(f))}
              stremio={
                <>
                  {current.features.filter((f) => f.id === "sfr").map((f) => renderFeature(f))}
                  <CollapsibleBlock title="SubMaker" id="submaker-integration">
                    {current.features
                      .filter((f) => f.id === "submaker")
                      .map((f) => renderFeature(f))}
                    <Submaker />
                  </CollapsibleBlock>
                  <CollapsibleBlock title="SeerrCatalog" id="seerrcatalog-integration">
                    <div className="space-y-4 p-4">
                      <p className="text-sm text-fg-muted">
                        {L(
                          "Deux fonctions indépendantes, chacune avec sa propre clé. Configurez-les aussi dans SeerrCatalog → Paramètres → Intégration Ether Remix.",
                          "Two independent features, each with its own token. Also configure them in SeerrCatalog → Settings → Ether Remix integration.",
                        )}
                      </p>
                      {current.features
                        .filter((f) => ["seerrcatalog", "seerrcatalog_search"].includes(f.id))
                        .map((f) => renderFeature(f))}
                    </div>
                  </CollapsibleBlock>
                </>
              }
              indexing={
                <>
                  {featuresOfTab(tab, current.features)
                    .filter(
                      (f) =>
                        ![
                          "sfr",
                          "seerrcatalog",
                          "seerrcatalog_search",
                          "torrent",
                          "torrent2",
                          "torrent3",
                          "xdcc",
                        ].includes(f.id),
                    )
                    .map((f) => renderFeature(f))}
                  <SearchPriority />
                </>
              }
            />
          ) : null}
          {tab === "priorites" ? <SourceOrder /> : null}
          {tab === "contenus" ? (
            <>
              <nav
                className="flex flex-wrap gap-2"
                aria-label={L("Types de contenus", "Content types")}
              >
                {[
                  ["films", "Films", "Movies"],
                  ["series", "Séries", "Shows"],
                  ["animes", "Animes", "Anime"],
                  ["musique", "Musique", "Music"],
                  ["bibliotheques", "Toutes les bibliothèques", "All libraries"],
                ].map(([key, fr, en]) => (
                  <Button
                    key={key}
                    variant={content === key ? "default" : "outline"}
                    aria-pressed={content === key}
                    onClick={() =>
                      setParams((p) => {
                        const n = new URLSearchParams(p);
                        n.set("content", key!);
                        return n;
                      })
                    }
                  >
                    {L(fr!, en!)}
                  </Button>
                ))}
              </nav>
              {current.features.filter((f) => f.id === "posters").map((f) => renderFeature(f))}
              {content === "animes" ? (
                <p className="text-sm text-fg-muted">
                  {L(
                    "Les animés utilisent vos critères de langue habituels. Les addons Stremio se configurent dans Sources.",
                    "Anime uses your usual language criteria. Configure Stremio addons in Sources.",
                  )}{" "}
                  <Link className="underline" to="/remix/sources?source=stremio">
                    {L("Gérer les sources Stremio", "Manage Stremio sources")}
                  </Link>
                </p>
              ) : null}
              {content === "musique" ? (
                musicPart("content")
              ) : (
                <ThemeLibraries
                  key={content}
                  preset={content === "animes" ? "anime" : undefined}
                  mediaType={
                    content === "films" ? "movie" : content === "series" ? "show" : undefined
                  }
                />
              )}
            </>
          ) : null}
          {tab === "langues" ? (
            <>
              <CollapsibleBlock
                title={L("Profils de qualité des releases", "Release quality profiles")}
              >
                <QualityProfiles onMode={setQualityActive} />
              </CollapsibleBlock>
              {qualityActive ? (
                <p className="rounded border border-border p-3 text-sm">
                  {L(
                    "Critères simples désactivés : les profils de qualité les remplacent. Le nombre minimum de releases reste applicable.",
                    "Simple criteria disabled: quality profiles replace them. The minimum release count still applies.",
                  )}
                </p>
              ) : null}
              <ThemeLibraries exceptionsOnly />
            </>
          ) : null}
          {tab === "demande" ? <VostfrTitles /> : null}
          {tab === "statistiques" ? (
            <>
              <nav
                className="flex flex-wrap gap-2"
                aria-label={L("Vues statistiques", "Statistics views")}
              >
                {[
                  ["indexation", "Indexation", "Indexing"],
                  ["sources", "Sources", "Sources"],
                  ["usage", "Utilisation", "Usage"],
                ].map(([key, fr, en]) => (
                  <Button
                    key={key}
                    variant={stats === key ? "default" : "outline"}
                    onClick={() =>
                      setParams((p) => {
                        const n = new URLSearchParams(p);
                        n.set("view", key!);
                        return n;
                      })
                    }
                  >
                    {L(fr!, en!)}
                  </Button>
                ))}
              </nav>
              {stats === "indexation" ? (
                <IndexerDetails />
              ) : (
                <>
                  <StatisticsPeriod />
                  <SourceStatistics view={stats === "usage" ? "usage" : "statistics"} />
                </>
              )}
            </>
          ) : null}
          {tab !== "sources" && tab !== "contenus"
            ? featuresOfTab(tab, current.features).map((f) => renderFeature(f))
            : null}
          {tab === "suivi" ? musicPart("tracking") : null}
        </div>
      </CollapsibleSections.Provider>
      <SaveBar
        dirty={changed}
        saving={saving}
        error={error}
        onSave={save}
        onDiscard={() => {
          reset();
          setResets([]);
          setError(null);
        }}
      />
    </DomainPage>
  );
}

function FeatureSection({
  feature,
  placement = "content",
  draft,
  resets,
  onChange,
  onReset,
}: {
  feature: Feature;
  placement?: "source" | "content" | "tracking";
  draft: Record<string, string>;
  resets: string[];
  onChange: (key: string, value: string) => void;
  onReset: (field: Field) => void;
}) {
  const basic = feature.fields.filter((fd) => !fd.advanced && !fd.community);
  const advanced = feature.fields.filter((fd) => fd.advanced);
  const row = (fd: Field) => (
    <SettingsRow
      key={fd.key}
      setting={`pse-${fd.key.toLowerCase()}`}
      stacked={
        fd.kind === "multi" ||
        fd.kind === "filter" ||
        (fd.kind === "url" && feature.id !== "seerrcatalog")
      }
      title={
        <span className="flex flex-wrap items-center gap-2" title={fd.key}>
          {T(fd.label)}
          <SourceBadge field={fd} pendingReset={resets.includes(fd.key)} />
        </span>
      }
      description={
        <>
          {T(fd.help)}
          <SourceHosting source={fd.key} />
        </>
      }
      control={
        <div
          className={
            feature.id === "seerrcatalog"
              ? "flex w-full items-center gap-2 sm:w-auto"
              : "flex flex-wrap items-center gap-2"
          }
        >
          <Control field={fd} value={draft[fd.key] ?? ""} onChange={(v) => onChange(fd.key, v)} />
          {fd.source === "console" && !resets.includes(fd.key) ? (
            <Button
              variant="ghost"
              size="sm"
              title={L(
                "Revenir à la variable d'environnement ou à la valeur par défaut",
                "Go back to the environment variable or the default",
              )}
              onClick={() => onReset(fd)}
            >
              <RotateCcw size={14} />
            </Button>
          ) : null}
          {fd.community ||
          fd.key === "PSE_MUSIC_HIFI_URL" ||
          fd.key === "PSE_MUSIC_ECLIPSE_URL" ||
          fd.key === "PSE_MUSIC_ALLDEBRID" ||
          fd.key === "PSE_MUSIC_REALDEBRID" ||
          fd.key === "PSE_MUSIC_DMM" ||
          fd.key === "PSE_MUSIC_TORBOX" ? (
            <MusicTest field={fd.key} url={draft[fd.key] ?? fd.value ?? ""} />
          ) : null}
        </div>
      }
    />
  );
  if (feature.id === "music" && placement === "source")
    return (
      <MusicSourceSettings
        fields={feature.fields.map((fd) => ({
          key: fd.key,
          value: draft[fd.key] ?? fd.value ?? "",
          advanced: fd.advanced,
          community: fd.community,
          control: row(fd),
        }))}
      />
    );
  if (feature.id === "sfr")
    return (
      <CollapsibleBlock id="pse-sfr" title={T(feature.title)} description={T(feature.description)}>
        <StatusLine feature={feature} />
        {basic.map(row)}
        <SfrHelp />
        {advanced.length ? (
          <details className="border-t border-border">
            <summary className="cursor-pointer select-none px-4 py-3 text-sm font-medium text-fg-muted hover:text-fg">
              {L("Options avancées", "Advanced options")} ({advanced.length})
            </summary>
            {advanced.map(row)}
          </details>
        ) : null}
      </CollapsibleBlock>
    );
  return (
    <div id={`pse-${feature.id}`}>
      <SettingsSection title={T(feature.title)} description={T(feature.description)}>
        {feature.id !== "music" || placement === "content" ? (
          <StatusLine feature={feature} />
        ) : null}
        {basic.map(row)}
        {feature.id === "seerrcatalog" ? <SeerrCatalogHelp status={feature.status} /> : null}
        {feature.id === "torrent" ? <TorrentStatus status={feature.status} /> : null}
        {feature.id === "xdcc" ? <XdccStatus status={feature.status} /> : null}
        {feature.id === "scrob" ? <ScrobLinks /> : null}
        {feature.id === "music" && placement === "content" ? (
          <>
            <MusicAccess />
            <MusicPlaylists />
          </>
        ) : null}
        {advanced.length ? (
          <details className="border-t border-border">
            <summary className="cursor-pointer select-none px-4 py-3 text-sm font-medium text-fg-muted hover:text-fg">
              {L("Options avancées", "Advanced options")} ({advanced.length})
            </summary>
            {advanced.map(row)}
          </details>
        ) : null}
      </SettingsSection>
    </div>
  );
}

function SourceBadge({ field, pendingReset }: { field: Field; pendingReset: boolean }) {
  if (pendingReset)
    return <Badge variant="warning">{L("Réinitialisé à l'enregistrement", "Reset on save")}</Badge>;
  if (field.source === "console") return <Badge variant="info">{L("Console", "Console")}</Badge>;
  if (field.source === "env")
    return <Badge variant="secondary">{L("Variable d'environnement", "Environment")}</Badge>;
  return null;
}

function Control({
  field,
  value,
  onChange,
}: {
  field: Field;
  value: string;
  onChange: (value: string) => void;
}) {
  const label = T(field.label);
  if (field.key === "PSE_INDEXER_SOURCES")
    return <ImportedSources value={value} options={field.options ?? []} onChange={onChange} />;
  switch (field.kind) {
    case "toggle":
      return (
        <Switch
          aria-label={label}
          checked={value === "on"}
          onChange={(e) => onChange(e.currentTarget.checked ? "on" : "off")}
        />
      );
    case "select":
      return (
        <Select
          aria-label={label}
          className="h-9 w-56 text-sm"
          value={value}
          onChange={(e) => onChange(e.target.value)}
        >
          {(field.options ?? []).map((o) => (
            <option key={o.value} value={o.value}>
              {T(o.label)}
            </option>
          ))}
        </Select>
      );
    case "multi":
      return <MultiControl field={field} value={value} onChange={onChange} />;
    case "number":
      return (
        <Input
          aria-label={label}
          type="number"
          className="h-9 w-32"
          min={field.min}
          max={field.max}
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    case "filter":
      return <FilterControl value={value} onChange={onChange} />;
    default:
      return (
        <Input
          aria-label={label}
          className={
            field.key.startsWith("PSE_SEERRCATALOG_")
              ? "h-9 min-w-0 flex-1 sm:w-96 sm:flex-none"
              : field.kind === "url"
                ? "h-9 w-full min-w-72"
                : "h-9 w-48"
          }
          placeholder={field.placeholder}
          value={value}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => onChange(e.target.value)}
        />
      );
  }
}

const splitList = (v: string) =>
  v
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean);

function MultiControl({
  field,
  value,
  onChange,
}: {
  field: Field;
  value: string;
  onChange: (value: string) => void;
}) {
  const selected = splitList(value);
  const options = [...(field.options ?? [])];
  // Un identifiant enregistré mais absent de la configuration reste visible.
  for (const v of selected)
    if (!options.some((o) => o.value === v)) options.push({ value: v, label: { fr: v, en: v } });
  if (options.length === 0)
    return (
      <span className="text-sm text-fg-muted">
        {L("Aucun indexeur configuré.", "No indexer configured.")}
      </span>
    );
  return (
    <div
      className={
        field.key === "PSE_INDEXER_SOURCES"
          ? "flex flex-col items-start gap-2"
          : "flex flex-wrap gap-x-5 gap-y-2"
      }
    >
      {options.map((o) => (
        <label key={o.value} className="flex items-center gap-2 text-sm">
          <Checkbox
            checked={selected.includes(o.value)}
            onChange={(e) => {
              const on = e.currentTarget.checked;
              const next = on ? [...selected, o.value] : selected.filter((s) => s !== o.value);
              // Conserve l'ordre des options pour une valeur stable.
              onChange(
                options
                  .map((x) => x.value)
                  .filter((x) => next.includes(x))
                  .join(","),
              );
            }}
          />
          {T(o.label)}
        </label>
      ))}
    </div>
  );
}

// --- critères du catalogue « lisible uniquement » ---

interface Criteria {
  lang: string[];
  min: string;
  max: string;
  codec: string[];
  cached: boolean;
  other: string[];
}

const LANGS: [string, string, string][] = [
  [
    "fr",
    "Français (toute VF : FRENCH, TRUEFRENCH, VFF, VFQ, VFi, VF, VOF, VF2)",
    "French (any dub: FRENCH, TRUEFRENCH, VFF, VFQ, VFi, VF, VOF, VF2)",
  ],
  ["vff", "VFF / TRUEFRENCH (France)", "VFF / TRUEFRENCH (France)"],
  ["vfq", "VFQ (Québec)", "VFQ (Québec)"],
  ["vfi", "VFi (internationale)", "VFi (international)"],
  ["multi", "MULTi", "MULTi"],
  ["en", "Anglais", "English"],
  ["vostfr", "VOSTFR / SUBFRENCH (sous-titres français)", "VOSTFR / SUBFRENCH (French subtitles)"],
  ["vo", "VO (sans doublage annoncé)", "Original (no dub announced)"],
];
const CODECS: [string, string][] = [
  ["h264", "H.264"],
  ["hevc", "HEVC"],
  ["av1", "AV1"],
];
const HEIGHTS = ["", "480", "720", "1080", "2160"];

export function parseCriteria(raw: string): Criteria {
  const c: Criteria = { lang: [], min: "", max: "", codec: [], cached: false, other: [] };
  for (const part of raw.split(";")) {
    const p = part.trim();
    if (!p) continue;
    const [k = "", v = ""] = p.split("=", 2).map((s) => s.trim());
    switch (k.toLowerCase()) {
      case "lang":
      case "langs":
      case "languages":
        c.lang = splitList(v.toLowerCase()).map((l) => (l === "qc" ? "vfq" : l));
        break;
      case "min":
      case "minheight":
        c.min = v.replace(/p$/i, "");
        break;
      case "max":
      case "maxheight":
        c.max = v.replace(/p$/i, "");
        break;
      case "codec":
      case "codecs":
        c.codec = splitList(v.toLowerCase()).map((x) =>
          x === "x265" || x === "h265" ? "hevc" : x === "x264" ? "h264" : x,
        );
        break;
      case "cached":
        c.cached = ["1", "true", "yes", "on", "oui"].includes(v.toLowerCase());
        break;
      default:
        c.other.push(p);
    }
  }
  return c;
}

export function formatCriteria(c: Criteria): string {
  const parts: string[] = [];
  if (c.lang.length) parts.push(`lang=${c.lang.join(",")}`);
  if (c.min) parts.push(`min=${c.min}`);
  if (c.max) parts.push(`max=${c.max}`);
  if (c.codec.length) parts.push(`codec=${c.codec.join(",")}`);
  if (c.cached) parts.push("cached=1");
  parts.push(...c.other);
  return parts.join(";");
}

function toggle(list: string[], v: string, on: boolean, order: string[]): string[] {
  const next = on ? [...list.filter((x) => x !== v), v] : list.filter((x) => x !== v);
  return [...order.filter((x) => next.includes(x)), ...next.filter((x) => !order.includes(x))];
}

function FilterControl({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const c = parseCriteria(value);
  const set = (patch: Partial<Criteria>) => onChange(formatCriteria({ ...c, ...patch }));
  const height = (v: string) => (v ? `${v}p` : L("Indifférent", "Any"));
  return (
    <div className="space-y-3 text-sm">
      <Group label={L("Langues", "Languages")}>
        {LANGS.map(([code, f, e]) => (
          <label key={code} className="flex items-center gap-2">
            <Checkbox
              checked={c.lang.includes(code)}
              onChange={(ev) =>
                set({
                  lang: toggle(
                    c.lang,
                    code,
                    ev.currentTarget.checked,
                    LANGS.map((l) => l[0]),
                  ),
                })
              }
            />
            {L(f, e)}
          </label>
        ))}
      </Group>
      <Group label={L("Résolution", "Resolution")}>
        <Select
          aria-label={L("Résolution minimum", "Minimum resolution")}
          className="h-8 w-36 text-sm"
          value={c.min}
          onChange={(e) => set({ min: e.target.value })}
        >
          {HEIGHTS.map((h) => (
            <option key={h} value={h}>
              {L("min. ", "min. ") + height(h)}
            </option>
          ))}
        </Select>
        <Select
          aria-label={L("Résolution maximum", "Maximum resolution")}
          className="h-8 w-36 text-sm"
          value={c.max}
          onChange={(e) => set({ max: e.target.value })}
        >
          {HEIGHTS.map((h) => (
            <option key={h} value={h}>
              {L("max. ", "max. ") + height(h)}
            </option>
          ))}
        </Select>
      </Group>
      <Group label={L("Codecs", "Codecs")}>
        {CODECS.map(([code, name]) => (
          <label key={code} className="flex items-center gap-2">
            <Checkbox
              checked={c.codec.includes(code)}
              onChange={(ev) =>
                set({
                  codec: toggle(
                    c.codec,
                    code,
                    ev.currentTarget.checked,
                    CODECS.map((x) => x[0]),
                  ),
                })
              }
            />
            {name}
          </label>
        ))}
      </Group>
      <label className="flex items-center gap-2">
        <Checkbox checked={c.cached} onChange={(ev) => set({ cached: ev.currentTarget.checked })} />
        {L(
          "Uniquement les releases annoncées en cache (marqueur de l'addon, non vérifié auprès du debrid)",
          "Only releases announced as cached (addon mark, not checked against the debrid service)",
        )}
      </label>
      <p className="text-xs text-fg-muted">
        {L(
          "Rien de coché : toutes les releases comptent.",
          "Nothing checked: every release counts.",
        )}{" "}
        {value ? <code>{value}</code> : null}
      </p>
    </div>
  );
}

function Group({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="grid gap-2 sm:grid-cols-[6rem_1fr]">
      <span className="w-24 shrink-0 text-fg-muted">{label}</span>
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 [&>label]:min-w-36">
        {children}
      </div>
    </div>
  );
}

function MusicTest({ field, url }: { field: string; url: string }) {
  const [busy, setBusy] = useState(false);
  return (
    <Button
      variant="outline"
      size="sm"
      disabled={busy || !url || url === "off"}
      onClick={async () => {
        setBusy(true);
        try {
          const r = await apiFetch<{ ok: boolean; message: string }>("/pse/music/test", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ key: field, url }),
          });
          if (r.ok) toast.success(r.message);
          else toast.error(r.message);
        } catch (e) {
          toast.error(e instanceof Error ? e.message : String(e));
        } finally {
          setBusy(false);
        }
      }}
    >
      {L("Tester", "Test")}
    </Button>
  );
}

// --- état de chaque fonction ---

const num = (v: unknown) => (typeof v === "number" ? v.toLocaleString(getLocale()) : "0");

function StatusLine({ feature }: { feature: Feature }) {
  const s = feature.status;
  if (!s || feature.id === "scrob") return null;
  const active = s.active === true;
  const items: ReactNode[] = [];
  if ("active" in s)
    items.push(
      <Badge key="state" variant={active ? "success" : "secondary"}>
        {active ? L("Actif", "On") : L("Inactif", "Off")}
      </Badge>,
    );
  if (feature.id === "sfr" && s.import && typeof s.import === "object") {
    const run = s.import as { state?: string; releases?: number; titles?: number; error?: string };
    if (run.state === "running")
      items.push(
        <span key="sfr">
          {L(
            `Import en cours : ${num(run.releases)} releases`,
            `Importing: ${num(run.releases)} releases`,
          )}
        </span>,
      );
    if (run.state === "ok")
      items.push(
        <span key="sfr">
          {L(
            `${num(run.releases)} releases importées pour ${num(run.titles)} titres`,
            `${num(run.releases)} releases imported for ${num(run.titles)} titles`,
          )}
        </span>,
      );
    if (run.state === "error")
      items.push(<span key="sfr">{L(`Échec : ${run.error}`, `Failed: ${run.error}`)}</span>);
  }
  if (feature.id === "notify")
    items.push(
      <span key="lang">
        {s.language === "fr"
          ? L("Notifications en français", "Notifications in French")
          : L("Notifications en anglais", "Notifications in English")}
      </span>,
    );
  if (feature.id === "playable" && typeof s.titles === "number")
    items.push(
      <span key="titles">
        {L(
          `${num(s.titles)} titres lisibles (≥ ${num(s.min)} releases)`,
          `${num(s.titles)} playable titles (≥ ${num(s.min)} releases)`,
        )}
      </span>,
    );
  if (feature.id === "music") {
    const lib = (s.library ?? {}) as Record<string, number>;
    if (s.source === "eclipse" || s.source === "hifi")
      items.push(
        <span key="src">
          {s.source === "eclipse"
            ? L("Source : Deezer + addon Eclipse", "Source: Deezer + Eclipse addon")
            : L("Source : HiFi API", "Source: HiFi API")}
        </span>,
      );
    if (active)
      items.push(
        <span key="path">
          {L("Adresse Subsonic : ", "Subsonic address: ")}
          <code>{window.location.origin}</code>
        </span>,
      );
    items.push(
      <span key="lib">
        {L(
          `Bibliothèques : ${num(lib.artist)} artistes, ${num(lib.album)} albums, ${num(lib.track)} titres`,
          `Libraries: ${num(lib.artist)} artists, ${num(lib.album)} albums, ${num(lib.track)} tracks`,
        )}
      </span>,
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-border px-4 py-3 text-sm text-fg-muted">
      {items}
    </div>
  );
}
