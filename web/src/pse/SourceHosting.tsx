import { getLocale } from "../i18n";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

// Liens des projets eux-mêmes ; ne pas déduire un dépôt depuis un nom d’instance.
const SELF_HOST: Record<string, string> = {
  PSE_MUSIC_ULTRAMAX_URL: "https://github.com/PaRaN01a-hash/ultramax-music",
  PSE_MUSIC_HIFI_URL: "https://github.com/binimum/hifi-api",
};
export function SourceHosting({
  source,
  publicInstance = false,
}: {
  source: string;
  publicInstance?: boolean;
}) {
  const repo = SELF_HOST[source];
  if (!publicInstance && !repo) return null;
  return (
    <span className="mt-2 block text-xs text-fg-muted">
      {publicInstance
        ? L(
            "Instance publique proposée — hébergée par le fournisseur.",
            "Public instance offered — hosted by the provider.",
          )
        : L(
            "Utilisez l’adresse d’une instance publique ou de votre instance privée.",
            "Use the address of a public instance or your private instance.",
          )}
      {repo ? (
        <>
          {" "}
          {"· "}
          <a
            className="underline underline-offset-2"
            href={repo}
            target="_blank"
            rel="noopener noreferrer"
          >
            {L("Héberger sa propre instance (GitHub)", "Host your own instance (GitHub)")} ↗
          </a>
        </>
      ) : null}
    </span>
  );
}
