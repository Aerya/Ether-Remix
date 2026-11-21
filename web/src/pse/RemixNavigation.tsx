import type { ReactNode } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { getLocale } from "../i18n";
import { isSimpleRoute } from "./navigation";
import { useRemixMode } from "./remixMode";

/**
 * Vrai quand la barre latérale doit afficher les entrées Expert : le mode Expert
 * est choisi, ou la page ouverte n’existe qu’en mode Expert (le mode mémorisé
 * ne change pas dans ce second cas).
 */
export function useRemixExpertView(): boolean {
  const [mode] = useRemixMode();
  const location = useLocation();
  if (mode === "expert") return true;
  if (!location.pathname.startsWith("/remix/")) return false;
  const tab = location.pathname.split("/")[2];
  return !isSimpleRoute(tab, new URLSearchParams(location.search).get("view"));
}

/** Entrée « Expert » du groupe Remix : un interrupteur au lieu d’un lien. */
export function RemixExpertToggle({
  className,
  title,
  children,
}: {
  className?: string;
  title?: string;
  children: ReactNode;
}) {
  const [, setMode] = useRemixMode();
  const navigate = useNavigate();
  const location = useLocation();
  const expert = useRemixExpertView();
  const fr = getLocale().startsWith("fr");
  return (
    <button
      type="button"
      aria-pressed={expert}
      aria-label={fr ? "Mode expert" : "Expert mode"}
      title={title}
      className={className}
      onClick={() => {
        setMode(expert ? "simple" : "expert");
        // Une page réservée à l’expert n’existe plus en mode Simple : retour à l’Accueil.
        if (location.pathname.startsWith("/remix/"))
          navigate(expert ? "/remix/accueil" : "/remix/apercu");
      }}
    >
      {children}
      <span
        aria-hidden="true"
        className={`ml-auto inline-flex h-4 w-7 shrink-0 items-center rounded-full transition-colors ${
          expert ? "bg-primary" : "bg-border"
        }`}
      >
        <span
          className={`h-3 w-3 rounded-full bg-surface transition-transform ${
            expert ? "translate-x-3.5" : "translate-x-0.5"
          }`}
        />
      </span>
    </button>
  );
}
