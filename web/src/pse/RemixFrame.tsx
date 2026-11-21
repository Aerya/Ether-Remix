import type { ReactNode } from "react";
import { DomainPage } from "../components/layout";
import { getLocale } from "../i18n";
import { remixTitle } from "./navigation";

const fr = () => getLocale().startsWith("fr");

/**
 * Cadre commun des pages Ether-Remix. La navigation se fait dans le groupe
 * « Remix » de la barre latérale ; la page ne garde que son titre.
 */
export function RemixFrame({ tab, children }: { tab: string | undefined; children: ReactNode }) {
  const title = remixTitle(tab, fr()) ?? "Ether-Remix";
  return (
    <DomainPage
      title={title}
      base="/remix"
      description={
        tab === "accueil" || tab === "assistant" || !remixTitle(tab, true)
          ? fr()
            ? "Films, séries, animés et musique. Les réglages s'appliquent sans redémarrer."
            : "Movies, shows, anime and music. Settings apply without a restart."
          : undefined
      }
    >
      {children}
    </DomainPage>
  );
}
