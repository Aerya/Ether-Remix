import { useId } from "react";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export type StremioFormat = "direct" | "compact" | "detaille";
export type StremioQuotas = { q2160: number; q1080: number; q720: number };
export type StremioOptions = { format: StremioFormat } & StremioQuotas;
export const DEFAULT_STREMIO_QUOTAS: StremioQuotas = { q2160: 2, q1080: 3, q720: 1 };
export const DEFAULT_STREMIO_OPTIONS: StremioOptions = {
  format: "direct",
  ...DEFAULT_STREMIO_QUOTAS,
};

export type StremioLinks = {
  surface: string;
  url: string;
  qr: string;
  filesUrl?: string;
  filesQr?: string;
};

const clampQuota = (n: number) => Math.min(5, Math.max(0, Math.round(n) || 0));

// Segment d'options du lien (« compact-2160.2-1080.3-720.1 »), absent pour
// l'affichage direct. Un nombre par qualité, de 0 (aucune) à 5.
export function stremioOptionsSegment(options: StremioOptions): string | null {
  if (options.format === "direct") return null;
  return `${options.format}-2160.${clampQuota(options.q2160)}-1080.${clampQuota(options.q1080)}-720.${clampQuota(options.q720)}`;
}

const DEFAULT_SEGMENT = (format: string) =>
  `${format}-2160.${DEFAULT_STREMIO_QUOTAS.q2160}-1080.${DEFAULT_STREMIO_QUOTAS.q1080}-720.${DEFAULT_STREMIO_QUOTAS.q720}`;

// Lien et QR code à afficher selon le choix : le lien « fichiers seuls » n'existe
// que si le serveur l'a fourni ; l'affichage et les quotas s'ajoutent à l'URL
// (…/o/<format>-<quotas>/manifest.json). Le serveur ne fournit le QR code que
// pour les quotas par défaut : sinon il est masqué (le lien se copie).
export function chosenStremioLink<T extends StremioLinks>(
  connection: T,
  filesOnly: boolean,
  options: StremioOptions = DEFAULT_STREMIO_OPTIONS,
): T {
  if (connection.surface !== "stremio") return connection;
  const files = filesOnly && Boolean(connection.filesUrl);
  const base = files
    ? { url: connection.filesUrl!, qr: connection.filesQr ?? connection.qr }
    : connection;
  const segment = stremioOptionsSegment(options);
  if (!segment) return files ? { ...connection, url: base.url, qr: base.qr } : connection;
  const key = `${files ? "filesQr" : "qr"}:${options.format}`;
  const qr = (connection as Record<string, unknown>)[key];
  return {
    ...connection,
    url: base.url.replace(/\/manifest\.json$/, `/o/${segment}/manifest.json`),
    qr: segment === DEFAULT_SEGMENT(options.format) && typeof qr === "string" ? qr : "",
  };
}

const SAMPLE_COMPACT = "2160p · REMUX · DV · EAC3 5.1 · 8,0 Go · FLUX";
const SAMPLE_DETAILED = [
  "[AD⚡] Ether",
  "2160p • REMUX • DV • 📶 9,5 Mb/s",
  "🇫🇷🇬🇧 • 💾 8,0 Go • 🔎 Alkolites",
  "🎧 HEVC • EAC3 5.1 • FLUX",
  "📁 Film.2160p.REMUX.DV.MULTI-FLUX.mkv",
].join("\n");

