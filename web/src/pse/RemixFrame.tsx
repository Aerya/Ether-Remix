import "./navigation.css";
import { Button, Switch } from "@braedonsaunders/appkit-ui";
import { Stethoscope } from "lucide-react";
import type { ReactNode } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { DomainPage } from "../components/layout";
import { getLocale } from "../i18n";
import { REMIX_TABS, SIMPLE_TABS, simpleTabHref, simpleTabOf } from "./navigation";
import { useRemixMode } from "./remixMode";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

/**
 * Cadre commun des pages Ether-Remix : titre, onglets du mode simple ou du mode
 * expert, bouton Diagnostic et interrupteur Expert. Une adresse réservée au mode
 * expert affiche les onglets experts sans modifier le mode mémorisé.
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
  const tabs = expert
    ? REMIX_TABS.map((t) => ({ key: t.key, label: L(t.fr, t.en) }))
    : SIMPLE_TABS.map((t) => ({ key: t.key, label: L(t.fr, t.en) }));
  return (
    <DomainPage
      className={className}
      title="Ether-Remix"
      base="/remix"
      tabs={tabs}
      active={expert ? (tab ?? "") : (simpleActive ?? "")}
      tabHref={expert ? undefined : simpleTabHref}
      tabActions={
        <>
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
        </>
      }
      description={L(
        "Films, séries, animés et musique. Les réglages s'appliquent sans redémarrer.",
        "Movies, shows, anime and music. Settings apply without a restart.",
      )}
    >
      {children}
    </DomainPage>
  );
}
