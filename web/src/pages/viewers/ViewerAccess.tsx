import { ViewerCatalogRules } from "./ViewerCatalogRules";
import { Button, SearchSelect, SettingsSection, Switch, toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { Link } from "react-router-dom";
import { ChipGroup, NumberInput } from "../../components/fields";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import {
  RatingLimitRow,
  StreamLimitRow,
  userLimits,
  type UserLimits,
} from "../../components/UserLimits";
import { viewerName } from "../../components/pickers";
import { t } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { api, apiFetch, errorMessage } from "../../lib/api";
import type { Viewer } from "../../lib/contract";
import { useAsync } from "../../lib/hooks";
import { timeZones } from "../livetv/model";
import { PlexWatchSync } from "./sections/PlexWatchSync";
import { FromPlexDefaults } from "./FromPlexDefaults";
import { ViewerLiveTVAccess } from "./sections/ViewerLiveTV";
import { ViewerSimkl } from "./sections/ViewerSimkl";
import { ViewerMDBList } from "./sections/ViewerMDBList";
import { ViewerTrakt } from "./sections/ViewerTrakt";
import { KID_PRESETS, ListText, normalizePolicy, type SafetyPolicy } from "./shared";
import { librariesApi, type Library } from "../libraries/api";
import type { ThemeData, ThemeLibrary } from "../../pse/ThemeLibraries";

/** A searchable IANA timezone picker; a stored zone the browser lacks stays listed. */
function TimeZoneSelect({
  value,
  onChange,
  serverDefault = false,
}: {
  value: string;
  onChange: (zone: string) => void;
  serverDefault?: boolean;
}) {
  const options = (() => {
    const zones = timeZones();
    const list = value && !zones.includes(value) ? [value, ...zones] : zones;
    return [
      ...(serverDefault ? [{ value: "", label: t("Server time zone") }] : []),
      ...list.map((zone) => ({ value: zone, label: zone.replace(/_/g, " ") })),
    ];
  })();
  return (
    <SearchSelect
      value={value}
      onChange={onChange}
      options={options}
      ariaLabel={t("Playback timezone")}
      searchable
      {...searchSelectLabels()}
    />
  );
}

/** Numéro de section Plex de la bibliothèque Remix : 5100 + son identifiant. */
export const remixSection = (theme: ThemeLibrary) => 5100 + theme.id;

/**
 * 5100 n'est jamais un vrai thème : sa présence marque un choix Remix explicite
 * (tout décocher = aucun thème). Il n'est jamais affiché comme case.
 */
export const REMIX_MARKER = 5100;
const inRemixRange = (id: number) => id >= REMIX_MARKER && id <= 5299;

/** Bibliothèque d'Ether (Films / Séries) dont dépend la visibilité d'une bibliothèque Remix. */
function baseLibraryIds(theme: ThemeLibrary, libs: Library[]): number[] {
  const want = theme.type === "mixed" ? ["movie", "show"] : [theme.type];
  return libs.filter((l) => want.includes(l.mediaType ?? "")).map((l) => l.id);
}

/**
 * Bibliothèques Remix cochées. Un réglage restreint sans aucun numéro Remix
 * (antérieur à ces cases) garde la visibilité d'origine : un thème est coché
 * quand sa bibliothèque de base l'est. Sinon, la liste fait foi (un numéro
 * orphelin n'a pas de case et est simplement ignoré).
 */
export function remixChecked(current: number[], themes: ThemeLibrary[], libs: Library[]) {
  const legacy = !current.some(inRemixRange);
  const out = new Set<number>();
  for (const th of themes) {
    const on = legacy
      ? baseLibraryIds(th, libs).some((id) => current.includes(id))
      : current.includes(remixSection(th)) || current.includes(remixSection(th) + 100);
    if (on) out.add(remixSection(th));
  }
  return out;
}

/** Libraries this viewer can open (empty restriction = every library, §1.4). */
export function ViewerLibraries({
  userId,
  allowed,
  isOwner,
  onSaved,
}: {
  userId: number;
  allowed?: number[] | null;
  isOwner?: boolean;
  onSaved: () => void;
}) {
  const libs = useAsync((signal) => librariesApi.list(signal), []);
  const remix = useAsync(() => apiFetch<ThemeData>("/pse/themes/"), []);
  const [draft, setDraft] = useState<number[] | null>(null);
  const [saving, setSaving] = useState(false);
  const all = libs.data ?? [];
  const themes = remix.data?.libraries ?? [];
  const current = draft ?? allowed ?? [];
  const unrestricted = current.length === 0;
  const checkedThemes = remixChecked(current, themes, all);
  const dirty = draft !== null && JSON.stringify(draft) !== JSON.stringify(allowed ?? []);
  const baseOn = (th: ThemeLibrary) => baseLibraryIds(th, all).some((id) => current.includes(id));
  // La liste enregistrée : bibliothèques d'Ether, plus les Remix cochées (5100+ID).
  const withRemix = (ids: number[], on: Set<number>) =>
    [...ids.filter((x) => !inRemixRange(x)), REMIX_MARKER, ...on].sort((a, b) => a - b);
  const toggleLibrary = (id: number, on: boolean) =>
    setDraft((prev) => {
      const base = prev ?? allowed ?? [];
      const ids = on ? [...base.filter((x) => x !== id), id] : base.filter((x) => x !== id);
      return withRemix(ids, remixChecked(base, themes, all)).sort((a, b) => a - b);
    });
  const toggleRemix = (section: number, on: boolean) =>
    setDraft((prev) => {
      const base = prev ?? allowed ?? [];
      const set = remixChecked(base, themes, all);
      if (on) set.add(section);
      else set.delete(section);
      return withRemix(base, set);
    });
  return (
    <SettingsSection
      title={t("Libraries this viewer can open")}
      description={t(
        "With every library allowed, new libraries open for this viewer too. Pick libraries to limit them to just those.",
      )}
    >
      {libs.loading || remix.loading ? (
        <LoadingRows rows={2} />
      ) : (
        <>
          <SettingsRow
            title={t("All libraries")}
            control={
              <Switch
                aria-label={t("All libraries")}
                checked={unrestricted || Boolean(isOwner)}
                disabled={Boolean(isOwner)}
                onChange={(e) =>
                  setDraft(
                    e.target.checked
                      ? []
                      : [REMIX_MARKER, ...all.map((l) => l.id), ...themes.map(remixSection)].sort(
                          (a, b) => a - b,
                        ),
                  )
                }
              />
            }
          />
          {!unrestricted && !isOwner
            ? all.map((l) => (
                <SettingsRow
                  key={l.id}
                  title={l.name}
                  control={
                    <Switch
                      aria-label={l.name}
                      checked={current.includes(l.id)}
                      onChange={(e) => toggleLibrary(l.id, e.target.checked)}
                    />
                  }
                />
              ))
            : null}
          {!unrestricted && !isOwner && themes.length > 0 ? (
            <>
              <h4 className="px-5 pt-4 text-sm font-medium text-fg">{t("Remix libraries")}</h4>
              {themes.map((th) => (
                <SettingsRow
                  key={`remix-${th.id}`}
                  title={th.name}
                  description={baseOn(th) ? undefined : t("Needs the Movies or Shows library")}
                  control={
                    <Switch
                      aria-label={th.name}
                      checked={checkedThemes.has(remixSection(th))}
                      disabled={!baseOn(th)}
                      onChange={(e) => toggleRemix(remixSection(th), e.target.checked)}
                    />
                  }
                />
              ))}
            </>
          ) : null}
          {isOwner ? (
            <p className="px-5 py-4 text-sm text-fg-muted">
              {t("The owner can always open every library.")}
            </p>
          ) : (
            <div className="pt-2">
              <Button
                size="sm"
                disabled={!dirty || saving}
                onClick={() => {
                  setSaving(true);
                  api
                    .updateUser(userId, { allowedLibraries: current })
                    .then(() => {
                      toast.success(t("Saved"));
                      setDraft(null);
                      onSaved();
                    })
                    .catch((e: unknown) => toast.error(errorMessage(e)))
                    .finally(() => setSaving(false));
                }}
              >
                {t("Save")}
              </Button>
            </div>
          )}
        </>
      )}
    </SettingsSection>
  );
}

/**
 * Everything that decides what one viewer may watch and when: streams at
 * once, the content rating limit, parental controls, allowed times, Live TV
 * and their connected accounts. One in-place form; the save bar writes
 * whichever parts changed. Account actions run immediately.
 */
export function ViewerAccess({ viewer }: { viewer: Viewer }) {
  const id = viewer.id;
  const name = viewerName(viewer);
  const users = useAsync(() => api.users(), []);
  const config = useAsync(() => api.getConfig(), []);
  const policy = useAsync(
    async (signal) =>
      normalizePolicy(await apiFetch<SafetyPolicy>(`/safety/policies/${id}`, { signal })),
    [id],
  );
  const user = users.data?.find((u) => u.id === id);
  const limits = useDraft<UserLimits>(user ? userLimits(user) : null);
  const rules = useDraft<SafetyPolicy>(policy.data);
  const [saving, setSaving] = useState(false);
  const [presetBusy, setPresetBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const patchLimits = (p: Partial<UserLimits>) => limits.setDraft((d) => (d ? { ...d, ...p } : d));
  const patchPolicy = (p: Partial<SafetyPolicy>) => rules.setDraft((d) => (d ? { ...d, ...p } : d));

  const applyPreset = async (age: string) => {
    setPresetBusy(true);
    try {
      const { maxContentRating, ...preset } = await apiFetch<
        Partial<SafetyPolicy> & { maxContentRating?: string }
      >(`/safety/policies/presets/kids?age=${age}`);
      rules.setDraft(normalizePolicy(preset));
      if (maxContentRating !== undefined) patchLimits({ maxContentRating });
    } catch (e) {
      toast.error(t("Couldn't load the preset"), { description: errorMessage(e) });
    } finally {
      setPresetBusy(false);
    }
  };

  const save = async () => {
    setSaving(true);
    setError(null);
    try {
      if (limits.dirty && limits.draft && user) {
        const saved = await api.updateUser(id, limits.draft);
        const next = saved ?? { ...user, ...limits.draft };
        users.setData((list) => (list ?? []).map((u) => (u.id === id ? next : u)));
      }
      if (rules.dirty && rules.draft) {
        await apiFetch(`/safety/policies/${id}`, {
          method: "PUT",
          body: JSON.stringify(rules.draft),
        });
        policy.setData(rules.draft);
      }
      toast.success(t("Preferences saved"), {
        description: t("{name}'s Ether preferences are up to date.", { name }),
      });
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };

  const l = limits.draft;
  const p = rules.draft;
  const noUser = users.data && (!user || !l);
  const trakt = config.data?.trakt;
  const simkl = config.data?.simkl;
  const mdblist = config.data?.mdblist;
  return (
    <div className="space-y-6">
      <ViewerCatalogRules key={id} id={id} region={String(config.data?.catalog?.region || "US")} />

      {users.error ? (
        <ErrorPanel error={users.error} onRetry={() => void users.reload()} />
      ) : policy.error ? (
        <ErrorPanel error={policy.error} onRetry={() => void policy.reload()} />
      ) : !users.data || !p ? (
        <LoadingRows rows={3} />
      ) : (
        <>
          {noUser ? (
            <p className="text-sm text-fg-muted">
              {t(
                "This viewer has no user record on the server yet, so there are no limits to set.",
              )}
            </p>
          ) : null}
          <SettingsSection
            title={t("Parental controls")}
            description={t(
              "Adult titles are always excluded. Unrated titles are blocked when a rating limit applies.",
            )}
          >
            <SettingsRow
              title={t("Start from a preset")}
              description={t(
                "Replaces the parental controls, playback restrictions and allowed times below.",
              )}
              control={
                <div className="flex flex-wrap gap-2">
                  {KID_PRESETS.map((preset) => (
                    <Button
                      key={preset.value}
                      variant="outline"
                      size="sm"
                      disabled={presetBusy}
                      onClick={() => void applyPreset(preset.value)}
                    >
                      {preset.label}
                    </Button>
                  ))}
                </div>
              }
            />
            {user && l ? (
              <RatingLimitRow
                value={l.maxContentRating}
                onChange={(maxContentRating) => patchLimits({ maxContentRating })}
                isOwner={user.isOwner}
              />
            ) : null}
            <SettingsRow
              title={t("Blocked genres")}
              description={t("Comma-separated, e.g. Horror, Thriller.")}
              control={
                <div className="w-64">
                  <ListText
                    ariaLabel={t("Blocked genres")}
                    value={p.blockedGenres}
                    onChange={(v) => patchPolicy({ blockedGenres: v })}
                  />
                </div>
              }
            />
            <SettingsRow
              title={t("Blocked keywords")}
              description={t("Comma-separated TMDB keywords.")}
              control={
                <div className="w-64">
                  <ListText
                    ariaLabel={t("Blocked keywords")}
                    value={p.blockedKeywords}
                    onChange={(v) => patchPolicy({ blockedKeywords: v })}
                  />
                </div>
              }
            />
          </SettingsSection>

          <ViewerLibraries
            userId={id}
            allowed={user?.allowedLibraries}
            isOwner={user?.isOwner}
            onSaved={() => void users.reload()}
          />

          <SettingsSection title={t("Playback restrictions")}>
            {user && l ? (
              <StreamLimitRow
                value={l.streamLimit}
                onChange={(streamLimit) => patchLimits({ streamLimit })}
                defaultStreamLimit={config.data?.server.defaultStreamLimit}
                hint={
                  <Link
                    to="/playback/transcoding"
                    className="font-medium text-primary hover:underline"
                  >
                    {t("Change the server default")}
                  </Link>
                }
              />
            ) : null}
            <SettingsRow
              title={t("Local network only")}
              description={t("Block playback from outside your home network.")}
              control={
                <Switch
                  aria-label={t("Local network only")}
                  checked={p.remoteDisabled}
                  onChange={(e) => patchPolicy({ remoteDisabled: e.target.checked })}
                />
              }
            />
            <SettingsRow
              title={t("Maximum bitrate")}
              description={t("In kbps. 0 uses the server default.")}
              control={
                <NumberInput
                  aria-label={t("Maximum bitrate")}
                  className="w-28"
                  min={0}
                  value={p.maxBitrateKbps}
                  onChange={(n) => patchPolicy({ maxBitrateKbps: Math.max(0, Math.round(n)) })}
                />
              }
            />
          </SettingsSection>

          {user && !user.isOwner ? (
            <SettingsSection
              title={t("Allowed times")}
              description={t("Let the current item finish, then pause playback until wake time.")}
            >
              {user && !user.isOwner ? (
                <>
                  <SettingsRow
                    title={t("Bedtime")}
                    control={
                      <Switch
                        checked={!!p.bedtime}
                        onChange={(e) =>
                          patchPolicy({
                            allowedHours: [],
                            bedtime: e.target.checked
                              ? { time: "20:00", wakeTime: "06:00", timezone: p.timezone }
                              : null,
                          })
                        }
                        aria-label={t("Bedtime")}
                      />
                    }
                  />
                  {!p.bedtime && p.allowedHours.length > 0 ? (
                    <SettingsRow
                      title={t("Allowed times")}
                      control={
                        <span>
                          {p.allowedHours
                            .map((hour) => `${String(hour).padStart(2, "0")}:00`)
                            .join(", ")}
                        </span>
                      }
                    />
                  ) : null}
                  {p.bedtime ? (
                    <>
                      <SettingsRow
                        title={t("Bedtime starts")}
                        control={
                          <input
                            type="time"
                            className="rounded border border-border bg-background px-3 py-2"
                            aria-label={t("Bedtime starts")}
                            required
                            value={p.bedtime.time}
                            onChange={(e) =>
                              patchPolicy({ bedtime: { ...p.bedtime!, time: e.target.value } })
                            }
                          />
                        }
                      />
                      <SettingsRow
                        title={t("Wake time")}
                        control={
                          <input
                            type="time"
                            className="rounded border border-border bg-background px-3 py-2"
                            aria-label={t("Wake time")}
                            required
                            value={p.bedtime.wakeTime || "06:00"}
                            onChange={(e) =>
                              patchPolicy({ bedtime: { ...p.bedtime!, wakeTime: e.target.value } })
                            }
                          />
                        }
                      />
                      <SettingsRow
                        title={t("Days")}
                        description={t(
                          "Choose the days bedtime starts. With no days selected, it applies every day.",
                        )}
                        control={
                          <ChipGroup
                            ariaLabel={t("Days")}
                            options={[
                              { value: 0, label: t("Sunday") },
                              { value: 1, label: t("Monday") },
                              { value: 2, label: t("Tuesday") },
                              { value: 3, label: t("Wednesday") },
                              { value: 4, label: t("Thursday") },
                              { value: 5, label: t("Friday") },
                              { value: 6, label: t("Saturday") },
                            ]}
                            value={p.bedtime.days ?? []}
                            onChange={(days) => patchPolicy({ bedtime: { ...p.bedtime!, days } })}
                          />
                        }
                      />
                    </>
                  ) : null}
                  <SettingsRow
                    title={t("Time zone")}
                    description={t("Leave empty to use the server’s time zone.")}
                    control={
                      <div className="w-64">
                        <TimeZoneSelect
                          serverDefault={!!p.bedtime || p.allowedHours.length === 0}
                          value={p.timezone}
                          onChange={(timezone) =>
                            patchPolicy({
                              timezone,
                              ...(p.bedtime ? { bedtime: { ...p.bedtime, timezone } } : {}),
                            })
                          }
                        />
                      </div>
                    }
                  />
                </>
              ) : null}
            </SettingsSection>
          ) : null}
        </>
      )}

      <FromPlexDefaults viewerId={id} show="share" />

      <ViewerLiveTVAccess viewerId={id} />

      <PlexWatchSync viewerId={id} plexId={viewer.plexId} />

      {config.data ? (
        <>
          <ViewerTrakt viewerId={id} ready={Boolean(trakt?.enabled && trakt.clientId.trim())} />
          <ViewerSimkl viewerId={id} ready={Boolean(simkl?.enabled && simkl.clientId.trim())} />
          <ViewerMDBList viewerId={id} ready={Boolean(mdblist?.enabled)} />
        </>
      ) : null}

      <SaveBar
        dirty={limits.dirty || rules.dirty}
        saving={saving}
        error={error}
        onSave={save}
        onDiscard={() => {
          limits.reset();
          rules.reset();
          setError(null);
        }}
      />
    </div>
  );
}
