import { getLocale } from "../i18n";

/**
 * Cadence libre de PSE pour les collections : « 6h » = toutes les six
 * heures (1 à 168), en plus de live, hourly, daily et weekly
 * (internal/collections/pse_refresh.go). Voir docs/pse/FORK.fr.md.
 */

export const PSE_REFRESH_MAX_HOURS = 168;

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

/** Heures d'une cadence « Nh », ou 0. */
export function pseRefreshHours(refresh: string | undefined): number {
  const m = /^([1-9][0-9]{0,2})h$/.exec(refresh ?? "");
  const n = m ? Number(m[1]) : 0;
  return n >= 1 && n <= PSE_REFRESH_MAX_HOURS ? n : 0;
}

/** Cadence « Nh » bornée. */
export function pseRefreshEvery(hours: number): string {
  const n = Math.min(PSE_REFRESH_MAX_HOURS, Math.max(1, Math.round(hours || 1)));
  return `${n}h`;
}

/** « Toutes les 6 h », ou "" si ce n'est pas une cadence libre. */
export function pseRefreshLabel(refresh: string | undefined): string {
  const n = pseRefreshHours(refresh);
  if (!n) return "";
  return n === 1 ? L("Toutes les heures", "Every hour") : L(`Toutes les ${n} h`, `Every ${n} h`);
}
