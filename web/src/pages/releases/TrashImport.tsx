import {
  Alert,
  AlertDescription,
  AlertTitle,
  Badge,
  Button,
  Input,
  SearchSelect,
  SettingsSection,
  Switch,
  Textarea,
  toast,
} from "@braedonsaunders/appkit-ui";
import {
  BookOpen,
  Check,
  ChevronLeft,
  Database,
  ExternalLink,
  FilePlus2,
  FileUp,
  Pencil,
  RefreshCw,
  Sparkles,
  Trash2,
  Upload,
} from "lucide-react";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { LoadingInline } from "../../components/feedback";
import { Segmented } from "../../components/fields";
import { SettingsRow } from "../../components/SettingsRow";
import { localized, t, tn } from "../../i18n";
import { searchSelectLabels } from "../../i18n/appkit-labels";
import { useLocale } from "../../i18n/react";
import { api, ApiError, errorMessage, isAbort } from "../../lib/api";
import { formatNumber } from "../../lib/format";
import { useAsync } from "../../lib/hooks";
import { itemName } from "../../lib/qualityRanking";
import {
  appForMedia,
  qualityGateLabel,
  releaseConsole,
  type ImportFormat,
  type ProfileMedia,
  type QualityImportPlan,
  type QualityImportSource,
  type StarrApp,
  type TrashProfileList,
} from "../../lib/releases";
import type { EtherConfig, ProfileDatabase, QualityItem, QualityProfile } from "../../lib/types";
import { RecordDrawer, TextRow } from "../sources/form";

type Step = "start" | "preset" | "trash" | "profilarr" | "blank" | "paste";

/** A pasted profile is a few KiB; the server takes up to 1 MiB. */
const MAX_PASTE = 1 << 20;

const FOR_OPTIONS = localized(() => [
  { value: "radarr" as const, label: t("Movies (Radarr)") },
  { value: "sonarr" as const, label: t("Episodes (Sonarr)") },
]);

const STATUS: Record<
  ImportFormat["status"],
  { label: string; variant: "secondary" | "success" | "warning" }
> = localized(() => ({
  present: { label: t("Already saved"), variant: "secondary" },
  add: { label: t("Added"), variant: "success" },
  unsupported: { label: t("Can't import"), variant: "warning" },
  missing: { label: t("Not found"), variant: "warning" },
}));

const BLANK_HEIGHTS = [2160, 1080, 720, 576, 480, 360];
const BLANK_SOURCES = ["remux", "bluray", "webdl", "webrip", "hdtv", "dvd", "unknown"];

function defaultBlankOrder(): QualityItem[] {
  return BLANK_HEIGHTS.filter((h) => h >= 720).map((h) => ({
    name: h === 2160 ? "4K (2160p)" : `${h}p`,
    qualities: BLANK_SOURCES.map((src) => `${src}-${h}`),
  }));
}

function makeBlankProfile(name: string): QualityProfile {
  return {
    name,
    maxHeight: 2160,
    minHeight: 720,
    allowHdr: true,
    allowDolbyVision: true,
    minSizeMbPerMin: 5,
    maxSizeMbPerMin: 250,
    preferredLanguages: null,
    audioLanguageMode: "preferred",
    allowCam: false,
    qualityOrder: defaultBlankOrder(),
  };
}

function uniqueProfileName(base: string, profiles: QualityProfile[] | null | undefined): string {
  const list = profiles ?? [];
  const taken = (n: string) => list.some((p) => p.name === n);
  if (!taken(base)) return base;
  for (let i = 2; ; i++) {
    const candidate = `${base} (${i})`;
    if (!taken(candidate)) return candidate;
  }
}

function resolutionLabel(h: number): string {
  return h >= 2160 ? "4K" : `${h}p`;
}

function resolution(p: QualityProfile): string {
  if (!p.maxHeight) return t("Any resolution");
  if (!p.minHeight || p.minHeight === p.maxHeight) return resolutionLabel(p.maxHeight);
  return t("{min} to {max}", {
    min: resolutionLabel(p.minHeight),
    max: resolutionLabel(p.maxHeight),
  });
}

function presetHint(p: QualityProfile): string {
  const parts = [resolution(p)];
  parts.push(p.allowDolbyVision || p.allowHdr ? "HDR" : "SDR");
  const scores = p.customFormatScores ?? {};
  if ((scores["ether:remux"] ?? 0) > (scores["ether:webdl"] ?? 0)) {
    parts.push(t("Remux first"));
  } else if (p.maxHeight <= 720) {
    parts.push(t("small files"));
  } else {
    parts.push(t("WEB-DL first"));
  }
  return parts.join(" · ");
}

