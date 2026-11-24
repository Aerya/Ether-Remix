import { Button } from "@braedonsaunders/appkit-ui";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { getLocale } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";
import { setSetupFlag } from "./remixMode";
import { JukeboxQuickSetup } from "./JukeboxQuickSetup";
import { ThemeLibraries } from "./ThemeLibraries";
import {
  contentOptions,
  discoveryOptions,
  initialChoices,
  restoreChoices,
  setupSources,
  usageOptions,
  visibleSetupSources,
  type Copy,
} from "./setupModel";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const text = (c: Copy) => L(...c);
const ROOT = "/pse/setup";
const STORAGE = "remix.setup.intent.v1";
const STEPS: Copy[] = [
  ["Vos contenus", "Your content"],
  ["Langues et disponibilité", "Languages and availability"],
  ["Découverte", "Discovery"],
  ["Modes de lecture", "Playback modes"],
  ["Connexions", "Connections"],
  ["Bibliothèques et accueil", "Libraries and home"],
  ["Affiches", "Posters"],
  ["Examiner et appliquer", "Review and apply"],
];
type Values = Record<string, string | null>;
type Field = {
  key: string;
  value: string;
  label: { fr: string; en: string };
  source: string;
  options?: { value: string; label: { fr: string; en: string } }[];
};
type Warning = { id: string; message: { fr: string; en: string }; href: string };
type State = { revision: string; fields: Field[]; warnings: Warning[] };
type Change = {
  key: string;
  label: { fr: string; en: string };
  before: string;
  after: string;
  reset: boolean;
};
type Plan = {
  revision: string;
  plan: string;
  values: Values;
  changes: Change[];
  warnings: Warning[];
};
const translated = (s: { fr: string; en: string }) => L(s.fr, s.en);
function readChoices() {
  try {
    return restoreChoices(JSON.parse(localStorage.getItem(STORAGE) || "null"));
  } catch {
    return { ...initialChoices };
  }
}
function SettingsLink({ to, children }: { to: string; children: ReactNode }) {
  return (
    <Link
      className="inline-flex items-center gap-2 rounded border border-border px-3 py-2 text-sm hover:bg-surface"
      to={to}
      target="_blank"
      rel="noopener noreferrer"
    >
      {children}
      <span aria-hidden="true">↗</span>
      <span className="sr-only">{L("Nouvel onglet", "New tab")}</span>
    </Link>
  );
}

/** Replacement for the original four-step wizard. Only the reviewed server
 * plan writes global settings; existing editors keep their explicit save flow. */
