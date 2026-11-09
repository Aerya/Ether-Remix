import { Field, NumberInput, Segmented } from "../components/fields";
import { getLocale } from "../i18n";
import type { CollectionSpec } from "../lib/contract";
import { REFRESH_OPTIONS } from "../lib/rules";
import { PSE_REFRESH_MAX_HOURS, pseRefreshEvery, pseRefreshHours } from "./refresh";

/**
 * Choix de la cadence d'une collection, avec l'option PSE « Toutes les N h ».
 * Remplace le sélecteur d'Ether dans les réglages de collection (accroche
 * dans web/src/pages/collections/SettingsPanels.tsx). Voir
 * docs/pse/FORK.fr.md.
 */

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

type Choice = "live" | "hourly" | "every" | "daily" | "weekly";

export function PseRefreshField({
  refresh,
  onChange,
  label,
  allowLive = true,
}: {
  refresh: CollectionSpec["refresh"];
  onChange: (refresh: CollectionSpec["refresh"]) => void;
  label: string;
  allowLive?: boolean;
}) {
  const hours = pseRefreshHours(refresh);
  const choice: Choice = hours ? "every" : (refresh as Choice);
  const options: { value: Choice; label: string }[] = [];
  for (const r of REFRESH_OPTIONS) {
    if (r.value === "live" && !allowLive) continue;
    options.push({ value: r.value as Choice, label: r.label });
    if (r.value === "hourly")
      options.push({ value: "every", label: L("Toutes les N h", "Every N h") });
  }
  const hint = hours
    ? L(`Reconstruite toutes les ${hours} h`, `Rebuilt every ${hours} h`)
    : REFRESH_OPTIONS.find((r) => r.value === refresh)?.hint;
  return (
    <Field label={label} hint={hint}>
      <div className="flex flex-wrap items-center gap-3">
        <Segmented
          ariaLabel={label}
          value={choice}
          onChange={(v) =>
            onChange((v === "every" ? pseRefreshEvery(hours || 6) : v) as CollectionSpec["refresh"])
          }
          options={options}
        />
        {hours ? (
          <label className="flex items-center gap-2 text-sm text-fg-muted">
            {L("Toutes les", "Every")}
            <NumberInput
              aria-label={L("Intervalle en heures", "Interval in hours")}
              value={hours}
              min={1}
              max={PSE_REFRESH_MAX_HOURS}
              className="w-20"
              onChange={(n) => onChange(pseRefreshEvery(n) as CollectionSpec["refresh"])}
            />
            {L("heures", "hours")}
          </label>
        ) : null}
      </div>
    </Field>
  );
}
