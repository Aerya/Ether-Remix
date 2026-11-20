import "./navigation.css";
import { Button, Switch } from "@braedonsaunders/appkit-ui";
import { Stethoscope } from "lucide-react";
import type { ReactNode } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { DomainPage } from "../components/layout";
import { getLocale } from "../i18n";
import { EXPERT_GROUPS, REMIX_TABS, SIMPLE_TABS, simpleTabHref, simpleTabOf } from "./navigation";
import { useRemixMode } from "./remixMode";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const label = (t: { fr: string; en: string }) => L(t.fr, t.en);

/**
 * Cadre commun des pages Ether-Remix : titre, navigation, bouton Diagnostic et
 * interrupteur Expert. Le mode simple affiche quatre onglets ; le mode expert
 * regroupe tous les onglets par intention (Surveiller, Trouver, Lire,
 * Personnaliser). Une adresse réservée au mode expert affiche la navigation
 * experte sans modifier le mode mémorisé.
 */
export function RemixFrame({
  tab,
  children,
  className = "remix-navigation",
}: {
  tab: string | undefined;
  children: ReactNode;
  className?: string;
}) {
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const [mode, setMode] = useRemixMode();
  const simpleActive = simpleTabOf(tab, params.get("view"));
  const expert = mode === "expert" || simpleActive === null;
  const active = expert ? (tab ?? "") : (simpleActive ?? "");
  const groups = expert
    ? EXPERT_GROUPS.map((g) => ({
        name: label(g),
        tabs: g.tabs.map((key) => REMIX_TABS.find((t) => t.key === key)!),
      }))
    : [{ name: "", tabs: [...SIMPLE_TABS] }];
  return (
    <DomainPage
      className={className}
      title="Ether-Remix"
      base="/remix"
      description={L(
        "Films, séries, animés et musique. Les réglages s'appliquent sans redémarrer.",
        "Movies, shows, anime and music. Settings apply without a restart.",
      )}
    >
      <div className="flex flex-wrap items-end gap-x-6 gap-y-3 border-b border-border pb-3">
        <div
          role="tablist"
          aria-label={L("Sections d’Ether-Remix", "Ether-Remix sections")}
          className="flex flex-wrap items-end gap-x-6 gap-y-3"
        >
          {groups.map((g) => (
            <div key={g.name || "simple"} role="presentation">
              {g.name ? (
                <p
                  aria-hidden="true"
                  className="mb-1 text-xs uppercase tracking-wide text-fg-muted"
                >
                  {g.name}
                </p>
              ) : null}
              <div role="presentation" className="flex flex-wrap gap-x-1">
                {g.tabs.map((t) => (
                  <button
                    key={t.key}
                    type="button"
                    role="tab"
                    aria-selected={active === t.key}
                    onClick={() => navigate(expert ? `/remix/${t.key}` : simpleTabHref(t.key))}
                  >
                    {label(t)}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
        <div className="ml-auto flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            variant={tab === "diagnostic" ? "default" : "outline"}
            onClick={() => navigate("/remix/diagnostic")}
          >
            <Stethoscope className="mr-1.5 h-4 w-4" aria-hidden="true" />
            {L("Diagnostic", "Diagnostics")}
          </Button>
          <label className="flex items-center gap-2 text-sm text-fg-muted">
            {L("Expert", "Expert")}
            <Switch
              aria-label={L("Mode expert", "Expert mode")}
              checked={expert}
              onChange={(e) => {
                const on = e.currentTarget.checked;
                setMode(on ? "expert" : "simple");
                navigate(on ? "/remix/apercu" : "/remix/accueil");
              }}
            />
          </label>
        </div>
      </div>
      {children}
    </DomainPage>
  );
}