export function RemixWizard() {
  const [params, setParams] = useSearchParams();
  const rawStep = Number(params.get("step"));
  const step = Number.isFinite(rawStep)
    ? Math.max(1, Math.min(STEPS.length, Math.trunc(rawStep) || 1))
    : 1;
  const [choices, setChoices] = useState(readChoices);
  const [state, setState] = useState<State | null>(null);
  const [patch, setPatch] = useState<Values>({});
  const [plan, setPlan] = useState<Plan | null>(null);
  const [undo, setUndo] = useState<Values | null>(null);
  const [busy, setBusy] = useState(false),
    [loading, setLoading] = useState(true),
    [error, setError] = useState(""),
    [notice, setNotice] = useState("");
  const [editLibraries, setEditLibraries] = useState(false);
  const [ack, setAck] = useState(false);
  const load = useCallback(async () => {
    setLoading(true);
    setPlan(null);
    setAck(false);
    try {
      const next = await apiFetch<State>(ROOT);
      if (!Array.isArray(next.fields) || !next.revision)
        throw new Error(
          L("État de configuration indisponible.", "Configuration state unavailable."),
        );
      setState(next);
      setError("");
    } catch (e) {
      setError(errorMessage(e));
      setState(null);
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    setSetupFlag("wizard");
    void load();
  }, [load]);
  useEffect(() => {
    try {
      localStorage.setItem(STORAGE, JSON.stringify(choices));
    } catch {
      /* Choices remain usable when storage is disabled. */
    }
  }, [choices]);
  // Returning from a settings tab invalidates an old preview, but never writes.
  useEffect(() => {
    const focus = () => {
      void load();
    };
    window.addEventListener("focus", focus);
    return () => window.removeEventListener("focus", focus);
  }, [load]);
  const go = (n: number) =>
    setParams((p) => {
      const next = new URLSearchParams(p);
      next.set("step", String(n));
      return next;
    });
  const choose = (key: "contents" | "usages" | "discovery" | "sources" | "clients", id: string) => {
    setChoices((c) =>
      restoreChoices({
        ...c,
        [key]: c[key].includes(id as never) ? c[key].filter((x) => x !== id) : [...c[key], id],
      }),
    );
    setNotice("");
  };
  const change = (key: string, value: string | null) => {
    setPatch((p) => ({ ...p, [key]: value }));
    setPlan(null);
    setAck(false);
    setNotice("");
  };
  const value = (key: string) =>
    patch[key] ?? state?.fields.find((f) => f.key === key)?.value ?? "";
  const label = (key: string) => {
    const f = state?.fields.find((f) => f.key === key);
    return f ? translated(f.label) : key;
  };
  const displayValue = (key: string, v: string) => {
    if (v === "on") return L("Activé", "Enabled");
    if (v === "off") return L("Désactivé", "Disabled");
    const option = state?.fields.find((f) => f.key === key)?.options?.find((o) => o.value === v);
    return option ? translated(option.label) : v || L("Valeur par défaut", "Default value");
  };
  async function preview(values = patch) {
    if (!state) return;
    setBusy(true);
    setError("");
    setNotice("");
    setPlan(null);
    setAck(false);
    try {
      const p = await apiFetch<Plan>(ROOT + "/preview", {
        method: "POST",
        body: JSON.stringify({ revision: state.revision, values }),
      });
      setPlan(p);
      setPatch(values);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function apply() {
    if (!plan || !ack) return;
    setBusy(true);
    setError("");
    try {
      const result = await apiFetch<{ undo: Values }>(ROOT + "/apply", {
        method: "POST",
        body: JSON.stringify({ revision: plan.revision, values: plan.values, plan: plan.plan }),
      });
      setUndo(result.undo);
      setPatch({});
      setPlan(null);
      await load();
      setNotice(
        L(
          "Réglages appliqués. Les connexions et les créations réalisées dans leurs éditeurs gardent leur propre état ; vérifiez la lecture dans votre client.",
          "Settings applied. Connections and creations made in their editors keep their own state; verify playback in your client.",
        ),
      );
    } catch (e) {
      setError(errorMessage(e));
      setPlan(null);
      setAck(false);
    } finally {
      setBusy(false);
    }
  }
  const option = (
    key: "contents" | "usages" | "discovery" | "sources" | "clients",
    id: string,
    title: string,
    help?: string,
  ) => (
    <label
      key={id}
      className={`flex items-start gap-3 rounded-xl border p-4 ${choices[key].includes(id as never) ? "border-primary bg-primary/5" : "border-border"}`}
    >
      <input
        className="mt-1"
        type="checkbox"
        checked={choices[key].includes(id as never)}
        onChange={() => choose(key, id)}
        disabled={busy}
      />
      <span>
        <span className="block font-medium">{title}</span>
        {help ? <span className="mt-1 block text-sm text-fg-muted">{help}</span> : null}
      </span>
    </label>
  );
  const toggle = (key: string, title: string, help: string) => (
    <label className="flex items-start gap-3 rounded border border-border p-3">
      <input
        type="checkbox"
        checked={value(key) === "on"}
        disabled={!state || busy || loading}
        onChange={(e) => change(key, e.target.checked ? "on" : "off")}
      />
      <span>
        {title}
        <span className="block text-sm text-fg-muted">{help}</span>
      </span>
    </label>
  );
  const select = (key: string, title: string, opts: { id: string; label: string }[]) => (
    <label className="block space-y-1">
      <span className="block text-sm font-medium">{title}</span>
      <select
        className="w-full rounded border border-border bg-surface p-2"
        value={value(key)}
        disabled={!state || busy || loading}
        onChange={(e) => change(key, e.target.value)}
      >
        {!opts.some((o) => o.id === value(key)) ? (
          <option value={value(key)}>
            {L("Réglage personnalisé conservé", "Custom setting preserved")}
          </option>
        ) : null}
        {opts.map((o) => (
          <option value={o.id} key={o.id}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  );
  const warnings = (items: Warning[]) => (
    <ul className="space-y-2">
      {items.map((w) => (
        <li key={w.id} className="rounded border border-border p-3 text-sm">
          <p>{translated(w.message)}</p>
          <SettingsLink to={w.href}>{L("Examiner ce réglage", "Review this setting")}</SettingsLink>
        </li>
      ))}
    </ul>
  );
  const activeSources = visibleSetupSources(choices);
  const selectedSources = setupSources.filter((s) => choices.sources.includes(s.id));
  const outOfScope = selectedSources.filter((s) => !activeSources.some((a) => a.id === s.id));
  return (
    <section
      className="mx-auto max-w-5xl space-y-5"
      aria-label={L("Assistant de configuration", "Setup assistant")}
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="text-xl font-semibold">
            {L("Configurer Remix selon vos envies · BETA", "Configure Remix around your needs")}
          </h2>
          <p className="text-sm text-fg-muted">
            {L(
              "Vos réponses orientent le parcours. Seuls les changements examinés sont appliqués ; décocher un contenu ne supprime rien.",
              "Your answers guide the journey. Only reviewed changes are applied; unchecking content deletes nothing.",
            )}
          </p>
        </div>
        <Button variant="outline" size="sm" disabled={loading || busy} onClick={() => void load()}>
          {L("Actualiser l’état", "Refresh state")}
        </Button>
      </div>
      <nav
        aria-label={L("Étapes de configuration", "Setup steps")}
        className="flex flex-wrap gap-2"
      >
        {STEPS.map((s, i) => (
          <Button
            key={s[0]}
            variant={step === i + 1 ? "default" : "outline"}
            size="sm"
            aria-current={step === i + 1 ? "step" : undefined}
            disabled={busy}
            onClick={() => go(i + 1)}
          >
            {i + 1}. {text(s)}
          </Button>
        ))}
      </nav>
      {error ? (
        <div role="alert" className="text-danger">
          {error}{" "}
          <Button variant="outline" size="sm" onClick={() => void load()} disabled={busy}>
            {L("Actualiser puis réexaminer", "Refresh and review again")}
          </Button>
        </div>
      ) : null}
      {loading ? (
        <p role="status">{L("Lecture de la configuration…", "Reading configuration…")}</p>
      ) : null}
      {notice ? (
        <p role="status" className="rounded border border-border p-3">
          {notice}
        </p>
      ) : null}
      <div className="space-y-4">
        {step === 1 ? (
          <>
            <h3 className="text-lg font-medium">
              {L("Que souhaitez-vous retrouver ?", "What would you like to find?")}
            </h3>
            <div className="grid gap-3 sm:grid-cols-2">
              {contentOptions.map((o) => option("contents", o.id, text(o.label), text(o.help)))}
            </div>
            <p className="text-sm text-fg-muted">
              {L(
                "Les contenus existants restent accessibles. Ces choix filtrent les propositions de l’assistant, pas les droits des spectateurs.",
                "Existing content remains accessible. These choices filter wizard suggestions, not viewer access rights.",
              )}
            </p>
          </>
        ) : null}
        {step === 2 ? (
          <>
            <h3 className="text-lg font-medium">
              {L(
                "Que doit afficher le catalogue général ?",
                "What should the general catalogue show?",
              )}
            </h3>
            {toggle(
              "PSE_FRENCH",
              L("Privilégier le catalogue français", "Use the French catalogue"),
              L(
                "Combine l’origine, les sorties et les releases connues. Ce n’est pas une garantie d’audio français à la lecture.",
                "Uses origin, releases and known sources. This is not a guarantee of French audio at playback.",
              ),
            )}
            {select(
              "PSE_FRENCH_RELEASES",
              L(
                "Tags de release suffisants pour ce catalogue",
                "Release tags sufficient for this catalogue",
              ),
              [
                {
                  id: "french,multi",
                  label: L(
                    "Toutes les VF + MULTi (VFQ incluse)",
                    "All French dubs + MULTi (including Quebec)",
                  ),
                },
                {
                  id: "vff,multi",
                  label: L("VFF / TRUEFRENCH + MULTi", "VFF / TRUEFRENCH + MULTi"),
                },
                { id: "vfq,multi", label: "VFQ + MULTi" },
                {
                  id: "french",
                  label: L(
                    "Toutes les VF, sans se fier au seul tag MULTi",
                    "All French dubs, without relying on MULTi alone",
                  ),
                },
              ],
            )}
            <p className="text-sm text-fg-muted">
              {L(
                "Ces tags ne sont pas un ordre de préférence audio. Pour exiger ou préférer VFF/VFQ, utilisez le profil de lecture ; MULTi seul ne prouve pas une piste française.",
                "These tags are not audio preferences. To require or prefer France/Quebec audio, use playback profiles; MULTi alone does not prove a French track.",
              )}
            </p>
            <label className="flex gap-2">
              <input
                type="checkbox"
                checked={choices.original}
                onChange={(e) => setChoices((c) => ({ ...c, original: e.target.checked }))}
              />
              {L(
                "Je veux aussi des sections VO / VOSTFR",
                "I also want original-language / French-subtitled sections",
              )}
            </label>
            {choices.original ? (
              <div className="space-y-2">
                <p>
                  {L(
                    "Créer une bibliothèque toutes langues ou une exception de collection conserve ces titres dans leur section. Un profil audio français peut encore empêcher leur lecture.",
                    "An all-language library or collection exception keeps these titles in their section. A French-audio profile can still prevent playback.",
                  )}
                </p>
                <SettingsLink to="/remix/lecture?view=vostfr">
                  {L("Choisir les titres VOSTFR", "Choose non-dubbed titles")}
                </SettingsLink>
              </div>
            ) : null}
            <label className="flex gap-2">
              <input
                type="checkbox"
                checked={choices.subtitles}
                onChange={(e) => setChoices((c) => ({ ...c, subtitles: e.target.checked }))}
              />
              {L(
                "Je souhaite rechercher ou traduire des sous-titres avec SubMaker",
                "I want to find or translate subtitles with SubMaker",
              )}
            </label>
            {choices.subtitles ? (
              <>
                <p className="text-sm">
                  {L(
                    "Manifeste personnel, langues, budget et périmètre à configurer. La traduction utilise un sous-titre exploitable ; elle ne garantit pas la création depuis l’audio.",
                    "Configure a personal manifest, languages, budget and scope. Translation requires usable subtitles; audio transcription is not guaranteed.",
                  )}
                </p>
                <SettingsLink to="/remix/lecture?view=soustitres">
                  {L("Configurer SubMaker", "Configure SubMaker")}
                </SettingsLink>
              </>
            ) : null}
            <SettingsLink to="/remix/lecture?view=qualite">
              {L("Profils audio, qualité et exceptions", "Audio, quality profiles and exceptions")}
            </SettingsLink>
            {toggle(
              "PSE_PLAYABLE",
              L("Limiter aux disponibilités indexées", "Limit to indexed availability"),
              L(
                "Les sources peuvent expirer. Les pre et SeerrCatalog ont des règles particulières signalées à l’examen.",
                "Sources can expire. Pre and SeerrCatalog have special rules shown during review.",
              ),
            )}
            {select(
              "PSE_PLAYABLE_MIN",
              L("Nombre minimum de releases distinctes", "Minimum distinct releases"),
              [1, 2, 3, 4, 5].map((n) => ({ id: String(n), label: String(n) })),
            )}
            {toggle(
              "PSE_PLAYABLE_SINGLE",
              L(
                "Afficher aussi les titres avec une seule release",
                "Also show titles with one release",
              ),
              L(
                "Assouplit le minimum sans garantir la lecture ; signalement selon le réglage existant.",
                "Relaxes the minimum without guaranteeing playback; uses the existing warning setting.",
              ),
            )}
            {select(
              "PSE_PLAYABLE_PENDING",
              L("Titres pas encore indexés", "Titles not yet indexed"),
              [
                { id: "hide", label: L("Masquer", "Hide") },
                { id: "show", label: L("Afficher pendant la préparation", "Show while preparing") },
              ],
            )}
          </>
        ) : null}
        {step === 3 ? (
          <>
            <h3 className="text-lg font-medium">
              {L("Comment découvrir vos contenus ?", "How would you like to discover content?")}
            </h3>
            <p>
              {L(
                "Une liste choisit les titres à afficher ; elle ne fournit pas nécessairement les médias.",
                "A list selects titles to display; it does not necessarily provide media.",
              )}
            </p>
            <div className="grid gap-3 sm:grid-cols-2">
              {discoveryOptions.map((o) => option("discovery", o.id, text(o.label), text(o.help)))}
            </div>
            {discoveryOptions
              .filter((o) => choices.discovery.includes(o.id))
              .map((o) => (
                <SettingsLink key={o.id} to={o.href}>
                  {text(o.label)}
                </SettingsLink>
              ))}
            {choices.discovery.includes("pre")
              ? toggle(
                  "PSE_PREDB",
                  L("Activer les annonces pre", "Enable pre announcements"),
                  L(
                    "Bêta : les annonces compteront dans l’index sans prouver la lecture. Examiner les flux avant activation.",
                    "Beta: announcements will count in the index without proving playback. Review feeds before enabling.",
                  ),
                )
              : null}
            {toggle(
              "PSE_INDEXER",
              L("Préparer les recherches en arrière-plan", "Prepare searches in the background"),
              L(
                "Réutilise les sources et budgets configurés. Ne télécharge pas les médias ; les addons peuvent effectuer leurs propres appels.",
                "Reuses configured sources and budgets. Does not download media; add-ons may perform their own calls.",
              ),
            )}
            <SettingsLink to="/remix/indexation">
              {L(
                "Sources d’indexation, cadence et budgets",
                "Indexing sources, schedule and budgets",
              )}
            </SettingsLink>
          </>
        ) : null}
        {step === 4 ? (
          <>
            <h3 className="text-lg font-medium">
              {L(
                "Comment souhaitez-vous lire et conserver ?",
                "How would you like to play and keep media?",
              )}
            </h3>
            <div className="grid gap-3 sm:grid-cols-2">
              {usageOptions.map((o) => option("usages", o.id, text(o.label), text(o.help)))}
            </div>
            <p>
              {L(
                "Préférer un mode n’autorise pas un nouveau téléchargement. Dans chaque connecteur, choisissez séparément s’il peut télécharger ce qui manque.",
                "Preferring a mode does not authorise a new download. Separately choose whether each connector may download missing media.",
              )}
            </p>
            <p className="text-sm text-fg-muted">
              {L(
                "L’ordre rapide est commun aux films, séries et animés. Les fichiers locaux et la musique ont leurs propres parcours. Les ordres avancés par contenu ne constituent pas un ordre universel entre tous les modes.",
                "Quick order is shared by movies, shows and anime. Local files and music use their own paths. Advanced per-content orders are not a universal order across all modes.",
              )}
            </p>
            <SettingsLink to="/remix/lecture?view=priorites">
              {L("Définir l’ordre et les secours autorisés", "Set ordering and allowed fallbacks")}
            </SettingsLink>
          </>
        ) : null}
        {step === 5 ? (
          <>
            <h3 className="text-lg font-medium">
              {L("Quels services voulez-vous utiliser ?", "Which services would you like to use?")}
            </h3>
            <p>
              {L(
                "Cochez les services à configurer ou à réutiliser. La sélection ne les active pas. Leurs éditeurs enregistrent séparément les connexions et permettent de les tester.",
                "Select services to configure or reuse. Selection does not enable them. Their editors save connections separately and allow testing.",
              )}
            </p>
            <div className="grid gap-3 sm:grid-cols-2">
              {activeSources.map((s) => (
                <div key={s.id} className="space-y-2">
                  {option("sources", s.id, text(s.label), text(s.help))}
                  {choices.sources.includes(s.id) ? (
                    <SettingsLink to={s.href}>
                      {L("Configurer / tester", "Configure / test")}
                    </SettingsLink>
                  ) : null}
                </div>
              ))}
            </div>
            {outOfScope.length ? (
              <p role="status">
                {L(
                  "Sélections conservées hors du parcours actuel : ",
                  "Selections preserved outside the current path: ",
                ) + outOfScope.map((s) => text(s.label)).join(", ")}
              </p>
            ) : null}
            {choices.sources.includes("usenet") || choices.sources.includes("torrent") ? (
              <SettingsLink to="/remix/sources?source=indexeurs">
                {L("Connecter aussi les indexeurs", "Also connect indexers")}
              </SettingsLink>
            ) : null}
            <SettingsLink to="/remix/diagnostic">
              {L("Vérifier les connexions et la lecture", "Check connections and playback")}
            </SettingsLink>
          </>
        ) : null}
        {step === 6 ? (
          <>
            <h3 className="text-lg font-medium">
              {L("Composez vos bibliothèques et votre accueil", "Build your libraries and home")}
            </h3>
            <div className="grid gap-3 sm:grid-cols-2">
              {option(
                "clients",
                "plex",
                "Plex",
                L(
                  "Accueil à prévisualiser par spectateur et appareil.",
                  "Preview home by viewer and device.",
                ),
              )}
              {option(
                "clients",
                "jellyfin",
                "Jellyfin",
                L(
                  "Bibliothèques compatibles ; certaines dispositions et tris restent propres au client.",
                  "Compatible libraries; some layouts and sorting remain client-specific.",
                ),
              )}
            </div>
            <p className="text-sm">
              {L(
                "Ce choix prépare les liens utiles ; il ne change pas les droits ni la visibilité par protocole. Les bibliothèques créées utilisent les clients actuellement compatibles.",
                "This choice prepares useful links; it does not change permissions or protocol visibility. Created libraries use currently supported clients.",
              )}
            </p>
            <SettingsLink to="/apps">
              {L("Activer et connecter les clients", "Enable and connect clients")}
            </SettingsLink>
            {choices.clients.includes("plex") ? (
              <SettingsLink to="/home">
                {L("Organiser et prévisualiser l’accueil Plex", "Arrange and preview Plex home")}
              </SettingsLink>
            ) : null}
            {choices.clients.includes("jellyfin") ? (
              <p>
                {L(
                  "Dans Jellyfin, vérifiez ensuite les bibliothèques et le tri dans l’application utilisée.",
                  "In Jellyfin, then check libraries and sorting in your chosen app.",
                )}
              </p>
            ) : null}
            {choices.contents.includes("music") ? (
              <SettingsLink to="/remix/contenus?content=musique">
                {L("Bibliothèque et playlists musicales", "Music library and playlists")}
              </SettingsLink>
            ) : null}
            <p className="rounded border border-border p-3 text-sm">
              {L(
                "Les boutons de création ci-dessous enregistrent immédiatement dans les éditeurs existants. L’examen final concerne uniquement les réglages globaux du questionnaire.",
                "The creation buttons below save immediately through existing editors. Final review only covers the questionnaire’s global settings.",
              )}
            </p>
            {choices.discovery.includes("jukebox") ? <JukeboxQuickSetup /> : null}
            <Button
              variant="outline"
              onClick={() => setEditLibraries(!editLibraries)}
              aria-expanded={editLibraries}
            >
              {L(
                "Configurer les bibliothèques et collections",
                "Configure libraries and collections",
              )}
            </Button>
            {editLibraries ? <ThemeLibraries /> : null}
            {choices.contents.includes("anime") ? (
              <SettingsLink to="/remix/contenus?content=animes">
                {L("Créer une bibliothèque d’animés", "Create an anime library")}
              </SettingsLink>
            ) : null}
            {select(
              "PSE_LIBRARY_SORT",
              L("Tri initial des bibliothèques", "Initial library sorting"),
              [
                { id: "release", label: L("Sorties récentes d’abord", "Newest releases first") },
                { id: "title", label: L("Titre A–Z", "Title A–Z") },
              ],
            )}
          </>
        ) : null}
        {step === 7 ? (
          <>
            <h3 className="text-lg font-medium">
              {L("Personnalisez les affiches", "Personalise posters")}
            </h3>
            <p>
              {L(
                "Conservez vos affiches ou choisissez un fournisseur et ses styles dans l’éditeur existant. La langue, les notes et les badges dépendent du fournisseur.",
                "Keep your artwork or choose a provider and its styles in the existing editor. Language, ratings and badges depend on the provider.",
              )}
            </p>
            <SettingsLink to="/remix/visuels?view=affiches">
              {L("Choisir les affiches et leur fournisseur", "Choose posters and their provider")}
            </SettingsLink>
            <SettingsLink to="/remix/visuels?view=cartes">
              {L("Configurer les cartes de découverte", "Configure discovery cards")}
            </SettingsLink>
            {select(
              "PSE_POSTER_REFRESH_HOURS",
              L("Renouvellement des affiches", "Poster refresh"),
              [6, 12, 24, 48, 168].map((n) => ({ id: String(n), label: n + " h" })),
            )}
            <p className="text-sm text-fg-muted">
              {L(
                "Les affiches de collections restent gérées dans leurs éditeurs. L’assistant ne remplace ni ne génère automatiquement vos images personnalisées.",
                "Collection artwork remains managed in its editors. The wizard does not replace or automatically generate your custom images.",
              )}
            </p>
          </>
        ) : null}
        {step === 8 ? (
          <>
            <h3 className="text-lg font-medium">
              {L("Vérifiez le résultat avant d’appliquer", "Review before applying")}
            </h3>
            <dl className="grid gap-2 sm:grid-cols-2">
              <div>
                <dt className="font-medium">{L("Contenus", "Content")}</dt>
                <dd>
                  {contentOptions
                    .filter((o) => choices.contents.includes(o.id))
                    .map((o) => text(o.label))
                    .join(", ") || L("Aucun sélectionné", "None selected")}
                </dd>
              </div>
              <div>
                <dt className="font-medium">{L("Modes de lecture", "Playback modes")}</dt>
                <dd>
                  {usageOptions
                    .filter((o) => choices.usages.includes(o.id))
                    .map((o) => text(o.label))
                    .join(", ") || L("Aucun sélectionné", "None selected")}
                </dd>
              </div>
            </dl>
            {selectedSources.length ? (
              <>
                <h4 className="font-medium">
                  {L("Connexions à vérifier séparément", "Connections to check separately")}
                </h4>
                <div className="flex flex-wrap gap-2">
                  {selectedSources.map((s) => (
                    <SettingsLink key={s.id} to={s.href}>
                      {text(s.label)}
                    </SettingsLink>
                  ))}
                </div>
              </>
            ) : null}
            <p className="text-sm text-fg-muted">
              {L(
                "Une sélection ou une adresse renseignée ne prouve pas une connexion ni une lecture réussie. Les préférences du questionnaire ne sont pas des règles de lecture supplémentaires.",
                "A selection or saved address does not prove a successful connection or playback. Questionnaire preferences are not additional playback rules.",
              )}
            </p>
            {warnings(plan?.warnings ?? state?.warnings ?? [])}
            {!plan ? (
              <>
                <ul>
                  {Object.keys(patch).map((k) => (
                    <li key={k}>
                      {label(k)} :{" "}
                      {patch[k] === null
                        ? L("Rétablir l’héritage", "Restore inheritance")
                        : displayValue(k, patch[k]!)}
                    </li>
                  ))}
                </ul>
                <Button onClick={() => void preview()} disabled={busy || loading || !state}>
                  {L("Prévisualiser les changements", "Preview changes")}
                </Button>
              </>
            ) : (
              <>
                <div className="overflow-x-auto">
                  <table className="w-full text-left text-sm">
                    <thead>
                      <tr>
                        <th>{L("Réglage", "Setting")}</th>
                        <th>{L("Avant", "Before")}</th>
                        <th>{L("Après", "After")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {plan.changes.map((c) => (
                        <tr key={c.key} className="border-t border-border">
                          <td className="p-2">{label(c.key)}</td>
                          <td className="p-2">{displayValue(c.key, c.before)}</td>
                          <td className="p-2">
                            {displayValue(c.key, c.after)}
                            {c.reset ? L(" (hérité)", " (inherited)") : ""}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {!plan.changes.length ? (
                  <p>{L("Aucun changement de réglage global.", "No global setting changes.")}</p>
                ) : (
                  <>
                    <label className="flex gap-2">
                      <input
                        type="checkbox"
                        checked={ack}
                        disabled={busy}
                        onChange={(e) => setAck(e.target.checked)}
                      />
                      {L(
                        "J’ai examiné les changements et les interactions ci-dessus.",
                        "I reviewed the changes and interactions above.",
                      )}
                    </label>
                    <Button disabled={busy || !ack || loading} onClick={() => void apply()}>
                      {L("Appliquer ces changements", "Apply these changes")}
                    </Button>
                  </>
                )}
              </>
            )}
            {undo ? (
              <Button
                variant="outline"
                disabled={busy || loading || !state}
                onClick={() => void preview(undo)}
              >
                {L(
                  "Prévisualiser le retour aux réglages précédents",
                  "Preview restoring previous settings",
                )}
              </Button>
            ) : null}
            <div className="flex flex-wrap gap-2">
              <SettingsLink to="/remix/diagnostic">
                {L("Diagnostic et essai de lecture", "Diagnostics and playback test")}
              </SettingsLink>
              <Link className="underline" to="/remix/accueil">
                {L("Revenir à l’accueil", "Back to start")}
              </Link>
            </div>
          </>
        ) : null}
      </div>
      <footer className="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4">
        <Button variant="outline" disabled={step === 1 || busy} onClick={() => go(step - 1)}>
          {L("Retour", "Back")}
        </Button>
        <span className="text-sm text-fg-muted">
          {step} / {STEPS.length} ·{" "}
          {L(
            "Réponses mémorisées dans ce navigateur ; modifications globales non enregistrées avant application.",
            "Answers remembered in this browser; global changes are unsaved until applied.",
          )}
        </span>
        {step < STEPS.length ? (
          <Button disabled={busy} onClick={() => go(step + 1)}>
            {L("Suivant", "Next")}
          </Button>
        ) : null}
      </footer>
    </section>
  );
}
