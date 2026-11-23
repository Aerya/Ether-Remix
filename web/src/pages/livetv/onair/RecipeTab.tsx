import { MultiSelect, SearchSelect, Switch, Tooltip } from "@braedonsaunders/appkit-ui";
import { Info } from "lucide-react";
import { Artwork } from "../../../components/Artwork";
import { MoreOptions } from "../../../components/ConfigSection";
import { SettingsRow } from "../../../components/SettingsRow";
import { Field, NumberInput } from "../../../components/fields";
import { t } from "../../../i18n";
import type { Programme } from "../model";
import type { CollectionChoice } from "../ChannelStudio";
import type { PersonalRecipe } from "./model";

export function InfoTip({ text }: { text: string }) {
  return (
    <Tooltip content={text} className="w-72 max-w-xs whitespace-normal">
      <button type="button" aria-label={text} className="inline-flex align-middle text-fg-subtle">
        <Info size={14} />
      </button>
    </Tooltip>
  );
}
export function RecipeTab({
  recipe,
  onChange,
  rundown,
  collections,
}: {
  recipe: PersonalRecipe;
  onChange: (recipe: PersonalRecipe) => void;
  rundown: Programme[];
  collections: CollectionChoice[];
}) {
  const labels: Record<string, string> = {
    continue: t("Continue watching"),
    nextEpisodes: t("Next episodes"),
    picked: t("Picked for them"),
    watchlist: t("From their watchlist"),
    comfort: t("Comfort rewatches"),
    newReleases: t("New releases they'd like"),
    collections: t("Your picks (collections)"),
  };
  const nameFormatOptions = [
    { value: "{viewer} TV", label: t("Viewer's name + TV") },
    { value: "{viewer} Live", label: t("Viewer's name + Live") },
    { value: "Made for {viewer}", label: t("Made for + viewer's name") },
  ];
  if (recipe.nameFormat && !nameFormatOptions.some((opt) => opt.value === recipe.nameFormat)) {
    nameFormatOptions.push({
      value: recipe.nameFormat,
      label: recipe.nameFormat.replaceAll("{viewer}", t("Viewer's name")),
    });
  }
  const set = (patch: Partial<PersonalRecipe>) => onChange({ ...recipe, ...patch });
  const ingredient = (i: number, patch: Partial<PersonalRecipe["ingredients"][number]>) =>
    set({ ingredients: recipe.ingredients.map((v, n) => (n === i ? { ...v, ...patch } : v)) });
  return (
    <div className="space-y-5">
      <div className="divide-y divide-border overflow-hidden rounded-xl border border-border bg-surface">
        {recipe.ingredients.map((v, i) => {
          const pick = rundown.length ? rundown[i % rundown.length] : undefined;
          return (
            <div
              key={v.kind}
              className={`flex flex-wrap items-center gap-3 p-3 ${v.kind === "collections" && v.enabled ? "" : "sm:flex-nowrap"}`}
            >
              <Artwork
                src={pick?.item.thumb}
                title=""
                alt=""
                className="h-12 w-20 shrink-0 rounded-lg"
                imgClassName="object-cover"
              />
              <div className="min-w-0 flex-1 sm:w-48 sm:flex-none">
                <p className="text-sm font-medium text-fg">{labels[v.kind]}</p>
                {pick?.item.title ? (
                  <p className="truncate text-xs text-fg-muted">{pick.item.title}</p>
                ) : null}
              </div>
              <div className="order-last flex w-full items-center gap-3 sm:order-none sm:w-auto sm:flex-1">
                <input
                  type="range"
                  min={0}
                  max={100}
                  value={v.share}
                  disabled={!v.enabled}
                  aria-label={t("{ingredient} share", { ingredient: labels[v.kind] || "" })}
                  onChange={(e) => ingredient(i, { share: Number(e.currentTarget.value) })}
                  className="min-w-0 flex-1 accent-primary"
                />
                <span className="w-9 text-right text-xs tabular-nums text-fg-muted">
                  {v.share}%
                </span>
              </div>
              <Switch
                checked={v.enabled}
                aria-label={labels[v.kind]}
                onChange={(e) => ingredient(i, { enabled: e.currentTarget.checked })}
              />
              {v.kind === "collections" && v.enabled ? (
                <div className="w-full min-w-0">
                  <MultiSelect
                    ariaLabel={t("Your picks (collections)")}
                    value={(v.collections ?? []).map(String)}
                    onChange={(values) => ingredient(i, { collections: values.map(Number) })}
                    placeholder={t("Choose collections…")}
                    searchPlaceholder={t("Search…")}
                    noMatchesLabel={t("No matches")}
                    clearLabel={t("Clear")}
                    selectAllLabel={t("Select all")}
                    options={collections
                      .filter((c) => c.ratingKey && Number.isFinite(Number(c.ratingKey)))
                      .map((c) => ({ value: c.ratingKey!, label: c.name }))}
                  />
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
      <div className="divide-y divide-border">
        <SettingsRow
          title={
            <span className="flex items-center gap-2">
              {t("Pause when they leave")}
              <InfoTip
                text={t(
                  "The channel pauses while this viewer is away and resumes when they return.",
                )}
              />
            </span>
          }
          control={
            <Switch
              checked={recipe.pauseWhenAway}
              aria-label={t("Pause when they leave")}
              onChange={(e) => set({ pauseWhenAway: e.currentTarget.checked })}
            />
          }
        />
        <SettingsRow
          title={t("Channel name")}
          description={t("How this channel appears in each viewer's guide, using their name.")}
          control={
            <div className="w-60 max-w-full">
              <SearchSelect
                value={recipe.nameFormat}
                onChange={(nameFormat) => set({ nameFormat })}
                options={nameFormatOptions}
                ariaLabel={t("Channel name")}
                searchable={false}
                selectLabel={t("Select")}
                closeLabel={t("Close")}
              />
            </div>
          }
        />
      </div>
      <MoreOptions label={t("More options")}>
        <div className="grid gap-4 p-4 sm:grid-cols-3">
          <Field label={t("Don't repeat within (hours)")}>
            <NumberInput
              min={1}
              max={168}
              value={recipe.repeatHours}
              onChange={(repeatHours) => set({ repeatHours })}
            />
          </Field>
          <Field label={t("Max share per show (%)")}>
            <NumberInput
              min={1}
              max={100}
              value={recipe.maxShowShare}
              onChange={(maxShowShare) => set({ maxShowShare })}
            />
          </Field>
          <Field label={t("Night runtime limit (minutes)")}>
            <NumberInput
              min={0}
              max={240}
              value={recipe.nightMaxMinutes}
              onChange={(nightMaxMinutes) => set({ nightMaxMinutes })}
            />
          </Field>
        </div>
      </MoreOptions>
    </div>
  );
}