// Deux liens Stremio pour un même profil : avec les catalogues de ses
// bibliothèques, ou uniquement les fichiers (flux de lecture).
export function StremioLinkChoice({
  connection,
  filesOnly,
  onChange,
  options = DEFAULT_STREMIO_OPTIONS,
  onOptionsChange,
}: {
  connection: StremioLinks;
  filesOnly: boolean;
  onChange: (filesOnly: boolean) => void;
  options?: StremioOptions;
  onOptionsChange?: (options: StremioOptions) => void;
}) {
  const name = useId();
  const formatName = useId();
  if (connection.surface !== "stremio" || !connection.filesUrl) return null;
  const linkTypes: [boolean, string, string][] = [
    [
      false,
      L("Avec les catalogues du profil", "With the profile's catalogues"),
      L(
        "Films, séries et rangées des bibliothèques de ce profil, en plus de la lecture.",
        "Movies, series and rows from this profile's libraries, plus playback.",
      ),
    ],
    [
      true,
      L("Uniquement les fichiers", "Files only"),
      L(
        "Aucun catalogue : Ether répond seulement à « Lire » depuis vos autres catalogues.",
        "No catalogue: Ether only answers “Play” from your other catalogues.",
      ),
    ],
  ];
  const formats: [StremioFormat, string, string, string][] = [
    [
      "direct",
      L("Direct", "Direct"),
      L(
        "Un seul flux : la version choisie par Ether.",
        "A single stream: the version Ether picks.",
      ),
      "",
    ],
    [
      "compact",
      L("Compact", "Compact"),
      L("Une ligne par version.", "One line per version."),
      SAMPLE_COMPACT,
    ],
    [
      "detaille",
      L("Détaillé", "Detailed"),
      L("Plusieurs lignes par version.", "Several lines per version."),
      SAMPLE_DETAILED,
    ],
  ];
  return (
    <div className="min-w-0 space-y-4">
      <fieldset className="min-w-0 space-y-2">
        <legend className="text-sm font-medium">{L("Type de lien", "Link type")}</legend>
        {linkTypes.map(([value, label, help]) => (
          <label key={String(value)} className="flex cursor-pointer items-start gap-2 text-sm">
            <input
              type="radio"
              name={name}
              checked={filesOnly === value}
              onChange={() => onChange(value)}
              className="mt-1"
            />
            <span className="min-w-0">
              <span className="block font-medium">{label}</span>
              <span className="block text-fg-muted">{help}</span>
            </span>
          </label>
        ))}
      </fieldset>
      {onOptionsChange && (
        <fieldset className="min-w-0 space-y-2">
          <legend className="text-sm font-medium">{L("Affichage", "Display")}</legend>
          {formats.map(([value, label, help, sample]) => (
            <label key={value} className="flex cursor-pointer items-start gap-2 text-sm">
              <input
                type="radio"
                name={formatName}
                checked={options.format === value}
                onChange={() => onOptionsChange({ ...options, format: value })}
                className="mt-1"
              />
              <span className="min-w-0">
                <span className="block font-medium">{label}</span>
                <span className="block text-fg-muted">{help}</span>
                {sample && (
                  <pre className="mt-1 overflow-x-auto whitespace-pre-wrap rounded-md border border-border bg-surface-muted p-2 text-xs">
                    {sample}
                  </pre>
                )}
              </span>
            </label>
          ))}
          {options.format !== "direct" && (
            <div className="space-y-1 text-sm">
              <span className="block font-medium">
                {L("Versions par qualité (0 = aucune)", "Versions per quality (0 = none)")}
              </span>
              <span className="flex flex-wrap gap-3">
                {(
                  [
                    ["q2160", "4K"],
                    ["q1080", "1080p"],
                    ["q720", "720p"],
                  ] as const
                ).map(([key, label]) => (
                  <label key={key} className="flex items-center gap-1">
                    <span>{label}</span>
                    <input
                      type="number"
                      min={0}
                      max={5}
                      aria-label={label}
                      value={options[key]}
                      onChange={(event) =>
                        onOptionsChange({
                          ...options,
                          [key]: clampQuota(Number(event.target.value)),
                        })
                      }
                      className="w-16 rounded-md border border-border bg-transparent px-2 py-1"
                    />
                  </label>
                ))}
              </span>
              <span className="block text-fg-muted">
                {L(
                  "Le flux Auto vient toujours en premier, en plus de ces versions.",
                  "The Auto stream always comes first, on top of these versions.",
                )}
              </span>
            </div>
          )}
        </fieldset>
      )}
    </div>
  );
}
