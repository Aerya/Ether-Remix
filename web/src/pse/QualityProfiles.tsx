import { Button, Checkbox, Input, Select, toast } from "@braedonsaunders/appkit-ui";
import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import type { ReleaseRule, ReleaseCondition } from "../lib/types";
import type { ThemeData } from "./ThemeLibraries";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const ROOT = "/pse/quality/";
type Profile = {
  id: string;
  name: string;
  scope: string;
  target?: string;
  replace?: boolean;
  rules: ReleaseRule[];
};
type Config = { mode?: "simple" | "profiles"; profiles: Profile[] };
type Imported = { rules: ReleaseRule[]; warnings: string[] };
const fields = [
  ["source", "Format / source", "Format / source"],
  ["remix_format", "Format détaillé AIOStreams", "AIOStreams detailed format"],
  ["remix_resolution", "Résolution ou inconnue", "Resolution or unknown"],
  ["remix_visual", "Tags vidéo détaillés", "Detailed visual tags"],
  ["remix_language", "Langue, originale ou inconnue", "Language, original or unknown"],
  ["remix_cache", "Cache debrid annoncé", "Declared debrid cache"],
  ["video", "Codec vidéo", "Video codec"],
  ["audio", "Codec audio", "Audio codec"],
  ["height", "Résolution", "Resolution"],
  ["hdr", "HDR / Dolby Vision", "HDR / Dolby Vision"],
  ["size_gib", "Taille (Gio)", "Size (GiB)"],
  ["language", "Langue audio", "Audio language"],
  ["subtitles", "Sous-titres", "Subtitles"],
  ["name", "Nom de release / regex", "Release name / regex"],
  ["group", "Groupe", "Release group"],
  ["edition", "Édition", "Edition"],
  ["protocol", "Protocole", "Protocol"],
  ["indexer", "Source / indexeur", "Source / indexer"],
];
const choices: Record<string, string[]> = {
  remix_format: [
    "BluRay REMUX",
    "BluRay",
    "WEB-DL",
    "WEBRip",
    "HDRip",
    "HC HD-Rip",
    "DVD REMUX",
    "DVDRip",
    "HDTV",
    "CAM",
    "TS",
    "TC",
    "SCR",
    "Unknown",
  ],
  remix_resolution: [
    "2160p",
    "1440p",
    "1080p",
    "720p",
    "576p",
    "480p",
    "360p",
    "240p",
    "144p",
    "Unknown",
  ],
  remix_visual: [
    "HDR+DV",
    "DV Only",
    "HDR Only",
    "HDR10+",
    "HDR10",
    "DV",
    "HDR",
    "HLG",
    "10bit",
    "3D",
    "IMAX",
    "AI",
    "Upscaled",
    "SDR",
    "H-OU",
    "H-SBS",
    "Unknown",
  ],
  remix_language: ["French", "Multi", "English", "Original", "Unknown"],
  remix_cache: ["cached", "uncached", "unknown"],
  source: ["webdl", "webrip", "bluray", "remux", "hdtv", "dvd", "cam", "screener"],
  video: ["h264", "hevc", "av1", "vp9", "mpeg2video"],
  audio: ["aac", "ac3", "eac3", "truehd", "dts", "flac", "opus", "mp3"],
  hdr: ["hdr10", "dv"],
  language: ["french", "english", "multi", "japanese", "german", "spanish", "italian"],
  subtitles: ["french", "english"],
  protocol: ["torrent", "usenet", "http"],
};
const presets = [
  ["1080p–4K", "height", "", 1080, 2160],
  ["HEVC / AV1", "video", "hevc,av1", 0, 0],
  ["WEB-DL / Remux", "source", "webdl,remux", 0, 0],
  ["Dolby Vision", "hdr", "dv", 0, 0],
  ["HDR10+", "name", "(?i)\\bhdr10(?:\\+|plus)", 0, 0],
  ["Dolby Atmos", "name", "(?i)\\batmos\\b", 0, 0],
  ["5.1 / 7.1", "name", "(?i)\\b(?:5[ .]1|7[ .]1)\\b", 0, 0],
] as const;
const uid = () => crypto.randomUUID();
function condition(field = "video"): ReleaseCondition {
  return {
    field,
    operator: ["height", "size_gib"].includes(field) ? "range" : "equals",
    value: "",
  };
}

