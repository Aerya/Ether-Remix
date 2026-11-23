import { Button, Checkbox, Input, Popover } from "@braedonsaunders/appkit-ui";
import {
  AppWindow,
  Eye,
  EyeOff,
  FolderInput,
  FolderPlus,
  Plus,
  Power,
  PowerOff,
  Star,
  StarOff,
} from "lucide-react";
import { useId, useState } from "react";
import { BulkBar } from "../../components/BulkBar";
import { t } from "../../i18n";
import { LIVE_APPS, type LiveApp, liveAppChipLabel, liveAppName } from "./ChannelApps";

export type ChannelPatch = {
  enabled?: boolean;
  favorite?: boolean;
  group?: string;
  /** Apps to list the channels in / leave them out of; other apps keep each channel's choice. */
  showApps?: string[];
  hideApps?: string[];
  clearApps?: string[];
};

/** How the selected channels currently appear in one app. */
export type AppState = "on" | "off" | "mixed";

export function appStates(hidden: (string[] | undefined)[]): Record<LiveApp, AppState> {
  const out = {} as Record<LiveApp, AppState>;
  for (const app of LIVE_APPS) {
    const off = hidden.filter((h) => h?.includes(app)).length;
    out[app] = off === 0 ? "on" : off === hidden.length ? "off" : "mixed";
  }
  return out;
}

/** A channel after a bulk patch, as the server applies it. */
export function patchChannel<C extends { hiddenApps?: string[]; shownApps?: string[] }>(
  c: C,
  patch: ChannelPatch,
): C {
  const { showApps, hideApps, clearApps, ...rest } = patch;
  const next: C = { ...c, ...rest, ...(rest.group !== undefined ? { customGroup: true } : {}) };
  if (showApps || hideApps || clearApps) {
    const hidden = new Set(c.hiddenApps ?? []);
    const shown = new Set(c.shownApps ?? []);
    for (const app of clearApps ?? []) {
      hidden.delete(app);
      shown.delete(app);
    }
    for (const app of showApps ?? []) {
      hidden.delete(app);
      shown.add(app);
    }
    for (const app of hideApps ?? []) {
      shown.delete(app);
      hidden.add(app);
    }
    next.hiddenApps = hidden.size ? LIVE_APPS.filter((a) => hidden.has(a)) : undefined;
    next.shownApps = shown.size ? LIVE_APPS.filter((a) => shown.has(a)) : undefined;
  }
  return next;
}
export type ChannelSelection = {
  ids: ReadonlySet<string>;
  busy: boolean;
  all: boolean;
  toggle: (id: string, range?: boolean) => void;
  toggleAll: () => void;
};

