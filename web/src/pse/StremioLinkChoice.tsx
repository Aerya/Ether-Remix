import { useId } from "react";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export type StremioLinks = {
  surface: string;
  url: string;
  qr: string;
  filesUrl?: string;
  filesQr?: string;
};

// Lien et QR code à afficher selon le choix : le lien « fichiers seuls » n'existe
// que si le serveur l'a fourni.
export function chosenStremioLink<T extends StremioLinks>(connection: T, filesOnly: boolean): T {
  if (!filesOnly || connection.surface !== "stremio" || !connection.filesUrl) return connection;
  return { ...connection, url: connection.filesUrl, qr: connection.filesQr ?? connection.qr };
}

// Deux liens Stremio pour un même profil : avec les catalogues de ses
// bibliothèques, ou uniquement les fichiers (flux de lecture).
export function StremioLinkChoice({
  connection,
  filesOnly,
  onChange,
}: {
  connection: StremioLinks;
  filesOnly: boolean;
  onChange: (filesOnly: boolean) => void;
}) {
  const name = useId();
  if (connection.surface !== "stremio" || !connection.filesUrl) return null;
  const options: [boolean, string, string][] = [
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
  return (
    <fieldset className="min-w-0 space-y-2">
      <legend className="text-sm font-medium">{L("Type de lien", "Link type")}</legend>
      {options.map(([value, label, help]) => (
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
  );
}
