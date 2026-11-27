import { Navigate, Link } from "react-router-dom";
import { useApp } from "../app-context";
import { LoadingRows } from "../components/feedback";
import { getLocale } from "../i18n";
import { getSetupFlag } from "./remixMode";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
/** One entry point for first-time setup and revisiting an existing installation. */
export function SimpleHome() {
  const { dashboard } = useApp();
  if (!dashboard) return <LoadingRows rows={3} />;
  const sources = dashboard.setupSteps?.find((s) => s.id === "sources");
  if (!getSetupFlag("wizard") && sources && !sources.complete)
    return <Navigate replace to="/remix/assistant" />;
  return (
    <section className="space-y-5" aria-label={L("Premiers pas", "Getting started")}>
      <div>
        <h2 className="text-lg font-semibold">
          {L("Votre configuration Remix", "Your Remix configuration")}
        </h2>
        <p className="text-sm text-fg-muted">
          {L(
            "Contenus, langues, découvertes, lecture, bibliothèques et affiches : un seul parcours, à reprendre quand vous voulez.",
            "Content, languages, discovery, playback, libraries and artwork: one journey you can revisit any time.",
          )}
        </p>
      </div>
      <Link
        className="inline-flex rounded bg-primary px-4 py-2 text-primary-fg"
        to="/remix/assistant"
      >
        {L("Relancer l’assistant de configuration", "Run the setup assistant again")}
      </Link>
      <p className="text-sm">
        {L(
          "Les réglages existants sont conservés. Le questionnaire présente les modifications avant application ; ouvrir un éditeur ne valide pas sa connexion.",
          "Existing settings are preserved. The questionnaire previews changes before applying; opening an editor does not validate its connection.",
        )}
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Link className="rounded border border-border p-4" to="/remix/sources">
          {L("Gérer les sources", "Manage sources")}
        </Link>
        <Link className="rounded border border-border p-4" to="/remix/diagnostic">
          {L("Vérifier les connexions et la lecture", "Check connections and playback")}
        </Link>
      </div>
    </section>
  );
}