/** Floats over the workspace or guide; selecting rows never resizes headers or columns. */
export function ChannelBulkBar({
  count,
  total,
  busy,
  groups,
  apps,
  selectedApp = "all",
  ownerGroups,
  floating = false,
  onAll,
  onClear,
  onApply,
  onSetApp,
  onAddToOwnerGroup,
}: {
  count: number;
  total: number;
  busy: boolean;
  groups: string[];
  /** Current app visibility of the selection. */
  apps: Record<LiveApp, AppState>;
  /** Active app view on the Channels page ("all" shows the multi-app popover). */
  selectedApp?: "all" | LiveApp;
  /** Existing "Your groups" choices for Add to group…. */
  ownerGroups?: { id: string; name: string }[];
  floating?: boolean;
  onAll: () => void;
  onClear: () => void;
  onApply: (patch: ChannelPatch) => Promise<boolean>;
  onSetApp?: (app: LiveApp, show: boolean) => Promise<boolean>;
  onAddToOwnerGroup?: (target: { id?: string; name: string }) => Promise<boolean>;
}) {
  const [groupOpen, setGroupOpen] = useState(false);
  const [ownerGroupOpen, setOwnerGroupOpen] = useState(false);
  const [appsOpen, setAppsOpen] = useState(false);
  const [appDraft, setAppDraft] = useState(apps);
  const [group, setGroup] = useState("");
  const [newOwnerGroup, setNewOwnerGroup] = useState("");
  const listId = useId();
  const activeAppLabel = selectedApp === "all" ? "" : liveAppChipLabel(selectedApp);
  const applySingleApp = (app: LiveApp, show: boolean) => {
    if (onSetApp) return onSetApp(app, show);
    return onApply(show ? { showApps: [app] } : { hideApps: [app] });
  };
  return (
    <BulkBar
      count={count}
      busy={busy}
      label={t("Channel actions")}
      floating={floating}
      leading={
        count < total ? (
          <Button size="sm" variant="ghost" disabled={busy} onClick={onAll}>
            {t("Select all matches")}
          </Button>
        ) : null
      }
      onClear={onClear}
    >
      {selectedApp !== "all" ? (
        <>
          <Button size="sm" disabled={busy} onClick={() => void applySingleApp(selectedApp, true)}>
            <Eye size={14} />
            {t("Show in {app}", { app: activeAppLabel })}
          </Button>
          <Button
            size="sm"
            variant="outline"
            disabled={busy}
            onClick={() => void applySingleApp(selectedApp, false)}
          >
            <EyeOff size={14} />
            {t("Hide from {app}", { app: activeAppLabel })}
          </Button>
        </>
      ) : (
        <Popover
          open={appsOpen}
          onOpenChange={(open) => {
            if (open) setAppDraft(apps);
            setAppsOpen(open);
          }}
          align="end"
          side="top"
          className="w-72 p-3"
          trigger={
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                if (!appsOpen) setAppDraft(apps);
                setAppsOpen(!appsOpen);
              }}
            >
              <AppWindow size={14} />
              {t("Show in apps…")}
            </Button>
          }
        >
          <form
            className="space-y-3"
            onSubmit={async (e) => {
              e.preventDefault();
              const showApps = LIVE_APPS.filter((a) => appDraft[a] === "on" && apps[a] !== "on");
              const hideApps = LIVE_APPS.filter((a) => appDraft[a] === "off" && apps[a] !== "off");
              if (await onApply({ showApps, hideApps })) setAppsOpen(false);
            }}
          >
            <fieldset className="space-y-2">
              <legend className="mb-1 text-sm font-medium">{t("Show in apps")}</legend>
              {LIVE_APPS.map((app) => (
                <label key={app} className="flex items-center gap-2.5 text-sm">
                  <Checkbox
                    checked={appDraft[app] === "on"}
                    ref={(el) => {
                      if (el) el.indeterminate = appDraft[app] === "mixed";
                    }}
                    disabled={busy}
                    onChange={(e) =>
                      setAppDraft({ ...appDraft, [app]: e.currentTarget.checked ? "on" : "off" })
                    }
                  />
                  {liveAppName(app)}
                </label>
              ))}
            </fieldset>
            <p className="text-xs text-fg-muted">
              {t("Apps you leave unchanged keep each channel's own setting.")}
            </p>
            <Button
              type="submit"
              size="sm"
              disabled={busy || LIVE_APPS.every((a) => appDraft[a] === apps[a])}
            >
              {t("Save")}
            </Button>
          </form>
        </Popover>
      )}
      {onAddToOwnerGroup ? (
        <Popover
          open={ownerGroupOpen}
          onOpenChange={(open) => {
            if (open) setNewOwnerGroup("");
            setOwnerGroupOpen(open);
          }}
          align="end"
          side="top"
          className="w-72 p-3"
          trigger={
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              onClick={() => {
                if (!ownerGroupOpen) setNewOwnerGroup("");
                setOwnerGroupOpen(!ownerGroupOpen);
              }}
            >
              <FolderPlus size={14} />
              {t("Add to group…")}
            </Button>
          }
        >
          <div className="space-y-3">
            {ownerGroups && ownerGroups.length > 0 ? (
              <div className="space-y-1">
                <p className="text-xs font-medium text-fg-subtle">{t("Your groups")}</p>
                <div className="app-scroll max-h-44 space-y-0.5 overflow-y-auto">
                  {ownerGroups.map((g) => (
                    <button
                      key={g.id}
                      type="button"
                      disabled={busy}
                      onClick={async () => {
                        if (await onAddToOwnerGroup({ id: g.id, name: g.name })) {
                          setOwnerGroupOpen(false);
                        }
                      }}
                      className="flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-sm text-fg transition-colors hover:bg-surface-hover disabled:opacity-50"
                    >
                      <span className="truncate font-medium">{g.name}</span>
                    </button>
                  ))}
                </div>
              </div>
            ) : null}
            <form
              className="space-y-2 border-t border-border pt-2.5 first:border-t-0 first:pt-0"
              onSubmit={async (e) => {
                e.preventDefault();
                const trimmed = newOwnerGroup.trim();
                if (!trimmed) return;
                if (await onAddToOwnerGroup({ name: trimmed })) {
                  setNewOwnerGroup("");
                  setOwnerGroupOpen(false);
                }
              }}
            >
              <label className="block space-y-1.5 text-xs font-medium text-fg-subtle">
                <span>{t("New group")}</span>
                <Input
                  autoFocus={!ownerGroups?.length}
                  value={newOwnerGroup}
                  maxLength={150}
                  disabled={busy}
                  placeholder={t("Group name")}
                  onChange={(e) => setNewOwnerGroup(e.currentTarget.value)}
                />
              </label>
              <Button type="submit" size="sm" disabled={busy || !newOwnerGroup.trim()}>
                <Plus size={14} />
                {t("Create group")}
              </Button>
            </form>
          </div>
        </Popover>
      ) : null}
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ enabled: true })}
      >
        <Power size={14} />
        {t("Enable")}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ enabled: false })}
      >
        <PowerOff size={14} />
        {t("Disable")}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ favorite: true })}
      >
        <Star size={14} />
        {t("Favourite")}
      </Button>
      <Button
        size="sm"
        variant="ghost"
        disabled={busy}
        onClick={() => void onApply({ favorite: false })}
      >
        <StarOff size={14} />
        {t("Unfavourite")}
      </Button>
      <Popover
        open={groupOpen}
        onOpenChange={setGroupOpen}
        align="end"
        side="top"
        className="w-72 p-3"
        trigger={
          <Button
            size="sm"
            variant="ghost"
            disabled={busy}
            onClick={() => setGroupOpen(!groupOpen)}
          >
            <FolderInput size={14} />
            {t("Move to group")}
          </Button>
        }
      >
        <form
          className="space-y-3"
          onSubmit={async (e) => {
            e.preventDefault();
            if (await onApply({ group: group.trim() })) setGroupOpen(false);
          }}
        >
          <label className="block space-y-1.5 text-sm font-medium">
            <span>{t("Group")}</span>
            <Input
              autoFocus
              value={group}
              maxLength={200}
              disabled={busy}
              list={listId}
              onChange={(e) => setGroup(e.currentTarget.value)}
            />
          </label>
          <datalist id={listId}>
            {groups.map((name) => (
              <option key={name} value={name} />
            ))}
          </datalist>
          <Button type="submit" size="sm" disabled={busy || !group.trim()}>
            {t("Save")}
          </Button>
        </form>
      </Popover>
    </BulkBar>
  );
}
