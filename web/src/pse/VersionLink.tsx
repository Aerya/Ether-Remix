import type { ReactNode } from "react";
import remixLogo from "../assets/brands/remix-media.png";

/** Identité du fork, distincte du crédit Ether conservé dans le pied de menu. */
export function RemixBrand() {
  return (
    <span className="flex items-center gap-2.5">
      <img src={remixLogo} alt="" width={40} height={40} className="rounded-xl shrink-0" />
      <span className="text-lg font-semibold tracking-tight">
        Remix
        <span className="block text-[10px] font-normal tracking-wide text-fg-subtle">
          Remix-Media
        </span>
      </span>
    </span>
  );
}

/** Dépôt GitHub de PSE, ouvert depuis le numéro de version de la console. */
export const PSE_REPO_URL = "https://github.com/Remix-Media/Ether-Remix";

/**
 * Numéro de version en bas de la barre latérale, cliquable vers le dépôt
 * GitHub de PSE. Voir docs/pse/FORK.fr.md.
 */
export function PseVersionLink({
  version,
  className,
  children,
}: {
  version: string;
  className?: string;
  children?: ReactNode;
}) {
  return (
    <a
      href={PSE_REPO_URL}
      target="_blank"
      rel="noopener noreferrer"
      className={`${className ?? ""} hover:text-fg hover:underline`}
      title={`${version} · GitHub`}
    >
      {children ?? version}
    </a>
  );
}

/** Liens distincts vers le projet d’origine et le fork. */
export function RemixRepositoryLinks() {
  return (
    <div className="flex flex-wrap justify-center gap-x-3 gap-y-1 text-[10px] text-fg-subtle">
      <a
        href="https://github.com/braedonsaunders/ether"
        target="_blank"
        rel="noopener noreferrer"
        className="hover:underline"
        title="GitHub · Ether"
      >
        Ether ↗
      </a>
      <a
        href={PSE_REPO_URL}
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex items-center gap-1 hover:underline"
        title="GitHub · Remix-Media / Ether-Remix"
      >
        <img src={remixLogo} alt="" width={16} height={16} className="rounded" />
        Remix ↗
      </a>
    </div>
  );
}
