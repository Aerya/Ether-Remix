import { useCallback, useEffect, useState } from "react";

/**
 * Mode d’affichage de la page Ether-Remix : « simple » (parcours guidé pour
 * débuter) ou « expert » (tous les réglages). Le choix est mémorisé par
 * navigateur, sans réglage serveur.
 */
export type RemixMode = "simple" | "expert";

const MODE_KEY = "remix.mode";
const CHANGE = "remix-mode-change";

function read(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}
function write(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    /* stockage indisponible : le choix vaut pour cette page seulement */
  }
}

export function getRemixMode(): RemixMode {
  return read(MODE_KEY) === "expert" ? "expert" : "simple";
}

export function useRemixMode(): readonly [RemixMode, (mode: RemixMode) => void] {
  const [mode, setMode] = useState<RemixMode>(getRemixMode);
  useEffect(() => {
    const sync = () => setMode(getRemixMode());
    window.addEventListener(CHANGE, sync);
    window.addEventListener("storage", sync);
    return () => {
      window.removeEventListener(CHANGE, sync);
      window.removeEventListener("storage", sync);
    };
  }, []);
  const update = useCallback((next: RemixMode) => {
    write(MODE_KEY, next);
    setMode(next);
    window.dispatchEvent(new Event(CHANGE));
  }, []);
  return [mode, update] as const;
}

/** Étapes déjà vues ou faites, mémorisées par navigateur. */
export type SetupFlag = "wizard" | "lang" | "app";
export const getSetupFlag = (flag: SetupFlag): boolean => read(`remix.setup.${flag}`) === "1";
export const setSetupFlag = (flag: SetupFlag) => write(`remix.setup.${flag}`, "1");