function shortRepo(repo: string): string {
  return repo.replace(/^https?:\/\//i, "").replace(/\/+$/, "");
}

function slugDatabaseId(raw: string): string {
  return raw
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function deriveDatabaseMeta(repoRaw: string, branchRaw: string): ProfileDatabase {
  const trimmed = repoRaw.trim();
  let branch = branchRaw.trim();
  try {
    const u = new URL(trimmed);
    if (u.hostname.toLowerCase() === "github.com") {
      const parts = u.pathname
        .replace(/^\/+|\/+$/g, "")
        .split("/")
        .filter(Boolean);
      const owner = parts[0] ?? "custom";
      const repoName = (parts[1] ?? "database").replace(/\.git$/i, "");
      if (!branch && parts.length >= 4 && parts[2] === "tree") {
        branch = parts.slice(3).join("/");
      }
      return {
        id: slugDatabaseId(`${owner}-${repoName}`),
        name: repoName,
        repo: `https://github.com/${owner}/${repoName}`,
        branch: branch || "v2",
      };
    }
    const base = u.pathname.split("/").pop() ?? "database";
    const name = base.replace(/\.(tar\.gz|tgz)$/i, "") || u.hostname;
    return {
      id: slugDatabaseId(`${u.hostname}-${name}`),
      name,
      repo: trimmed,
      ...(branch ? { branch } : {}),
    };
  } catch {
    return {
      id: slugDatabaseId(trimmed) || "custom-db",
      name: trimmed,
      repo: trimmed,
      branch: branch || "v2",
    };
  }
}

function signed(n: number): string {
  return n > 0 ? `+${formatNumber(n)}` : formatNumber(n);
}

function firstSentence(text: string | undefined): string {
  if (!text) return "";
  const clean = text.trim().split(/\n+/)[0] ?? "";
  return clean.length > 110 ? `${clean.slice(0, 107)}…` : clean;
}

function StartCard({
  icon,
  title,
  body,
  onClick,
}: {
  icon: ReactNode;
  title: string;
  body: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex items-start gap-3 rounded-xl border border-border bg-surface p-4 text-left transition-colors hover:border-primary/40 hover:bg-primary/5"
    >
      <span className="mt-0.5 text-primary [&>svg]:size-5">{icon}</span>
      <span>
        <span className="block text-sm font-medium text-fg">{title}</span>
        <span className="mt-0.5 block text-xs text-fg-muted">{body}</span>
      </span>
    </button>
  );
}

function PickList({
  items,
  value,
  onPick,
  label,
}: {
  items: { id: string; name: string; hint: string }[];
  value?: string;
  onPick: (id: string) => void;
  label: string;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="grid gap-2 sm:grid-cols-2">
      {items.map((p) => {
        const on = p.id === value;
        return (
          <button
            key={p.id}
            type="button"
            role="radio"
            aria-checked={on}
            onClick={() => onPick(p.id)}
            className={`flex items-start justify-between gap-2 rounded-lg border px-3 py-2 text-left transition-colors ${
              on ? "border-primary/50 bg-primary/5" : "border-border hover:border-border-strong"
            }`}
          >
            <span className="min-w-0">
              <span className="block text-sm font-medium text-fg">{p.name}</span>{" "}
              {p.hint ? <span className="block text-xs text-fg-muted">{p.hint}</span> : null}
            </span>
            {on ? <Check size={15} className="mt-0.5 shrink-0 text-primary" /> : null}
          </button>
        );
      })}
    </div>
  );
}

/**
 * Unified Add profile drawer: start from an Ether preset, TRaSH Guides,
 * a Profilarr database (official Dictionarry or a custom GitHub database),
 * a blank profile, or a pasted/uploaded profile file.
 */
export function TrashImport({
  open,
  onClose,
  onImported,
  media,
}: {
  open: boolean;
  media?: ProfileMedia | string;
  onClose: () => void;
  onImported: () => Promise<void> | void;
}) {
  const navigate = useNavigate();
  const { locale } = useLocale();
  const [step, setStep] = useState<Step>("start");
  const [app, setApp] = useState<StarrApp>(() => appForMedia(media) ?? "radarr");
  const [config, setConfig] = useState<EtherConfig | null>(null);

  // Profilarr custom database state
  const [dbId, setDbId] = useState("");
  const [dbFormMode, setDbFormMode] = useState<"add" | "manage" | null>(null);
  const [editingDbId, setEditingDbId] = useState<string | null>(null);
  const [repoInput, setRepoInput] = useState("");
  const [branchInput, setBranchInput] = useState("");
  const [checkingDb, setCheckingDb] = useState(false);
  const [dbCheckError, setDbCheckError] = useState<string | null>(null);
  const [dbCheckedCount, setDbCheckedCount] = useState<number | null>(null);

  // Selection / preview state
  const [presetName, setPresetNameState] = useState("");
  // The default-preset effect below can run after a click has already
  // queued a pick (its render closure is stale then), so it reads the
  // latest pick from this ref instead of from state.
  const presetPick = useRef("");
  const setPresetName = (value: string) => {
    presetPick.current = value;
    setPresetNameState(value);
  };
  const [profileId, setProfileId] = useState("");
  const [pasted, setPasted] = useState("");
  const [plan, setPlan] = useState<QualityImportPlan | null>(null);
  const [source, setSource] = useState<QualityImportSource | null>(null);
  const [name, setName] = useState("");
  const [keepInSync, setKeepInSync] = useState(true);
  const [previewing, setPreviewing] = useState(false);
  const [previewError, setPreviewError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [nameError, setNameError] = useState<string>();

  const preview = useRef<AbortController | null>(null);
  const file = useRef<HTMLInputElement>(null);

  const databases = config?.quality.databases ?? [];
  const existingProfiles = config?.quality.profiles ?? [];

  // Load current config when drawer opens so custom databases and existing names are fresh.
  useEffect(() => {
    if (!open) return;
    let active = true;
    setStep("start");
    setApp(appForMedia(media) ?? "radarr");
    setDbId("");
    setDbFormMode(null);
    setEditingDbId(null);
    setRepoInput("");
    setBranchInput("");
    setCheckingDb(false);
    setDbCheckError(null);
    setDbCheckedCount(null);
    setPresetName("");
    setProfileId("");
    setPasted("");
    setKeepInSync(true);
    clearPreview();
    setSaveError(null);
    void api
      .getConfig()
      .then((cfg) => {
        if (active) setConfig(cfg);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [open]);

  const presetsQuery = useAsync(
    (signal) => (open && step === "preset" ? releaseConsole.presets(signal) : Promise.resolve([])),
    [open, step],
  );
  const presets = presetsQuery.data ?? [];
  const chosenPreset = presets.find((p) => p.name === presetName) ?? presets[0];

  useEffect(() => {
    if (step !== "preset" || !presets.length) return;
    const picked = presetPick.current;
    if (!picked || !presets.some((p) => p.name === picked)) {
      const first = presets[0]!;
      setPresetName(first.name);
      setName(uniqueProfileName(first.name, existingProfiles));
      setNameError(undefined);
    }
  }, [step, presets, presetName]);

  const listing = open && (step === "trash" || (step === "profilarr" && dbFormMode === null));
  const profiles = useAsync(
    (signal) => {
      if (!listing) return Promise.resolve<TrashProfileList>({ items: [] });
      return step === "profilarr"
        ? releaseConsole.dictionarryProfiles(app, dbId || undefined, signal)
        : releaseConsole.trashProfiles(app, signal);
    },
    [listing, step, app, dbId],
  );
  const list = useMemo(
    () => (listing ? (profiles.data?.items ?? []) : []),
    [listing, profiles.data],
  );
  const chosen = list.find((p) => p.id === profileId);

  const pickItems = useMemo(
    () =>
      list.map((p) => {
        const formatHint =
          step === "trash" && p.defaultFormats != null
            ? t("{own} own · {defaults} default", {
                own: formatNumber(p.formats),
                defaults: formatNumber(p.defaultFormats),
              })
            : tn(p.formats, "{count} custom format", "{count} custom formats");
        const desc = firstSentence(p.description);
        return {
          id: p.id,
          name: p.name,
          hint: step === "profilarr" && desc ? desc : formatHint,
        };
      }),
    [list, step, locale],
  );

  const dbOptions = useMemo(
    () => [
      { value: "dictionarry", label: t("Dictionarry (official)") },
      ...databases.map((db) => ({
        value: db.id,
        label: `${db.name} (${shortRepo(db.repo)})`,
      })),
      { value: "__add", label: t("Add a database…") },
      ...(databases.length ? [{ value: "__manage", label: t("Manage databases…") }] : []),
    ],
    [databases, locale],
  );

  function clearPreview() {
    preview.current?.abort();
    setPlan(null);
    setSource(null);
    setPreviewError(null);
    setPreviewing(false);
    setNameError(undefined);
  }

  function goStart() {
    clearPreview();
    setProfileId("");
    setSaveError(null);
    setDbFormMode(null);
    setEditingDbId(null);
    setDbCheckError(null);
    setStep("start");
  }

  async function load(next: QualityImportSource) {
    preview.current?.abort();
    const ctl = new AbortController();
    preview.current = ctl;
    setPlan(null);
    setSource(null);
    setPreviewError(null);
    setSaveError(null);
    setNameError(undefined);
    setPreviewing(true);
    try {
      const p = await releaseConsole.previewImport(next, ctl.signal);
      if (ctl.signal.aborted) return;
      setPlan(p);
      setSource({ ...next, app: p.app } as QualityImportSource);
      setName(p.profile.name);
      setKeepInSync(true);
    } catch (e) {
      if (!isAbort(e) && !ctl.signal.aborted) setPreviewError(e);
    } finally {
      if (preview.current === ctl) setPreviewing(false);
    }
  }

  function pick(id: string) {
    setProfileId(id);
    if (!id) {
      clearPreview();
      return;
    }
    if (step === "profilarr") {
      void load({
        app,
        source: "dictionarry",
        ...(dbId ? { database: dbId } : {}),
        profile: id,
      });
    } else {
      void load({ app, profile: id });
    }
  }

  async function upload(f: File | undefined) {
    if (!f) return;
    if (f.size > MAX_PASTE) {
      setPreviewError(
        new Error(t("That file is larger than 1 MiB; choose a TRaSH quality profile file.")),
      );
      return;
    }
    const text = await f.text();
    setPasted(text);
    void load({ app, json: text });
  }

  async function checkAndSaveDatabase() {
    const rawRepo = repoInput.trim();
    if (!rawRepo) {
      setDbCheckError(t("Enter a GitHub repository URL."));
      return;
    }
    setCheckingDb(true);
    setDbCheckError(null);
    setDbCheckedCount(null);
    try {
      const res = await releaseConsole.checkQualityDatabase(
        rawRepo,
        branchInput.trim() || undefined,
      );
      if (!res.ok) {
        setDbCheckError(res.error || t("Could not read that database."));
        return;
      }
      const derived = deriveDatabaseMeta(rawRepo, branchInput);
      const entry: ProfileDatabase = {
        id: res.id || derived.id,
        name: res.name || derived.name,
        repo: res.repo || derived.repo,
        branch: res.branch ?? derived.branch,
      };
      const latest = await api.getConfig();
      const prev = latest.quality.databases ?? [];
      const replaceId = editingDbId ?? entry.id;
      const existsIdx = prev.findIndex((d) => d.id === replaceId || d.id === entry.id);
      const nextDbs =
        existsIdx >= 0 ? prev.map((d, idx) => (idx === existsIdx ? entry : d)) : [...prev, entry];
      await api.putSection("quality", { ...latest.quality, databases: nextDbs });
      const updated = await api.getConfig();
      setConfig(updated);
      setDbCheckedCount(res.profiles ?? 0);
      setDbId(entry.id);
      setProfileId("");
      clearPreview();
      setEditingDbId(null);
      setDbFormMode(null);
    } catch (e) {
      setDbCheckError(errorMessage(e));
    } finally {
      setCheckingDb(false);
    }
  }

  async function removeDatabase(id: string) {
    setCheckingDb(true);
    setDbCheckError(null);
    try {
      const latest = await api.getConfig();
      const nextDbs = (latest.quality.databases ?? []).filter((d) => d.id !== id);
      await api.putSection("quality", { ...latest.quality, databases: nextDbs });
      const updated = await api.getConfig();
      setConfig(updated);
      if (dbId === id) {
        setDbId("");
        setProfileId("");
        clearPreview();
      }
      if (!nextDbs.length) setDbFormMode(null);
    } catch (e) {
      setDbCheckError(errorMessage(e));
    } finally {
      setCheckingDb(false);
    }
  }

  function finishAdded(finalName: string) {
    toast.success(t("Profile added"), {
      action: {
        label: t("Use it in a rule →"),
        onClick: () => navigate(`/releases/rules?new=1&profile=${encodeURIComponent(finalName)}`),
      },
    });
  }

  async function saveDirectProfile(template: QualityProfile) {
    const finalName = name.trim();
    if (!finalName) {
      setNameError(t("Enter a name."));
      return;
    }
    setSaving(true);
    setSaveError(null);
    setNameError(undefined);
    try {
      const latest = await api.getConfig();
      const listNow = latest.quality.profiles ?? [];
      if (listNow.some((p) => p.name === finalName)) {
        setNameError(
          t('A profile named "{name}" already exists; choose another name.', { name: finalName }),
        );
        return;
      }
      const nextProfile: QualityProfile = { ...template, name: finalName };
      delete nextProfile.import;
      await api.putSection("quality", {
        ...latest.quality,
        profiles: [...listNow, nextProfile],
      });
      finishAdded(finalName);
      await onImported();
      onClose();
    } catch (e) {
      setSaveError(e);
    } finally {
      setSaving(false);
    }
  }

  async function save() {
    if (step === "preset") {
      if (!chosenPreset) return;
      await saveDirectProfile(chosenPreset);
      return;
    }
    if (step === "blank") {
      await saveDirectProfile(makeBlankProfile(name.trim()));
      return;
    }
    if (!plan || !source) return;
    const finalName = name.trim();
    if (!finalName) {
      setNameError(t("Enter a name."));
      return;
    }
    setSaving(true);
    setSaveError(null);
    setNameError(undefined);
    try {
      const payload: QualityImportSource =
        !keepInSync && plan.source ? { ...source, sync: false } : source;
      await releaseConsole.importProfile(payload, finalName);
      finishAdded(finalName);
      await onImported();
      onClose();
    } catch (e) {
      if (e instanceof ApiError && e.code === "exists") setNameError(e.message);
      else setSaveError(e);
    } finally {
      setSaving(false);
    }
  }

  const activeCustomDb = databases.find((d) => d.id === dbId);
  const syncSourceName =
    step === "profilarr"
      ? (activeCustomDb?.name ?? "Dictionarry")
      : plan?.source === "dictionarry"
        ? (activeCustomDb?.name ?? "Dictionarry")
        : "TRaSH Guides";

  const ready =
    step === "preset"
      ? Boolean(chosenPreset && !presetsQuery.loading)
      : step === "blank"
        ? true
        : step === "trash" || step === "profilarr" || step === "paste"
          ? Boolean(plan && !previewing && !plan.unusable)
          : false;

  const backButton = (
    <button
      type="button"
      onClick={goStart}
      className="inline-flex items-center gap-1 text-sm text-fg-muted hover:text-fg"
    >
      <ChevronLeft size={15} /> {t("Start from")}
    </button>
  );

  return (
    <RecordDrawer
      open={open}
      onClose={() => !saving && onClose()}
      title={t("Add profile")}
      onSave={() => void save()}
      saving={saving}
      saveLabel={t("Add profile")}
      saveDisabled={!ready}
      error={saveError}
    >
      {step === "start" ? (
        <div className="space-y-4">
          <p className="text-sm font-medium text-fg">{t("Start from")}</p>
          <div className="grid gap-3 sm:grid-cols-2">
            <StartCard
              icon={<Sparkles />}
              title={t("Ether preset")}
              body={t("4K, 1080p or 720p. Good starting points.")}
              onClick={() => {
                setPresetName("");
                setNameError(undefined);
                setStep("preset");
              }}
            />
            <StartCard
              icon={<BookOpen />}
              title={t("TRaSH Guides")}
              body={t(
                "Community profiles written for Radarr (movies) and Sonarr (episodes). Ether downloads them; nothing to install.",
              )}
              onClick={() => {
                setProfileId("");
                clearPreview();
                setStep("trash");
              }}
            />
            <StartCard
              icon={<Database />}
              title={t("Profilarr database")}
              body={t(
                "Dictionarry’s public profile database, the one the Profilarr app uses. Ether downloads it; you don’t need Profilarr.",
              )}
              onClick={() => {
                setProfileId("");
                setDbFormMode(null);
                clearPreview();
                setStep("profilarr");
              }}
            />
            <StartCard
              icon={<FilePlus2 />}
              title={t("Blank")}
              body={t("Start from nothing and set every tier yourself.")}
              onClick={() => {
                setName(uniqueProfileName(t("Custom"), existingProfiles));
                setNameError(undefined);
                setStep("blank");
              }}
            />
          </div>
          <button
            type="button"
            onClick={() => {
              clearPreview();
              setStep("paste");
            }}
            className="inline-flex items-center gap-1.5 text-sm text-primary hover:underline"
          >
            <Upload size={14} /> {t("Paste or upload a profile file")}
          </button>
        </div>
      ) : null}

      {step === "preset" ? (
        <div className="space-y-4">
          {backButton}
          <SettingsSection title={t("Ether preset")}>
            <div className="px-5 py-3">
              {presetsQuery.loading ? (
                <LoadingInline label={t("Loading presets…")} />
              ) : presetsQuery.error ? (
                <div className="space-y-2">
                  <p className="text-xs text-danger">{errorMessage(presetsQuery.error)}</p>
                  <Button variant="outline" size="sm" onClick={() => void presetsQuery.reload()}>
                    <RefreshCw size={14} /> {t("Try again")}
                  </Button>
                </div>
              ) : (
                <PickList
                  label={t("Preset")}
                  items={presets.map((p) => ({
                    id: p.name,
                    name: p.name,
                    hint: presetHint(p),
                  }))}
                  value={chosenPreset?.name}
                  onPick={(picked) => {
                    setPresetName(picked);
                    setName(uniqueProfileName(picked, existingProfiles));
                    setNameError(undefined);
                  }}
                />
              )}
            </div>
          </SettingsSection>
          {chosenPreset ? (
            <SettingsSection
              title={t("New profile")}
              description={t("Edit it like any other profile once added.")}
            >
              <TextRow
                label={t("Name")}
                value={name}
                error={nameError}
                onChange={(v) => {
                  setName(v);
                  setNameError(undefined);
                }}
                help={t("Must differ from your other profiles.")}
              />
              <SettingsRow stacked title={t("What it brings")}>
                <div className="flex flex-wrap gap-1.5">
                  <Badge variant="secondary">{resolution(chosenPreset)}</Badge>
                  <Badge variant="outline">
                    {chosenPreset.allowDolbyVision
                      ? t("HDR and Dolby Vision")
                      : chosenPreset.allowHdr
                        ? t("HDR allowed")
                        : t("SDR only")}
                  </Badge>
                  {(chosenPreset.qualityOrder ?? []).slice(0, 3).map((item) => (
                    <Badge key={itemName(item)} variant="outline">
                      {itemName(item)}
                    </Badge>
                  ))}
                </div>
              </SettingsRow>
            </SettingsSection>
          ) : null}
        </div>
      ) : null}

      {step === "blank" ? (
        <div className="space-y-4">
          {backButton}
          <SettingsSection
            title={t("New profile")}
            description={t("Edit it like any other profile once added.")}
          >
            <TextRow
              label={t("Name")}
              value={name}
              error={nameError}
              onChange={(v) => {
                setName(v);
                setNameError(undefined);
              }}
              help={t("Must differ from your other profiles.")}
            />
          </SettingsSection>
        </div>
      ) : null}

      {step === "profilarr" ? (
        <div className="space-y-4">
          {backButton}
          <SettingsSection title={t("Profilarr database")}>
            <SettingsRow
              title={t("Database")}
              description={
                dbCheckedCount != null && dbFormMode === null ? (
                  <span className="inline-flex items-center gap-1 text-success">
                    <Check size={13} />{" "}
                    {tn(dbCheckedCount, "{count} profile found", "{count} profiles found")}
                  </span>
                ) : (
                  t("Ether downloads it from GitHub. You don’t need Profilarr.")
                )
              }
              control={
                <SearchSelect
                  {...searchSelectLabels()}
                  ariaLabel={t("Database")}
                  className="w-64"
                  value={
                    dbFormMode === "add"
                      ? "__add"
                      : dbFormMode === "manage"
                        ? "__manage"
                        : dbId || "dictionarry"
                  }
                  options={dbOptions}
                  onChange={(v) => {
                    setDbCheckError(null);
                    setDbCheckedCount(null);
                    if (v === "__add") {
                      setEditingDbId(null);
                      setRepoInput("");
                      setBranchInput("");
                      setProfileId("");
                      clearPreview();
                      setDbFormMode("add");
                      return;
                    }
                    if (v === "__manage") {
                      setEditingDbId(null);
                      setProfileId("");
                      clearPreview();
                      setDbFormMode("manage");
                      return;
                    }
                    setDbFormMode(null);
                    setEditingDbId(null);
                    const nextDb = v === "dictionarry" ? "" : v;
                    setDbId(nextDb);
                    setProfileId("");
                    clearPreview();
                  }}
                />
              }
            />
            {dbFormMode === "add" ? (
              <>
                <SettingsRow
                  title={t("GitHub repository")}
                  description={t("A Profilarr database, for example a fork of Dictionarry’s.")}
                  control={
                    <Input
                      aria-label={t("GitHub repository")}
                      className="w-full sm:w-72"
                      placeholder="https://github.com/owner/repo"
                      value={repoInput}
                      onChange={(e) => {
                        setRepoInput(e.target.value);
                        setDbCheckError(null);
                      }}
                    />
                  }
                />
                <SettingsRow
                  title={t("Branch")}
                  control={
                    <Input
                      aria-label={t("Branch")}
                      className="w-32"
                      placeholder="v2"
                      value={branchInput}
                      onChange={(e) => {
                        setBranchInput(e.target.value);
                        setDbCheckError(null);
                      }}
                    />
                  }
                />
                <SettingsRow
                  title={t("Check it")}
                  description={
                    dbCheckError ? <span className="text-danger">{dbCheckError}</span> : undefined
                  }
                  control={
                    <span className="inline-flex items-center gap-3">
                      {dbCheckedCount != null ? (
                        <span className="inline-flex items-center gap-1 text-sm text-success">
                          <Check size={14} />{" "}
                          {tn(dbCheckedCount, "{count} profile found", "{count} profiles found")}
                        </span>
                      ) : null}
                      <Button
                        size="sm"
                        variant="outline"
                        disabled={checkingDb || !repoInput.trim()}
                        onClick={() => void checkAndSaveDatabase()}
                      >
                        {checkingDb ? t("Checking…") : t("Check and save")}
                      </Button>
                    </span>
                  }
                />
              </>
            ) : null}
            {dbFormMode === "manage" ? (
              <div className="divide-y divide-border px-5 py-1">
                {databases.map((db) => (
                  <div key={db.id} className="flex items-center justify-between gap-3 py-2.5">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium text-fg">{db.name}</p>
                      <p className="truncate text-xs text-fg-muted">
                        {db.repo}
                        {db.branch ? ` · ${db.branch}` : ""}
                      </p>
                    </div>
                    <div className="flex shrink-0 items-center gap-1.5">
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={t("Edit {name}", { name: db.name })}
                        onClick={() => {
                          setEditingDbId(db.id);
                          setRepoInput(db.repo);
                          setBranchInput(db.branch ?? "");
                          setDbCheckError(null);
                          setDbFormMode("add");
                        }}
                      >
                        <Pencil size={14} />
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        disabled={checkingDb}
                        aria-label={t("Remove {name}", { name: db.name })}
                        onClick={() => void removeDatabase(db.id)}
                      >
                        <Trash2 size={14} />
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            ) : null}
          </SettingsSection>

          {dbFormMode === null ? (
            <SettingsSection title={t("Profile")}>
              <SettingsRow
                title={t("For")}
                description={t("Either works for both. The guide is written for one.")}
                control={
                  <Segmented
                    size="sm"
                    ariaLabel={t("For")}
                    value={app}
                    options={FOR_OPTIONS}
                    onChange={(nextApp) => {
                      setApp(nextApp);
                      setProfileId("");
                      clearPreview();
                    }}
                  />
                }
              />
              <div className="space-y-2 px-5 py-3">
                {profiles.loading && list.length === 0 ? (
                  <LoadingInline label={t("Loading profiles…")} />
                ) : profiles.error ? (
                  <div className="space-y-2">
                    <p className="text-xs text-danger">{errorMessage(profiles.error)}</p>
                    <Button variant="outline" size="sm" onClick={() => void profiles.reload()}>
                      <RefreshCw size={14} /> {t("Try again")}
                    </Button>
                  </div>
                ) : (
                  <PickList
                    label={t("Profile")}
                    items={pickItems}
                    value={profileId}
                    onPick={pick}
                  />
                )}
                {profiles.data?.warning ? (
                  <Alert variant="warning">
                    <AlertTitle>{t("Some guide groups didn't load")}</AlertTitle>
                    <AlertDescription>{profiles.data.warning}</AlertDescription>
                  </Alert>
                ) : null}
                {chosen?.description ? (
                  <p className="whitespace-pre-line text-xs text-fg-muted">{chosen.description}</p>
                ) : null}
                {chosen?.url?.startsWith("https://dictionarry.dev") ? (
                  <a
                    href={chosen.url}
                    target="_blank"
                    rel="noreferrer noopener"
                    className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
                  >
                    {t("Read the guide")} <ExternalLink size={12} />
                  </a>
                ) : null}
              </div>
            </SettingsSection>
          ) : null}
        </div>
      ) : null}

      {step === "trash" ? (
        <div className="space-y-4">
          {backButton}
          <SettingsSection title={t("TRaSH Guides")}>
            <SettingsRow
              title={t("For")}
              description={t("Either works for both. The guide is written for one.")}
              control={
                <Segmented
                  size="sm"
                  ariaLabel={t("For")}
                  value={app}
                  options={FOR_OPTIONS}
                  onChange={(nextApp) => {
                    setApp(nextApp);
                    setProfileId("");
                    clearPreview();
                  }}
                />
              }
            />
            <div className="space-y-2 px-5 py-3">
              {profiles.loading && list.length === 0 ? (
                <LoadingInline label={t("Loading profiles…")} />
              ) : profiles.error ? (
                <div className="space-y-2">
                  <p className="text-xs text-danger">{errorMessage(profiles.error)}</p>
                  <Button variant="outline" size="sm" onClick={() => void profiles.reload()}>
                    <RefreshCw size={14} /> {t("Try again")}
                  </Button>
                </div>
              ) : (
                <PickList label={t("Profile")} items={pickItems} value={profileId} onPick={pick} />
              )}
              {profiles.data?.warning ? (
                <Alert variant="warning">
                  <AlertTitle>{t("Some guide groups didn't load")}</AlertTitle>
                  <AlertDescription>{profiles.data.warning}</AlertDescription>
                </Alert>
              ) : null}
              {chosen?.description ? (
                <p className="whitespace-pre-line text-xs text-fg-muted">{chosen.description}</p>
              ) : null}
              {chosen?.url?.startsWith("https://trash-guides.info/") ? (
                <a
                  href={chosen.url}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="inline-flex items-center gap-1 text-xs text-primary hover:underline"
                >
                  {t("Read the guide")} <ExternalLink size={12} />
                </a>
              ) : null}
            </div>
          </SettingsSection>
        </div>
      ) : null}

      {step === "paste" ? (
        <div className="space-y-4">
          {backButton}
          <SettingsSection title={t("Paste or upload a profile file")}>
            <SettingsRow
              title={t("For")}
              description={t("Either works for both. The guide is written for one.")}
              control={
                <Segmented
                  size="sm"
                  ariaLabel={t("For")}
                  value={app}
                  options={FOR_OPTIONS}
                  onChange={(nextApp) => {
                    setApp(nextApp);
                    if (plan || previewError) clearPreview();
                  }}
                />
              }
            />
            <SettingsRow
              stacked
              title={t("Profile JSON")}
              description={t(
                "Paste a Radarr or Sonarr quality profile file, from TRaSH Guides or anywhere else.",
              )}
            >
              <div className="space-y-2">
                <Textarea
                  aria-label={t("Profile JSON")}
                  className="min-h-40 w-full font-mono text-xs"
                  spellCheck={false}
                  value={pasted}
                  placeholder='{ "name": "WEB 1080p", "items": [ … ], "formatItems": { … } }'
                  onChange={(e) => {
                    setPasted(e.target.value);
                    if (plan || previewError) clearPreview();
                  }}
                />
                <div className="flex flex-wrap gap-2">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={!pasted.trim() || previewing || pasted.length > MAX_PASTE}
                    onClick={() => void load({ app, json: pasted })}
                  >
                    {t("Preview")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => file.current?.click()}>
                    <FileUp size={14} /> {t("Upload file")}
                  </Button>
                  <input
                    ref={file}
                    type="file"
                    accept="application/json,.json"
                    className="hidden"
                    aria-label={t("Profile file")}
                    onChange={(e) => {
                      void upload(e.currentTarget.files?.[0]);
                      e.currentTarget.value = "";
                    }}
                  />
                </div>
              </div>
            </SettingsRow>
          </SettingsSection>
        </div>
      ) : null}

      {step === "trash" || step === "profilarr" || step === "paste" ? (
        <>
          {previewing ? (
            <LoadingInline label={t("Reading the profile and its custom formats…")} />
          ) : null}
          {previewError ? (
            <Alert variant="destructive" role="alert">
              <AlertTitle>{t("Couldn't read that profile")}</AlertTitle>
              <AlertDescription>{errorMessage(previewError)}</AlertDescription>
            </Alert>
          ) : null}
          {plan ? (
            <ImportPreview
              plan={plan}
              name={name}
              nameError={nameError}
              onName={(v) => {
                setName(v);
                setNameError(undefined);
              }}
              syncSourceName={syncSourceName}
              keepInSync={keepInSync}
              onKeepInSync={setKeepInSync}
            />
          ) : null}
        </>
      ) : null}
    </RecordDrawer>
  );
}

function ImportPreview({
  plan,
  name,
  nameError,
  onName,
  syncSourceName,
  keepInSync,
  onKeepInSync,
}: {
  plan: QualityImportPlan;
  name: string;
  nameError?: string;
  onName: (v: string) => void;
  syncSourceName: string;
  keepInSync: boolean;
  onKeepInSync: (v: boolean) => void;
}) {
  const p = plan.profile;
  const counts = plan.formats.reduce<Record<string, number>>((acc, f) => {
    acc[f.status] = (acc[f.status] ?? 0) + 1;
    return acc;
  }, {});
  const importedCount = (counts.add ?? 0) + (counts.present ?? 0);
  const skipped = (counts.unsupported ?? 0) + (counts.missing ?? 0);
  const formatSummary = [
    counts.add ? tn(counts.add, "{count} added", "{count} added") : "",
    counts.present ? tn(counts.present, "{count} already saved", "{count} already saved") : "",
    skipped ? tn(skipped, "{count} can't be imported", "{count} can't be imported") : "",
  ]
    .filter(Boolean)
    .join(" · ");

  return (
    <div className="mt-4 space-y-4">
      {plan.unusable ? (
        <Alert variant="destructive" role="alert">
          <AlertTitle>{t("This profile can't be added")}</AlertTitle>
          <AlertDescription>{plan.unusable}</AlertDescription>
        </Alert>
      ) : null}
      <SettingsSection
        title={t("New profile")}
        description={t("Edit it like any other profile once added.")}
      >
        <TextRow
          label={t("Name")}
          value={name}
          error={nameError}
          onChange={onName}
          help={t("Must differ from your other profiles.")}
        />
        {plan.source ? (
          <SettingsRow
            title={t("Keep in sync with {source}", { source: syncSourceName })}
            description={t("Updates this profile when the source changes.")}
            control={
              <Switch
                aria-label={t("Keep in sync")}
                checked={keepInSync}
                onChange={(e) => onKeepInSync(e.currentTarget.checked)}
              />
            }
          />
        ) : null}
        <SettingsRow stacked title={t("What it brings")}>
          <div className="space-y-2">
            <ul className="flex flex-wrap gap-1.5" aria-label={t("Allowed qualities")}>
              {(p.allowedQualities ?? []).map((q) => (
                <li key={q}>
                  <Badge variant="secondary">{qualityGateLabel(q)}</Badge>
                </li>
              ))}
              {importedCount > 0 ? (
                <li>
                  <Badge variant="outline">
                    {tn(importedCount, "{count} custom format", "{count} custom formats")}
                  </Badge>
                </li>
              ) : null}
              <li>
                <Badge variant="outline">
                  {t("Minimum score {score}", {
                    score: formatNumber(p.minCustomFormatScore ?? 0),
                  })}
                </Badge>
              </li>
              {skipped > 0 ? (
                <li>
                  <Badge variant="warning">
                    {tn(skipped, "{count} format not supported", "{count} formats not supported")}
                  </Badge>
                </li>
              ) : null}
            </ul>
            {(p.qualityOrder ?? []).length > 0 ? (
              <p className="text-xs text-fg-muted">
                {(p.qualityOrder ?? []).map(itemName).filter(Boolean).join(" › ")}
              </p>
            ) : null}
          </div>
        </SettingsRow>
      </SettingsSection>

      <SettingsSection
        title={t("Custom formats")}
        description={
          plan.formats.length
            ? `${formatSummary}.${plan.scoreSet ? ` ${t("Scored with TRaSH's {set} scores.", { set: plan.scoreSet })}` : ""}`
            : t("This profile scores no custom formats.")
        }
      >
        {plan.formats.length ? (
          <div className="max-h-80 overflow-auto rounded-lg border border-border">
            <table className="w-full text-left text-sm" aria-label={t("Custom formats")}>
              <thead className="sticky top-0 bg-bg-subtle text-xs text-fg-muted">
                <tr>
                  <th className="px-3 py-2 font-medium">{t("Format")}</th>
                  <th className="px-3 py-2 text-right font-medium">{t("Score")}</th>
                  <th className="px-3 py-2 font-medium">{t("Status")}</th>
                </tr>
              </thead>
              <tbody>
                {plan.formats.map((f) => (
                  <tr className="border-t border-border align-top" key={f.id}>
                    <td className="px-3 py-2">
                      <span className="block text-fg">{f.name}</span>
                      {f.reason ? (
                        <span className="block text-xs text-fg-muted">{f.reason}</span>
                      ) : null}
                    </td>
                    <td
                      className={`px-3 py-2 text-right tabular-nums ${f.score < 0 ? "text-danger" : "text-fg"} ${
                        f.status === "unsupported" || f.status === "missing"
                          ? "line-through opacity-60"
                          : ""
                      }`}
                    >
                      {signed(f.score)}
                    </td>
                    <td className="px-3 py-2">
                      <Badge variant={STATUS[f.status].variant}>{STATUS[f.status].label}</Badge>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
      </SettingsSection>

      {plan.warnings.length ? (
        <Alert variant="warning">
          <AlertTitle>{t("Worth knowing")}</AlertTitle>
          <AlertDescription>
            <ul className="mt-1 list-disc space-y-1 pl-5">
              {plan.warnings.map((w) => (
                <li key={w}>{w}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