export function QualityProfiles({ onMode }: { onMode?: (active: boolean) => void }) {
  const doc = useAsync(
    () => Promise.all([apiFetch<Config>(ROOT), apiFetch<ThemeData>("/pse/themes/")]),
    [],
  );
  const [baseline, setBaseline] = useState<Config | null>(null);
  const [draft, setDraft] = useState<Config | null>(null);
  const [selected, setSelected] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const sources = useAsync(() => apiFetch<{ id: string; name: string }[]>(ROOT + "manifest"), []);
  const [sourceId, setSourceId] = useState("");
  const [manifest, setManifest] = useState("");
  const [password, setPassword] = useState("");
  const [raw, setRaw] = useState("");
  const [imported, setImported] = useState<Imported | null>(null);
  const [names, setNames] = useState("");
  const [originalLanguage, setOriginalLanguage] = useState("");
  const [preview, setPreview] = useState<{ name: string; result: { rejected: boolean } }[] | null>(
    null,
  );
  const persisted = baseline ?? doc.data?.[0];
  useEffect(() => {
    if (persisted)
      onMode?.(persisted.mode === "profiles" || (!persisted.mode && persisted.profiles.length > 0));
  }, [persisted, onMode]);
  if (doc.error) return <p role="alert">{String(doc.error)}</p>;
  if (!doc.data) return <p>{L("Chargement des profils…", "Loading profiles…")}</p>;
  const saved = baseline ?? doc.data[0],
    themes = doc.data[1];
  const config = draft ?? saved;
  const active = config.profiles.find((p) => p.id === selected) ?? config.profiles[0];
  const edit = (next: Config) => {
    setDraft(next);
    setPreview(null);
    setError("");
  };
  const patch = (change: Partial<Profile>) => {
    if (active)
      edit({
        ...config,
        profiles: config.profiles.map((p) => (p.id === active.id ? { ...p, ...change } : p)),
      });
  };
  const rulePatch = (id: string, change: Partial<ReleaseRule>) =>
    active && patch({ rules: active.rules.map((r) => (r.id === id ? { ...r, ...change } : r)) });
  const addRule = (field = "video", value = "", min = 0, max = 0) => {
    if (!active) return;
    const cs = value.includes(",")
      ? value.split(",").map((v) => ({ ...condition(field), value: v }))
      : [
          {
            ...condition(field),
            value,
            min,
            max,
            operator: field === "name" ? "regex" : condition(field).operator,
          },
        ];
    patch({
      rules: [
        ...active.rules,
        {
          id: uid(),
          name: fields.find((f) => f[0] === field)?.[1] ?? field,
          action: "require",
          match: "any",
          conditions: cs,
        },
      ],
    });
  };
  const run = async (fn: () => Promise<void>) => {
    setBusy(true);
    setError("");
    try {
      await fn();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="space-y-4 rounded-xl border border-line p-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="font-semibold">
            {L("Profils de qualité des releases", "Release quality profiles")}
          </h2>
          <p className="max-w-4xl text-sm text-fg-muted">
            {L(
              "Filtre les releases retenues pour le catalogue et la lecture. Les sources sont interrogées normalement : un filtre ne peut pas récupérer ce qu’un addon a déjà masqué. Les contraintes de lecture Ether et du lecteur restent applicables.",
              "Filters releases accepted for the catalogue and playback. Sources are queried normally: filters cannot recover releases already hidden by an addon. Ether playback profiles and player limits still apply.",
            )}
          </p>
        </div>
        <Button
          onClick={() => {
            const p: Profile = {
              id: uid(),
              name: L("Nouveau profil", "New profile"),
              scope: "global",
              rules: [],
            };
            edit({ ...config, profiles: [...config.profiles, p] });
            setSelected(p.id);
          }}
        >
          <Plus className="size-4" />
          {L("Ajouter un profil", "Add profile")}
        </Button>
      </div>
      <p className="text-xs text-fg-muted">
        {L(
          "Priorité : collection → bibliothèque → Films/Séries → global. À portée égale, le premier profil correspondant gagne. Les filtres globaux s’ajoutent, sauf remplacement explicite. Un titre partagé reçoit le même filtre dans tous les clients.",
          "Priority: collection → library → Movies/Shows → global. Within the same scope, the first matching profile wins. Global filters also apply unless explicitly replaced. A shared title uses the same filter in every client.",
        )}
      </p>
      <label className="flex items-center gap-3 font-medium">
        <Checkbox
          checked={config.mode === "profiles" || (!config.mode && config.profiles.length > 0)}
          onChange={(e) =>
            edit({ ...config, mode: e.currentTarget.checked ? "profiles" : "simple" })
          }
        />
        {L(
          "Utiliser les profils à la place des critères simples",
          "Use profiles instead of simple criteria",
        )}
      </label>
      <p className="text-sm text-fg-muted">
        {L(
          "Après enregistrement, ce mode remplace tous les critères des releases de « Catalogue lisible uniquement ». Le seuil minimum de releases reste actif. Sans profil correspondant, aucune restriction supplémentaire. Désactivez ce mode pour retrouver vos critères simples conservés.",
          "After saving, this mode replaces all release criteria in Playable-only catalogue. The minimum release count stays active. Without a matching profile there is no additional restriction. Disable this mode to restore your saved simple criteria.",
        )}
      </p>
      <div className="flex flex-wrap gap-2">
        {config.profiles.map((p) => (
          <Button
            key={p.id}
            variant={p.id === active?.id ? "default" : "outline"}
            onClick={() => {
              setSelected(p.id);
              setImported(null);
              setPreview(null);
            }}
          >
            {p.name}
          </Button>
        ))}
      </div>
      {active ? (
        <div className="space-y-4 border-t border-line pt-4">
          <div className="flex flex-wrap gap-2">
            <Input
              aria-label={L("Nom du profil", "Profile name")}
              value={active.name}
              maxLength={160}
              className="w-64"
              onChange={(e) => patch({ name: e.target.value })}
            />
            <Select
              aria-label={L("Portée du profil", "Profile scope")}
              value={active.scope}
              onChange={(e) => patch({ scope: e.target.value, target: "" })}
              className="w-48"
            >
              {[
                ["global", "Global", "Global"],
                ["movie", "Films", "Movies"],
                ["show", "Séries", "Shows"],
                ["library", "Bibliothèque", "Library"],
                ["collection", "Collection", "Collection"],
              ].map(([v, f, e]) => (
                <option key={v} value={v}>
                  {L(f!, e!)}
                </option>
              ))}
            </Select>
            {["library", "collection"].includes(active.scope) ? (
              <Select
                className="w-72"
                aria-label={L("Cible du profil", "Profile target")}
                value={active.target ?? ""}
                onChange={(e) => patch({ target: e.target.value })}
              >
                <option value="">{L("Sélectionner…", "Select…")}</option>
                {(active.scope === "library" ? themes.libraries : themes.collections).map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </Select>
            ) : null}
            {[
              [-1, ArrowUp],
              [1, ArrowDown],
            ].map(([direction, Icon]) => {
              const D = Icon as typeof ArrowUp;
              const n = direction as number;
              return (
                <Button
                  key={n}
                  variant="outline"
                  aria-label={
                    n < 0
                      ? L("Monter le profil", "Move profile up")
                      : L("Descendre le profil", "Move profile down")
                  }
                  onClick={() => {
                    const ps = [...config.profiles],
                      i = ps.findIndex((p) => p.id === active.id),
                      j = i + n;
                    if (j < 0 || j >= ps.length) return;
                    [ps[i], ps[j]] = [ps[j]!, ps[i]!];
                    edit({ ...config, profiles: ps });
                  }}
                >
                  <D className="size-4" />
                </Button>
              );
            })}
            <Button
              variant="outline"
              onClick={() =>
                edit({ ...config, profiles: config.profiles.filter((p) => p.id !== active.id) })
              }
            >
              <Trash2 className="size-4" />
              {L("Retirer", "Remove")}
            </Button>
          </div>
          {active.scope !== "global" ? (
            <label className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={!!active.replace}
                onChange={(e) => patch({ replace: e.currentTarget.checked })}
              />
              {L(
                "Remplacer les filtres globaux pour cette sélection",
                "Replace global filters for this selection",
              )}
            </label>
          ) : null}
          <p className="text-xs text-fg-muted">
            {L(
              "Imposer : au moins une valeur doit être annoncée. Exclure : rejette seulement les valeurs annoncées. Aucune règle : aucune restriction supplémentaire. Les informations viennent des noms et métadonnées des sources.",
              "Require: at least one value must be declared. Exclude: only rejects declared values. No rules means no additional restriction. Facts come from source names and metadata.",
            )}
          </p>
          <div className="flex flex-wrap gap-2">
            {presets.map(([label, field, value, min, max]) => (
              <Button
                key={label}
                variant="outline"
                size="sm"
                onClick={() => addRule(field, value, min, max)}
              >
                {label}
              </Button>
            ))}
          </div>
          {active.rules.map((r) => (
            <div key={r.id} className="space-y-2 rounded-lg border border-line p-3">
              <div className="flex flex-wrap gap-2">
                <Input
                  aria-label={L("Nom de règle", "Rule name")}
                  value={r.name}
                  className="w-56"
                  onChange={(e) => rulePatch(r.id, { name: e.target.value })}
                />
                <Select
                  aria-label={L("Action", "Action")}
                  value={r.action}
                  className="w-36"
                  onChange={(e) =>
                    rulePatch(r.id, { action: e.target.value as ReleaseRule["action"] })
                  }
                >
                  <option value="require">{L("Imposer", "Require")}</option>
                  <option value="avoid">{L("Exclure", "Exclude")}</option>
                </Select>
                <Select
                  aria-label={L("Combinaison", "Combination")}
                  value={r.match ?? "any"}
                  className="w-48"
                  onChange={(e) => rulePatch(r.id, { match: e.target.value as "any" | "all" })}
                >
                  <option value="any">{L("Au moins une (OU)", "Any (OR)")}</option>
                  <option value="all">{L("Toutes (ET)", "All (AND)")}</option>
                </Select>
                <Button
                  variant="outline"
                  aria-label={L("Retirer la règle", "Remove rule")}
                  onClick={() => patch({ rules: active.rules.filter((x) => x.id !== r.id) })}
                >
                  <Trash2 className="size-4" />
                </Button>
              </div>
              {r.conditions.map((c, i) => {
                const change = (v: ReleaseCondition) =>
                  rulePatch(r.id, { conditions: r.conditions.map((x, j) => (j === i ? v : x)) });
                const numeric = ["height", "size_gib"].includes(c.field);
                return (
                  <div key={i} className="flex flex-wrap gap-2">
                    <Select
                      className="w-52"
                      aria-label={L("Critère", "Criterion")}
                      value={c.field}
                      onChange={(e) => change(condition(e.target.value))}
                    >
                      {fields.map(([v, f, e]) => (
                        <option key={v} value={v}>
                          {L(f!, e!)}
                        </option>
                      ))}
                    </Select>
                    {numeric ? (
                      <>
                        <Input
                          type="number"
                          min={0}
                          aria-label="Minimum"
                          className="w-32"
                          value={c.min ?? 0}
                          onChange={(e) => change({ ...c, min: Number(e.target.value) })}
                        />
                        <Input
                          type="number"
                          min={0}
                          aria-label="Maximum"
                          className="w-32"
                          value={c.max ?? 0}
                          onChange={(e) => change({ ...c, max: Number(e.target.value) })}
                        />
                        <span className="self-center text-xs">
                          {L("Maximum 0 : sans limite", "Maximum 0: unlimited")}
                        </span>
                      </>
                    ) : (
                      <>
                        <Select
                          aria-label={L("Comparaison", "Comparison")}
                          className="w-36"
                          value={c.operator}
                          onChange={(e) =>
                            change({
                              ...c,
                              operator: e.target.value as ReleaseCondition["operator"],
                            })
                          }
                        >
                          <option value="equals">{L("Égal à", "Equals")}</option>
                          <option
                            disabled={
                              ["language", "subtitles"].includes(c.field) ||
                              c.field.startsWith("remix_")
                            }
                            value="contains"
                          >
                            {L("Contient", "Contains")}
                          </option>
                          <option
                            disabled={
                              ["language", "subtitles"].includes(c.field) ||
                              c.field.startsWith("remix_")
                            }
                            value="regex"
                          >
                            Regex
                          </option>
                        </Select>
                        <Input
                          className="min-w-52 flex-1"
                          aria-label={L("Valeur ou regex", "Value or regex")}
                          list={`quality-${c.field}`}
                          value={c.value ?? ""}
                          onChange={(e) => change({ ...c, value: e.target.value })}
                        />
                      </>
                    )}
                    <Button
                      variant="outline"
                      disabled={r.conditions.length === 1}
                      aria-label={L("Retirer le critère", "Remove criterion")}
                      onClick={() =>
                        rulePatch(r.id, { conditions: r.conditions.filter((_, j) => j !== i) })
                      }
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
                );
              })}
              <Button
                variant="outline"
                size="sm"
                onClick={() => rulePatch(r.id, { conditions: [...r.conditions, condition()] })}
              >
                {L("Ajouter une valeur / condition", "Add value / condition")}
              </Button>
            </div>
          ))}
          {Object.entries(choices).map(([field, values]) => (
            <datalist key={field} id={`quality-${field}`}>
              {values.map((v) => (
                <option key={v} value={v} />
              ))}
            </datalist>
          ))}
          <Button variant="outline" onClick={() => addRule()}>
            <Plus className="size-4" />
            {L("Ajouter une règle", "Add rule")}
          </Button>
          <details className="rounded-lg border border-line p-3">
            <summary className="cursor-pointer font-medium">
              {L("Importer les filtres AIOStreams", "Import AIOStreams filters")}
            </summary>
            <div className="mt-3 space-y-3">
              <p className="text-sm text-fg-muted">
                {L(
                  "Import JSON déchiffré ci-dessous, ou import par manifest avec le formulaire dédié. Seuls les filtres compatibles sont extraits ; les comptes et clés ne sont pas enregistrés. Les règles incluses (contournement), préférées et distantes sont signalées sans être appliquées.",
                  "Use a decrypted JSON export below, or the dedicated manifest import form. Only compatible filters are extracted; accounts and keys are not saved. Included (bypass), preferred and remote rules are reported without applying them.",
                )}
              </p>
              <div className="space-y-2 rounded border border-line p-3">
                <h3 className="font-medium">
                  {L(
                    "Depuis une source / un manifest AIOStreams",
                    "From an AIOStreams source / manifest",
                  )}
                </h3>
                <Select
                  aria-label="Source AIOStreams"
                  value={sourceId}
                  onChange={(e) => setSourceId(e.target.value)}
                >
                  <option value="">{L("Coller un manifest", "Paste a manifest")}</option>
                  {sources.data?.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </Select>
                {!sourceId ? (
                  <Input
                    type="password"
                    aria-label="Manifest AIOStreams"
                    value={manifest}
                    onChange={(e) => setManifest(e.target.value)}
                    placeholder="https://…/manifest.json"
                  />
                ) : null}
                <Input
                  type="password"
                  autoComplete="off"
                  aria-label={L(
                    "Mot de passe de configuration AIOStreams",
                    "AIOStreams configuration password",
                  )}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder={L(
                    "Mot de passe de configuration AIOStreams",
                    "AIOStreams configuration password",
                  )}
                />
                <p className="text-xs text-fg-muted">
                  {L(
                    "Le manifest contient un accès chiffré. Le mot de passe sert uniquement à cet import et n’est pas enregistré. Les variantes sont signalées : leurs instructions ne sont pas reproduites.",
                    "The manifest contains encrypted access. The password is only used for this import and is not saved. Variants are flagged: their instructions are not reproduced.",
                  )}
                </p>
                <Button
                  disabled={busy || !password || (!sourceId && !manifest)}
                  onClick={() =>
                    run(async () => {
                      try {
                        setImported(
                          await apiFetch<Imported>(ROOT + "manifest", {
                            method: "POST",
                            body: JSON.stringify({ sourceId, manifest, password }),
                          }),
                        );
                      } finally {
                        setPassword("");
                      }
                    })
                  }
                >
                  {L("Récupérer et analyser les filtres", "Fetch and preview filters")}
                </Button>
              </div>
              <input
                type="file"
                accept=".json,application/json"
                aria-label={L("Export AIOStreams", "AIOStreams export")}
                onChange={async (e) => {
                  const f = e.target.files?.[0];
                  if (f) {
                    if (f.size > 2 * 1024 * 1024) {
                      setError(L("Fichier limité à 2 Mio", "File limit: 2 MiB"));
                      return;
                    }
                    setRaw(await f.text());
                    setImported(null);
                  }
                }}
              />
              <textarea
                className="min-h-28 w-full rounded border border-line bg-transparent p-2 font-mono text-xs"
                aria-label="JSON AIOStreams"
                value={raw}
                onChange={(e) => {
                  setRaw(e.target.value);
                  setImported(null);
                }}
              />
              <Button
                disabled={busy || !raw.trim()}
                onClick={() =>
                  run(async () => {
                    setImported(
                      await apiFetch<Imported>(ROOT + "import", { method: "POST", body: raw }),
                    );
                    setRaw("");
                  })
                }
              >
                {L("Analyser l’import", "Preview import")}
              </Button>
              {imported ? (
                <div className="space-y-2">
                  <p>
                    {imported.rules.length} {L("règles compatibles", "compatible rules")}
                  </p>
                  {imported.warnings.map((w, i) => (
                    <p className="text-sm text-amber-600" key={i}>
                      {w}
                    </p>
                  ))}
                  <pre className="max-h-64 overflow-auto text-xs">
                    {JSON.stringify(imported.rules, null, 2)}
                  </pre>
                  <Button
                    disabled={!imported.rules.length}
                    onClick={() => {
                      patch({
                        rules: [
                          ...active.rules,
                          ...imported.rules.map((r) => ({ ...r, id: uid() })),
                        ],
                      });
                      setImported(null);
                    }}
                  >
                    {L("Ajouter au brouillon modifiable", "Add to editable draft")}
                  </Button>
                </div>
              ) : null}
            </div>
          </details>
          <details className="rounded-lg border border-line p-3">
            <summary className="cursor-pointer font-medium">
              {L("Tester des noms de releases", "Test release names")}
            </summary>
            <div className="mt-3 space-y-2">
              <p className="text-xs text-fg-muted">
                {L(
                  "Test du profil affiché, sans recherche ni enregistrement. Un nom par ligne. Regex RE2 : pas de lookaround ni de références arrière ; (?i) ignore la casse.",
                  "Tests this profile without searching or saving. One name per line. RE2 regex: no lookaround or backreferences; (?i) ignores case.",
                )}
              </p>
              <Input
                aria-label={L(
                  "Langue originale du titre pour le test",
                  "Original title language for preview",
                )}
                placeholder={L(
                  "Langue originale du titre : fr, en, ja…",
                  "Original title language: fr, en, ja…",
                )}
                value={originalLanguage}
                onChange={(e) => setOriginalLanguage(e.target.value)}
              />
              <textarea
                className="min-h-24 w-full rounded border border-line bg-transparent p-2"
                value={names}
                aria-label={L("Noms de releases", "Release names")}
                onChange={(e) => setNames(e.target.value)}
              />
              <Button
                disabled={busy}
                onClick={() =>
                  run(async () =>
                    setPreview(
                      await apiFetch(ROOT + "preview", {
                        method: "POST",
                        body: JSON.stringify({
                          profile: active,
                          names: names.split("\n").filter(Boolean),
                          originalLanguage,
                        }),
                      }),
                    ),
                  )
                }
              >
                {L("Tester", "Test")}
              </Button>
              {preview?.map((p, i) => (
                <p key={i} className={p.result.rejected ? "text-red-500" : "text-emerald-600"}>
                  {p.result.rejected ? L("Refusée", "Rejected") : L("Acceptée", "Accepted")} ·{" "}
                  {p.name}
                </p>
              ))}
            </div>
          </details>
        </div>
      ) : (
        <p className="text-sm text-fg-muted">
          {L(
            "Aucun profil supplémentaire. Les réglages existants sont conservés.",
            "No additional profile. Existing settings are preserved.",
          )}
        </p>
      )}
      {error ? (
        <p role="alert" className="text-red-500">
          {error}
        </p>
      ) : null}
      {draft ? (
        <div className="flex gap-2">
          <Button
            disabled={busy}
            onClick={() =>
              run(async () => {
                const c = await apiFetch<Config>(ROOT, {
                  method: "PUT",
                  body: JSON.stringify(config),
                });
                setBaseline(c);
                setDraft(null);
                toast.success(
                  L(
                    "Profils enregistrés. Le catalogue est recalculé en arrière-plan.",
                    "Profiles saved. The catalogue is recalculated in the background.",
                  ),
                );
              })
            }
          >
            {L("Enregistrer les profils", "Save profiles")}
          </Button>
          <Button
            variant="outline"
            onClick={() => {
              setDraft(null);
              setImported(null);
              setPreview(null);
            }}
          >
            {L("Annuler les modifications", "Discard changes")}
          </Button>
        </div>
      ) : null}
    </section>
  );
}
