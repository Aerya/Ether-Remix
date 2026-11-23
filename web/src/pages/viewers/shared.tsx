import { MoreOptions } from "../../components/ConfigSection";
import { Input, SearchSelect, Select, SettingsSection, Switch } from "@braedonsaunders/appkit-ui";
import { ArrowLeft, ArrowRight, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { SettingsRow } from "../../components/SettingsRow";
import { getLocale, localized, t } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { useLocale } from "../../i18n/react";
import { LANGUAGE_TAGS } from "../libraries/browser/CatalogPickers";
import { REGIONAL_TRACK_TAGS, baseLanguage } from "../setup/language";

const BIBLIOGRAPHIC_ISO639: Record<string, string> = {
  alb: "sq",
  arm: "hy",
  baq: "eu",
  bur: "my",
  chi: "zh",
  cze: "cs",
  dut: "nl",
  fre: "fr",
  geo: "ka",
  ger: "de",
  gre: "el",
  ice: "is",
  mac: "mk",
  mao: "mi",
  may: "ms",
  per: "fa",
  rum: "ro",
  slo: "sk",
  tib: "bo",
  wel: "cy",
};

/** Canonicalize an ISO 639 / BCP-47 track code for comparisons without mangling unknown tags. */
function canonicalTrackCode(raw: string): string {
  const trimmed = raw.trim();
  if (!trimmed) return "";
  const mapped = BIBLIOGRAPHIC_ISO639[trimmed.toLowerCase()] ?? trimmed;
  try {
    return Intl.getCanonicalLocales(mapped)[0] ?? mapped.toLowerCase();
  } catch {
    return mapped.toLowerCase();
  }
}

/** Human-readable localized language name for an ISO 639 or regional BCP-47 track tag. */
export function trackLanguageName(code: string, locale = getLocale()): string {
  const trimmed = code.trim();
  if (!trimmed) return "";
  const canonical = canonicalTrackCode(trimmed);
  try {
    return new Intl.DisplayNames([locale], { type: "language" }).of(canonical) ?? trimmed;
  } catch {
    return trimmed;
  }
}

/**
 * Ordered multi-select language picker by name. Reuses the console's
 * `LANGUAGE_TAGS` + `REGIONAL_TRACK_TAGS` vocabulary and AppKit `SearchSelect`,
 * while preserving explicit preference order via numbered chips.
 */
export function OrderedLanguagePicker({
  value,
  onChange,
  ariaLabel,
  disabled,
}: {
  value: string[] | null | undefined;
  onChange: (next: string[]) => void;
  ariaLabel: string;
  disabled?: boolean;
}) {
  const { locale } = useLocale();
  const list = value ?? [];
  const options = useMemo(() => {
    const selectedKeys = new Set(list.map((c) => canonicalTrackCode(c).toLowerCase()));
    const seen = new Set<string>();
    const out: { value: string; label: string }[] = [];
    const push = (code: string) => {
      const key = canonicalTrackCode(code).toLowerCase();
      if (!key || seen.has(key) || selectedKeys.has(key)) return;
      seen.add(key);
      out.push({ value: code, label: trackLanguageName(code, locale) });
    };
    for (const tag of LANGUAGE_TAGS) push(baseLanguage(tag));
    for (const tag of REGIONAL_TRACK_TAGS) push(tag);
    return out.sort((a, b) => a.label.localeCompare(b.label, locale));
  }, [list, locale]);

  const move = (from: number, to: number) => {
    if (to < 0 || to >= list.length) return;
    const next = [...list];
    const [picked] = next.splice(from, 1);
    if (picked !== undefined) {
      next.splice(to, 0, picked);
      onChange(next);
    }
  };

  return (
    <div className="space-y-2">
      {list.length > 0 ? (
        <ol
          aria-label={t("{label} order", { label: ariaLabel })}
          className="flex flex-wrap gap-1.5"
        >
          {list.map((code, idx) => {
            const label = trackLanguageName(code, locale);
            return (
              <li
                key={`${code}-${idx}`}
                className="inline-flex items-center gap-1 rounded-full border border-border bg-bg-subtle py-0.5 pe-1.5 ps-2.5 text-xs text-fg"
              >
                <span className="tabular-nums text-fg-muted">{idx + 1}.</span>
                <span className="font-medium">{label}</span>
                {list.length > 1 ? (
                  <>
                    <button
                      type="button"
                      disabled={disabled || idx === 0}
                      aria-label={t("Move {language} earlier", { language: label })}
                      onClick={() => move(idx, idx - 1)}
                      className="rounded p-0.5 text-fg-muted hover:bg-surface-hover hover:text-fg disabled:opacity-35"
                    >
                      <ArrowLeft size={12} />
                    </button>
                    <button
                      type="button"
                      disabled={disabled || idx === list.length - 1}
                      aria-label={t("Move {language} later", { language: label })}
                      onClick={() => move(idx, idx + 1)}
                      className="rounded p-0.5 text-fg-muted hover:bg-surface-hover hover:text-fg disabled:opacity-35"
                    >
                      <ArrowRight size={12} />
                    </button>
                  </>
                ) : null}
                <button
                  type="button"
                  disabled={disabled}
                  aria-label={t("Remove {language}", { language: label })}
                  onClick={() => onChange(list.filter((_, i) => i !== idx))}
                  className="rounded p-0.5 text-fg-muted hover:bg-danger/10 hover:text-danger disabled:opacity-35"
                >
                  <X size={12} />
                </button>
              </li>
            );
          })}
        </ol>
      ) : null}
      <SearchSelect
        {...searchSelectLabels()}
        value=""
        options={options}
        onChange={(next) => {
          if (next) onChange([...list, next]);
        }}
        ariaLabel={ariaLabel}
        searchable
        disabled={disabled}
        placeholder={list.length > 0 ? t("Add language…") : t("Any language")}
      />
    </div>
  );
}

/** Split a comma-separated list, optionally lower-casing (language codes). */
function parseList(text: string, lower = false): string[] {
  return text
    .split(",")
    .map((s) => (lower ? s.trim().toLowerCase() : s.trim()))
    .filter(Boolean);
}

/**
 * Comma-separated list edited as text. Unlike `ListInput` it reports every
 * keystroke, so the save bar appears while typing rather than after blur.
 */
export function ListText({
  value,
  onChange,
  lower = false,
  placeholder,
  ariaLabel,
  disabled,
}: {
  value: string[] | null | undefined;
  onChange: (v: string[]) => void;
  lower?: boolean;
  placeholder?: string;
  ariaLabel: string;
  disabled?: boolean;
}) {
  const list = value ?? [];
  const [text, setText] = useState(list.join(", "));
  const key = JSON.stringify(list);
  useEffect(() => {
    // Follow outside changes (discard, reset, presets) without fighting typing.
    if (JSON.stringify(parseList(text, lower)) !== key) setText(list.join(", "));
  }, [key]);
  return (
    <Input
      aria-label={ariaLabel}
      value={text}
      disabled={disabled}
      placeholder={placeholder ?? t("Comma-separated")}
      onChange={(e) => {
        setText(e.target.value);
        onChange(parseList(e.target.value, lower));
      }}
    />
  );
}

// ---- Audio & subtitle rules (/stream-preferences/:user; user 0 is the default) ----

type SubtitleAppearance = {
  fontSize: string;
  fontFamily: string;
  fontColor: string;
  backgroundColor: string;
  backgroundStyle: string;
  backgroundOpacity: number;
  textOutline: boolean;
  textOutlineColor: string;
  position: string;
};

const DEFAULT_SUBTITLE_APPEARANCE: SubtitleAppearance = {
  fontSize: "large",
  fontFamily: "sans-serif",
  fontColor: "#ffffff",
  backgroundColor: "#000000",
  backgroundStyle: "box",
  backgroundOpacity: 75,
  textOutline: false,
  textOutlineColor: "#000000",
  position: "bottom",
};

export type StreamRules = {
  audioLanguages: string[];
  subtitleLanguages: string[];
  preferOriginal: boolean;
  subtitleMode: string;
  subtitleStyle: string;
  subtitleAppearance: SubtitleAppearance;
  loudness: boolean;
  nightMode: boolean;
  dialogueBoost: boolean;
};

export type ShowMemory = {
  showId: number;
  showTitle?: string;
  kind: string;
  choice: { language: string; forced: boolean; sdh: boolean; off: boolean };
};

const INITIAL_RULES: StreamRules = {
  audioLanguages: [],
  subtitleLanguages: [],
  preferOriginal: false,
  subtitleMode: "off",
  subtitleStyle: "normal",
  subtitleAppearance: DEFAULT_SUBTITLE_APPEARANCE,
  loudness: false,
  nightMode: false,
  dialogueBoost: false,
};

/** Fill gaps in a server response (older servers send null language lists). */
export function normalizeRules(r: Partial<StreamRules> | null | undefined): StreamRules {
  return {
    ...INITIAL_RULES,
    ...r,
    audioLanguages: r?.audioLanguages ?? [],
    subtitleLanguages: r?.subtitleLanguages ?? [],
    subtitleAppearance: { ...DEFAULT_SUBTITLE_APPEARANCE, ...r?.subtitleAppearance },
  };
}

export function sameRules(a: StreamRules, b: StreamRules): boolean {
  const keys = Object.keys(INITIAL_RULES) as (keyof StreamRules)[];
  return keys.every((k) => JSON.stringify(a[k]) === JSON.stringify(b[k]));
}

const SUBTITLE_MODES = localized(() => [
  { value: "off", label: t("Off unless I turn them on") },
  { value: "forced-only", label: t("Forced tracks only") },
  { value: "foreign-only", label: t("Only for foreign audio") },
  { value: "always", label: t("Always on") },
]);
const SUBTITLE_SIZES = localized(() => [
  { value: "small", label: t("Small") },
  { value: "medium", label: t("Medium") },
  { value: "large", label: t("Large") },
]);
const SUBTITLE_BACKGROUNDS = localized(() => [
  { value: "none", label: t("None") },
  { value: "shadow", label: t("Shadow") },
  { value: "box", label: t("Box") },
  { value: "outline", label: t("Outline") },
]);
const SUBTITLE_POSITIONS = localized(() => [
  { value: "bottom", label: t("Bottom") },
  { value: "center", label: t("Center") },
  { value: "top", label: t("Top") },
]);
const SUBTITLE_STYLES = localized(() => [
  { value: "normal", label: t("Normal") },
  { value: "sdh", label: t("SDH / hearing impaired") },
  { value: "forced", label: t("Forced") },
]);

/** The audio and subtitle choices, shared by the defaults and a viewer's override. */
export function StreamRulesFields({
  value,
  onChange,
  disabled,
}: {
  value: StreamRules;
  onChange: (next: StreamRules) => void;
  disabled?: boolean;
}) {
  const set = <K extends keyof StreamRules>(k: K, v: StreamRules[K]) =>
    onChange({ ...value, [k]: v });
  const appearance = value.subtitleAppearance ?? DEFAULT_SUBTITLE_APPEARANCE;
  const setAppearance = (patch: Partial<SubtitleAppearance>) =>
    set("subtitleAppearance", { ...DEFAULT_SUBTITLE_APPEARANCE, ...appearance, ...patch });
  return (
    <>
      <SettingsSection
        title={t("Languages")}
        description={t("Plex track changes are remembered for later episodes of the same show.")}
      >
        <SettingsRow
          setting="audio-languages"
          title={t("Audio languages")}
          description={t("Tried in order from first to last.")}
          control={
            <div className="w-72 max-w-full">
              <OrderedLanguagePicker
                ariaLabel={t("Audio languages")}
                value={value.audioLanguages}
                onChange={(v) => set("audioLanguages", v)}
                disabled={disabled}
              />
            </div>
          }
        />
        <SettingsRow
          setting="prefer-the-original-language"
          title={t("Prefer the original language")}
          description={t("Play a title's original language before dubs.")}
          control={
            <Switch
              aria-label={t("Prefer the original language")}
              checked={value.preferOriginal}
              disabled={disabled}
              onChange={(e) => set("preferOriginal", e.target.checked)}
            />
          }
        />
        <SettingsRow
          setting="subtitle-languages"
          title={t("Subtitle languages")}
          description={t("Tried in order from first to last.")}
          control={
            <div className="w-72 max-w-full">
              <OrderedLanguagePicker
                ariaLabel={t("Subtitle languages")}
                value={value.subtitleLanguages}
                onChange={(v) => set("subtitleLanguages", v)}
                disabled={disabled}
              />
            </div>
          }
        />
        <SettingsRow
          setting="show-subtitles"
          title={t("Show subtitles")}
          control={
            <div className="w-64">
              <Select
                aria-label={t("Show subtitles")}
                value={value.subtitleMode}
                disabled={disabled}
                onChange={(e) => set("subtitleMode", e.target.value)}
              >
                {SUBTITLE_MODES.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </Select>
            </div>
          }
        />
        <SettingsRow
          setting="subtitle-style"
          title={t("Subtitle style")}
          control={
            <div className="w-64">
              <Select
                aria-label={t("Subtitle style")}
                value={value.subtitleStyle}
                disabled={disabled}
                onChange={(e) => set("subtitleStyle", e.target.value)}
              >
                {SUBTITLE_STYLES.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </Select>
            </div>
          }
        />
      </SettingsSection>
      <SettingsSection
        title={t("Subtitle appearance")}
        description={t("Size, colour, background and position, in apps that can style subtitles.")}
      >
        <MoreOptions
          setting="subtitle-appearance"
          label={t("More options: {title}", { title: t("Subtitle appearance") })}
        >
          <SettingsRow
            setting="size"
            title={t("Size")}
            control={
              <div className="w-64">
                <Select
                  aria-label={t("Size")}
                  value={appearance.fontSize}
                  disabled={disabled}
                  onChange={(e) => setAppearance({ fontSize: e.target.value })}
                >
                  {SUBTITLE_SIZES.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </Select>
              </div>
            }
          />
          <SettingsRow
            setting="text-colour"
            title={t("Text colour")}
            description={t("A hex colour, such as #ffffff.")}
            control={
              <div className="w-40">
                <Input
                  aria-label={t("Text colour")}
                  value={appearance.fontColor}
                  disabled={disabled}
                  placeholder="#ffffff"
                  onChange={(e) => setAppearance({ fontColor: e.target.value })}
                />
              </div>
            }
          />
          <SettingsRow
            setting="background"
            title={t("Background")}
            control={
              <div className="flex w-72 items-center gap-2">
                <Select
                  aria-label={t("Background")}
                  value={appearance.backgroundStyle}
                  disabled={disabled}
                  onChange={(e) => setAppearance({ backgroundStyle: e.target.value })}
                >
                  {SUBTITLE_BACKGROUNDS.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </Select>
                <Input
                  aria-label={t("Background colour")}
                  value={appearance.backgroundColor}
                  disabled={disabled}
                  placeholder="#000000"
                  onChange={(e) => setAppearance({ backgroundColor: e.target.value })}
                />
              </div>
            }
          />
          <SettingsRow
            setting="position"
            title={t("Position")}
            control={
              <div className="w-64">
                <Select
                  aria-label={t("Position")}
                  value={appearance.position}
                  disabled={disabled}
                  onChange={(e) => setAppearance({ position: e.target.value })}
                >
                  {SUBTITLE_POSITIONS.map((o) => (
                    <option key={o.value} value={o.value}>
                      {o.label}
                    </option>
                  ))}
                </Select>
              </div>
            }
          />
        </MoreOptions>
      </SettingsSection>
      <SettingsSection
        title={t("Audio processing")}
        description={t(
          "Needs server audio transcoding. Dialogue boost applies to sources with six or more channels.",
        )}
      >
        <MoreOptions
          setting="audio-processing"
          label={t("More options: {title}", { title: t("Audio processing") })}
        >
          {(
            [
              ["loudness", t("Normalize loudness")],
              ["nightMode", t("Night mode compression")],
              ["dialogueBoost", t("Boost centre-channel dialogue")],
            ] as const
          ).map(([key, label]) => (
            <SettingsRow
              key={key}
              setting={
                {
                  loudness: "normalize-loudness",
                  nightMode: "night-mode-compression",
                  dialogueBoost: "boost-centre-channel-dialogue",
                }[key]
              }
              title={label}
              control={
                <Switch
                  aria-label={label}
                  checked={value[key]}
                  disabled={disabled}
                  onChange={(e) => set(key, e.target.checked)}
                />
              }
            />
          ))}
        </MoreOptions>
      </SettingsSection>
    </>
  );
}

// ---- Safety policy (/safety/policies/:user) ----------------------------------------

export type SafetyPolicy = {
  bedtime?: { time: string; wakeTime: string; timezone: string; days?: number[] } | null;
  blockedGenres: string[];
  blockedKeywords: string[];
  remoteDisabled: boolean;
  maxBitrateKbps: number;
  videoTranscodingDisabled?: boolean;
  /** Owner override for offline downloads; absent follows the server setting. */
  allowDownloads?: boolean | null;
  /** Downloads only from releases that need no video conversion. */
  downloadTranscodingDisabled?: boolean;
  /** "" follows the server, "none" is no limit, else what the limit counts. */
  downloadLimitUnit?: "" | "none" | "titles" | "gb";
  downloadLimitAmount?: number;
  downloadLimitPeriod?: "" | "day" | "month";
  /** Highest download resolution for this viewer; 0 follows the server. */
  downloadMaxHeight?: number;
  allowedHours: number[];
  timezone: string;
};

function emptyPolicy(): SafetyPolicy {
  return {
    blockedGenres: [],
    blockedKeywords: [],
    remoteDisabled: false,
    maxBitrateKbps: 0,
    allowedHours: [],
    timezone: "",
  };
}

export function normalizePolicy(p: Partial<SafetyPolicy> | null | undefined): SafetyPolicy {
  const base = emptyPolicy();
  const out: SafetyPolicy = {
    ...base,
    ...p,
    blockedGenres: p?.blockedGenres ?? [],
    blockedKeywords: p?.blockedKeywords ?? [],
    allowedHours: [...(p?.allowedHours ?? [])].sort((a, b) => a - b),
    timezone:
      p?.bedtime?.timezone || p?.timezone || (p?.allowedHours?.length ? "UTC" : base.timezone),
  };
  if (p?.allowDownloads === true || p?.allowDownloads === false) {
    out.allowDownloads = p.allowDownloads;
  } else {
    delete out.allowDownloads;
  }
  return out;
}

export const KID_PRESETS = localized(
  () =>
    [
      { value: "preschool", label: t("Young children") },
      { value: "family", label: t("Family") },
      { value: "teens", label: t("Teens") },
    ] as const,
);
