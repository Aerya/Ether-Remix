import { Button, toast } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useApp } from "../app-context";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { getSetupFlag, setSetupFlag } from "./remixMode";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

type Language = "fr" | "all";
interface Values {
  features?: { fields: { key: string; value: string }[] }[];
}
const STEPS = [
  { fr: "Sources", en: "Sources" },
  { fr: "Langue", en: "Language" },
  { fr: "Application", en: "App" },
  { fr: "Terminé", en: "Done" },
] as const;

async function savePse(values: Record<string, string>) {
  await apiFetch("/pse/", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ values }),
  });
}

/** Assistant en quatre étapes : sources, langue, application, puis fin. */
export function RemixWizard() {
  const { dashboard } = useApp();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const step = Math.min(4, Math.max(1, Number(params.get("step")) || 1));
  const sourcesDone = !!dashboard?.setupSteps?.find((s) => s.id === "sources")?.complete;
  const [language, setLanguage] = useState<Language>("all");
  const [indexing, setIndexing] = useState(true);
  const [busy, setBusy] = useState(false);
  useEffect(() => setSetupFlag("wizard"), []);
  useEffect(() => {
    let live = true;
    apiFetch<Values>("/pse/")
      .then((data) => {
        const value = data.features
          ?.flatMap((f) => f.fields)
          .find((f) => f.key === "PSE_FRENCH")?.value;
        if (live && value) setLanguage(value === "on" ? "fr" : "all");
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, []);
  const go = (n: number) =>
    setParams((p) => {
      const next = new URLSearchParams(p);
      next.set("step", String(n));
      return next;
    });
  async function next() {
    if (step === 2) {
      setBusy(true);
      try {
        await savePse({ PSE_FRENCH: language === "fr" ? "on" : "off" });
        setSetupFlag("lang");
      } catch {
        toast.error(
          L(
            "Le choix de langue n’a pas pu être enregistré.",
            "The language choice could not be saved.",
          ),
        );
        setBusy(false);
        return;
      }
      setBusy(false);
    }
    go(step + 1);
  }
  async function finish() {
    setBusy(true);
    try {
      if (indexing && sourcesDone) await savePse({ PSE_INDEXER: "on" });
      toast.success(L("Assistant terminé", "Assistant finished"));
      navigate("/remix/accueil");
    } catch {
      toast.error(
        L(
          "La préparation automatique n’a pas pu être activée.",
          "Automatic preparation could not be enabled.",
        ),
      );
      setBusy(false);
    }
  }
  const choice = (id: Language, title: string, text: string, tag?: string) => (
    <button
      type="button"
      role="radio"
      aria-checked={language === id}
      onClick={() => setLanguage(id)}
      className={`rounded-xl p-4 text-left ${
        language === id ? "border-2 border-primary" : "border border-border"
      }`}
    >
      <span className="font-medium">{title}</span>
      {tag ? <span className="ml-2 text-xs text-fg-muted">{tag}</span> : null}
      <span className="mt-1 block text-sm text-fg-muted">{text}</span>
    </button>
  );
  return (
    <section className="mx-auto max-w-2xl space-y-6" aria-label={L("Assistant", "Assistant")}>
      <ol className="flex flex-wrap items-center gap-2 text-sm">
        {STEPS.map((s, i) => (
          <li
            key={s.fr}
            aria-current={step === i + 1 ? "step" : undefined}
            className={step === i + 1 ? "font-medium" : "text-fg-muted"}
          >
            {i + 1}. {L(s.fr, s.en)}
            {i < STEPS.length - 1 ? <span aria-hidden="true"> —</span> : null}
          </li>
        ))}
      </ol>
      {step === 1 ? (
        <div className="space-y-3">
          <h2 className="text-lg font-semibold">
            {L("Où trouver vos films et séries ?", "Where do your movies and shows come from?")}
          </h2>
          <p className="text-sm text-fg-muted">
            {L(
              "Ajoutez un addon Stremio, un indexeur ou un compte debrid. Remix ne fournit ni les comptes ni les manifestes.",
              "Add a Stremio addon, an indexer or a debrid account. Remix does not supply accounts or manifests.",
            )}
          </p>
          <p className="text-sm">
            {sourcesDone
              ? L("Une source est déjà active.", "A source is already active.")
              : L("Aucune source pour l’instant.", "No source yet.")}
          </p>
          <Button
            variant={sourcesDone ? "outline" : "default"}
            onClick={() => navigate("/remix/sources")}
          >
            {L("Ouvrir les sources", "Open sources")}
          </Button>
        </div>
      ) : null}
      {step === 2 ? (
        <div className="space-y-3">
          <h2 className="text-lg font-semibold">
            {L("Dans quelle langue voulez-vous regarder ?", "Which language do you watch in?")}
          </h2>
          <p className="text-sm text-fg-muted">
            {L("Vous pourrez changer plus tard.", "You can change this later.")}
          </p>
          <div role="radiogroup" className="grid gap-3 sm:grid-cols-2">
            {choice(
              "fr",
              L("Français", "French"),
              L("Titres disponibles en VF ou MULTi.", "Titles available in French or MULTi."),
              L("conseillé", "recommended"),
            )}
            {choice(
              "all",
              L("Toutes langues", "All languages"),
              L("Aucun filtre de langue.", "No language filter."),
            )}
          </div>
        </div>
      ) : null}
      {step === 3 ? (
        <div className="space-y-3">
          <h2 className="text-lg font-semibold">
            {L("Connectez votre application", "Connect your app")}
          </h2>
          <p className="text-sm text-fg-muted">
            {L(
              "Activez le protocole de votre client (Plex, Jellyfin, Emby…) dans Applications, puis connectez-vous avec le compte du spectateur.",
              "Enable your client’s protocol (Plex, Jellyfin, Emby…) in Apps, then sign in with the viewer’s account.",
            )}
          </p>
          <Button
            variant="outline"
            onClick={() => {
              setSetupFlag("app");
              navigate("/apps");
            }}
          >
            {L("Ouvrir Applications", "Open Apps")}
          </Button>
          <p className="text-sm text-fg-muted">
            {L("Revenez ensuite ici avec ", "Then come back with ")}
            <Link className="underline" to="/remix/assistant?step=4">
              {L("l’étape Terminé", "the Done step")}
            </Link>
            .
          </p>
        </div>
      ) : null}
      {step === 4 ? (
        <div className="space-y-3">
          <h2 className="text-lg font-semibold">{L("Presque fini", "Almost done")}</h2>
          <ul className="space-y-1 text-sm">
            <li>
              {sourcesDone ? "✓ " : "✗ "}
              {L("Source", "Source")}
            </li>
            <li>
              {getSetupFlag("lang") ? "✓ " : "✗ "}
              {L("Langue", "Language")}
            </li>
            <li>
              {getSetupFlag("app") ? "✓ " : "✗ "}
              {L("Application", "App")}
            </li>
          </ul>
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              checked={indexing && sourcesDone}
              disabled={!sourcesDone}
              onChange={(e) => setIndexing(e.currentTarget.checked)}
              className="mt-1"
            />
            <span>
              {L(
                "Préparer automatiquement les titres en arrière-plan (recherche des releases, sans téléchargement).",
                "Prepare titles automatically in the background (release search, no download).",
              )}
            </span>
          </label>
          {!sourcesDone ? (
            <p className="text-sm text-fg-muted">
              {L("Disponible une fois une source ajoutée.", "Available once a source is added.")}
            </p>
          ) : null}
        </div>
      ) : null}
      <div className="flex justify-between">
        {step > 1 ? (
          <Button variant="outline" onClick={() => go(step - 1)}>
            {L("Retour", "Back")}
          </Button>
        ) : (
          <Button variant="ghost" onClick={() => navigate("/remix/accueil")}>
            {L("Passer l’assistant", "Skip the assistant")}
          </Button>
        )}
        {step < 4 ? (
          <Button onClick={() => void next()} disabled={busy}>
            {L("Suivant", "Next")}
          </Button>
        ) : (
          <Button onClick={() => void finish()} disabled={busy}>
            {L("Terminer", "Finish")}
          </Button>
        )}
      </div>
    </section>
  );
}
