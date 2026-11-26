import { useId } from "react";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export type StremioFormat = "direct" | "compact" | "detaille";
export type StremioOptions = { format: StremioFormat; count: number };
export const DEFAULT_STREMIO_OPTIONS: StremioOptions = { format: "direct", count: 5 };

export type StremioLinks = {
  surface: string;
  url: string;
  qr: string;
  filesUrl?: string;
  filesQr?: string;
};

// Segment d'options du lien (« compact-5 »), absent pour l'affichage direct.
export function stremioOptionsSegment(options: StremioOptions): string | null {
  if (options.format === "direct") return null;
  const count = Math.min(10, Math.max(1, Math.round(options.count) || 1));
  return `${options.format}-${count}`;
}

// Lien et QR code à afficher selon le choix : le lien « fichiers seuls » n'existe
// que si le serveur l'a fourni ; l'affichage et le nombre s'ajoutent à l'URL
// (…/o/<format>-<n>/manifest.json), le QR code correspondant vient du serveur.
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
  const key = `${files ? "filesQr" : "qr"}:${segment}`;
  const qr = (connection as Record<string, unknown>)[key];
  return {
    ...connection,
    url: base.url.replace(/\/manifest\.json$/, `/o/${segment}/manifest.json`),
    qr: typeof qr === "string" ? qr : "",
  };
}

const SAMPLE_COMPACT = "WEB-DL · DD+ 5.1 · 8,1 Go · Director's Cut";
const SAMPLE_DETAILED = [
  "2160p • BluRay REMUX • HDR10",
  "🌐 FR, EN • 💾 8,1 Go",
  "🎞 HEVC • 🔊 DD+ 5.1 • 👥 FLUX",
  "📁 Film.2160p.REMUX-FLUX.mkv",
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
            <label className="flex items-center gap-2 text-sm">
              <span className="font-medium">{L("Nombre de versions", "Number of versions")}</span>
              <input
                type="number"
                min={1}
                max={10}
                value={options.count}
                onChange={(event) =>
                  onOptionsChange({
                    ...options,
                    count: Math.min(10, Math.max(1, Math.round(Number(event.target.value)) || 1)),
                  })
                }
                className="w-20 rounded-md border border-border bg-transparent px-2 py-1"
              />
            </label>
          )}
        </fieldset>
      )}
    </div>
  );
}
