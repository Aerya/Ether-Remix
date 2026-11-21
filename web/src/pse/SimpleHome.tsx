import { Button } from "@braedonsaunders/appkit-ui";
import { Check, Stethoscope, Wand2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { useApp } from "../app-context";
import { LoadingRows } from "../components/feedback";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { QuickStart } from "./QuickStart";
import { getSetupFlag, setSetupFlag, type SetupFlag } from "./remixMode";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

interface Step {
  title: string;
  text: string;
  done: boolean;
  action: string;
  to: string;
  flag?: SetupFlag;
}

/** Accueil du mode simple : trois étapes pour regarder un premier titre. */
export function SimpleHome() {
  const { dashboard } = useApp();
  const navigate = useNavigate();
  const [indexing, setIndexing] = useState(false);
  useEffect(() => {
    let live = true;
    apiFetch<{ features?: { fields: { key: string; value: string }[] }[] }>("/pse/")
      .then((data) => {
        const v = data.features
          ?.flatMap((f) => f.fields)
          .find((f) => f.key === "PSE_INDEXER")?.value;
        if (live) setIndexing(v === "on");
      })
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, []);
  if (!dashboard) return <LoadingRows rows={3} />;
  const sources = dashboard.setupSteps?.find((s) => s.id === "sources");
  // Première visite sans aucune source : l’assistant démarre tout seul, une seule fois.
  if (!getSetupFlag("wizard") && sources && !sources.complete)
    return <Navigate replace to="/remix/assistant" />;
  const steps: Step[] = [
    {
      title: L("Ajouter une source", "Add a source"),
      text: sources?.complete
        ? L("Au moins une source est active.", "At least one source is active.")
        : L(
            "Une source indique où trouver vos films et séries.",
            "A source tells Remix where to find movies and shows.",
          ),
      done: !!sources?.complete,
      action: sources?.complete ? L("Modifier", "Edit") : L("Ajouter", "Add"),
      to: "/remix/sources",
    },
    {
      title: L("Choisir langues et qualité", "Choose languages and quality"),
      text: L(
        "Vérifiez les langues, résolutions et formats acceptés.",
        "Check the accepted languages, resolutions and formats.",
      ),
      done: getSetupFlag("lang"),
      action: L("Vérifier", "Check"),
      to: "/remix/lecture?view=qualite",
      flag: "lang",
    },
    {
      title: L("Connecter votre application", "Connect your app"),
      text: L(
        "Plex, Jellyfin, Emby, Infuse… activez celle que vous utilisez.",
        "Plex, Jellyfin, Emby, Infuse… enable the one you use.",
      ),
      done: getSetupFlag("app"),
      action: L("Connecter", "Connect"),
      to: "/apps",
      flag: "app",
    },
  ];
  const firstTodo = steps.findIndex((s) => !s.done);
  return (
    <section className="space-y-4" aria-label={L("Premiers pas", "Getting started")}>
      <div>
        <h2 className="text-lg font-semibold">
          {firstTodo === -1
            ? L("Tout est prêt", "All set")
            : L("3 étapes pour regarder un film", "3 steps to start watching")}
        </h2>
        <p className="text-sm text-fg-muted">
          {firstTodo === -1
            ? L(
                "Votre configuration de base est terminée. Si quelque chose ne se lance pas, lancez le diagnostic.",
                "Your basic setup is done. If something does not start, run the diagnostics.",
              )
            : L("Chaque étape s’ouvre d’un clic.", "Each step opens with one click.")}
        </p>
      </div>
      <ol className="divide-y divide-border rounded-xl border border-border">
        {steps.map((s, i) => (
          <li key={s.to} className="flex items-center gap-3 p-4">
            <span
              aria-hidden="true"
              className={`flex h-7 w-7 shrink-0 items-center justify-center rounded-full text-sm font-medium ${
                s.done ? "border border-success text-success" : "border border-border text-fg-muted"
              }`}
            >
              {s.done ? <Check className="h-4 w-4" /> : i + 1}
            </span>
            <div className="min-w-0 flex-1">
              <p className="font-medium">
                {s.title}
                <span className="sr-only">
                  {" — "}
                  {s.done ? L("fait", "done") : L("à faire", "to do")}
                </span>
              </p>
              <p className="text-sm text-fg-muted">{s.text}</p>
            </div>
            <Button
              size="sm"
              variant={i === firstTodo ? "default" : "outline"}
              onClick={() => {
                if (s.flag) setSetupFlag(s.flag);
                navigate(s.to);
              }}
            >
              {s.action}
            </Button>
          </li>
        ))}
      </ol>
      <p className="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm text-fg-muted">
        <Link className="inline-flex items-center gap-1 underline" to="/remix/assistant">
          <Wand2 className="h-4 w-4" aria-hidden="true" />
          {L("Relancer l’assistant", "Run the assistant again")}
        </Link>
        <Link className="inline-flex items-center gap-1 underline" to="/remix/diagnostic">
          <Stethoscope className="h-4 w-4" aria-hidden="true" />
          {L("Ça ne marche pas ? Lancer le diagnostic", "Not working? Run the diagnostics")}
        </Link>
      </p>
      <details className="rounded-xl border border-border px-5 py-4 text-sm">
        <summary className="cursor-pointer font-medium">
          {L("Guide détaillé pas à pas", "Detailed step-by-step guide")}
        </summary>
        <div className="mt-4">
          <QuickStart indexing={indexing} />
        </div>
      </details>
    </section>
  );
}
