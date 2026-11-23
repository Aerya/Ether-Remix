import { toast } from "@braedonsaunders/appkit-ui";
import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { Segmented } from "../../components/fields";
import { ToolbarPicker } from "../../components/ToolbarPicker";
import { t } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { formatNumber } from "../../lib/format";
import { useDebouncedValue, useInterval } from "../../lib/hooks";
import { LIVE_APPS, liveAppName } from "./ChannelApps";
import { GuideSourceList, GuideWorkbench, sourceTitle } from "./GuideData";
import { GuideGrid } from "./GuideGrid";
import { filterGuideChannels, groupGuideChannels, type GuideAppFilter } from "./guide-filter";
import {
  MINUTE,
  compileOwnerGroupMatcher,
  groupBy,
  sortByNumber,
  tv,
  type Channel,
  type ChannelSummary,
  type Coverage,
  type OwnerGroup,
  type Programme,
  type Source,
} from "./model";

const HOUR = 60 * MINUTE;
const GUIDE_APP_KEY = "ether:guide-app";
const GUIDE_GROUP_KEY = "ether:guide-group";

function readStored(key: string): string | null {
  try {
    return typeof localStorage !== "undefined" ? localStorage.getItem(key) : null;
  } catch {
    return null;
  }
}

function writeStored(key: string, value: string): void {
  try {
    if (typeof localStorage !== "undefined") localStorage.setItem(key, value);
  } catch {
    /* ignore storage errors */
  }
}

function initialGuideApp(stored: string | null): GuideAppFilter {
  if (stored === "all" || stored === "vela") return stored;
  return LIVE_APPS.includes(stored as (typeof LIVE_APPS)[number])
    ? (stored as GuideAppFilter)
    : "all";
}

function windowStart(shiftHours: number): Date {
  const d = new Date();
  d.setMinutes(0, 0, 0);
  return new Date(d.getTime() - HOUR + shiftHours * HOUR);
}

type Sub = "grid" | "mapping";

/**
 * The Guide tab as one full-width page: the TV guide grid and channel mapping
 * behind a segmented control in one toolbar row.
 */
export function GuidePage({
  initialSub = "grid",
  channels,
  sources,
  ownerGroups = [],
  coverage,
  query,
  guideUntil,
  missingCount,
  selectedChannelId,
  guideSourceId,
  onSelectGuideSource,
  onSelectChannel,
  onInspect,
  onPlay,
  onMapChannel,
  onToggleFavorite,
  onAddGuideSource,
  onAddPlaylistSource,
  onEditSource,
  onReload,
}: {
  /** The guide-data route opens on channel mapping, the Guide tab on the grid. */
  initialSub?: Sub;
  channels: ChannelSummary[];
  sources: Source[];
  ownerGroups?: OwnerGroup[];
  coverage: Map<string, Coverage> | null;
  query: string;
  /** When listings run out (ms); undefined when some channel never runs out. */
  guideUntil?: number;
  missingCount: number | null;
  selectedChannelId: string | null;
  guideSourceId: string | null;
  onSelectGuideSource: (id: string | null) => void;
  onSelectChannel: (id: string) => void;
  onInspect: (c: Channel, p: Programme) => void;
  onPlay: (c: Channel) => void;
  onMapChannel: (id: string) => void;
  onToggleFavorite?: (c: Channel) => void;
  onAddGuideSource?: () => void;
  onAddPlaylistSource?: () => void;
  onEditSource: (id: string) => void;
  onReload: () => Promise<void>;
}) {
  const [sub, setSub] = useState<Sub>(initialSub === "mapping" ? "mapping" : "grid");
  const [app, setApp] = useState<GuideAppFilter>(() => initialGuideApp(readStored(GUIDE_APP_KEY)));
  const [group, setGroup] = useState<string>(() => readStored(GUIDE_GROUP_KEY) ?? "");
  const changeApp = (next: GuideAppFilter) => {
    setApp(next);
    writeStored(GUIDE_APP_KEY, next);
  };
  const changeGroup = (next: string) => {
    setGroup(next);
    writeStored(GUIDE_GROUP_KEY, next);
  };
  const [now, setNow] = useState(() => Date.now());
  useInterval(() => setNow(Date.now()), 30_000);

  const guideSource = guideSourceId ? sources.find((s) => s.id === guideSourceId) : undefined;

  // Coverage over live IPTV channels (virtual channels build their own).
  const live = useMemo(() => channels.filter((c) => c.kind === "iptv" && c.enabled), [channels]);
  const missing = useMemo(
    () => live.filter((c) => coverage && (coverage.get(c.id)?.programmes ?? 0) === 0).length,
    [live, coverage],
  );
  const missingLabel = missingCount ?? missing;

  const groups = useMemo(() => groupGuideChannels(channels), [channels]);
  // Your groups (by rule, provider group or picked channels) are owner groups,
  // not channel renames: filter them with the same matcher Channels uses.
  const sourceNames = useMemo(() => new Map(sources.map((s) => [s.id, sourceTitle(s)])), [sources]);
  const ownerGroup = group.startsWith("owner:")
    ? ownerGroups.find((g) => g.id === group.slice(6))
    : undefined;
  const guideChannels = useMemo(
    () =>
      ownerGroup ? channels.filter(compileOwnerGroupMatcher(ownerGroup, sourceNames)) : channels,
    [channels, ownerGroup, sourceNames],
  );
  // A remembered group that was since deleted or renamed would leave the
  // guide empty: fall back to every group once the lineup has loaded.
  useEffect(() => {
    if (!group || group === "yours" || group === "provider" || channels.length === 0) return;
    const gone = group.startsWith("owner:")
      ? !ownerGroups.some((g) => g.id === group.slice(6))
      : group.startsWith("name:") &&
        !groups.yours.includes(group.slice(5)) &&
        !groups.provider.includes(group.slice(5));
    if (gone) changeGroup("");
  }, [group, ownerGroups, groups, channels.length]);

  const subTabs = (
    <Segmented
      ariaLabel={t("Guide")}
      size="sm"
      className="p-px"
      value={sub}
      onChange={setSub}
      options={[
        { value: "grid", label: t("TV guide") },
        {
          value: "mapping",
          label: `${t("Channel mapping")} · ${formatNumber(missingLabel)}`,
        },
      ]}
    />
  );

  // One toolbar row: these sit at the start of the grid's own toolbar.
  const gridFilters = (
    <div className="flex min-w-0 flex-wrap items-center gap-2">
      <ToolbarPicker
        ariaLabel={t("As seen in")}
        value={app}
        onChange={(v) => changeApp(v as GuideAppFilter)}
        options={[
          { value: "all", label: t("All apps") },
          ...LIVE_APPS.map((a) => ({ value: a, label: liveAppName(a) })),
        ]}
        searchable={false}
        menuClassName="w-48"
      />
      <ToolbarPicker
        ariaLabel={t("Group")}
        value={group}
        onChange={changeGroup}
        className="max-w-[11rem]"
        options={[
          { value: "", label: t("All groups") },
          ...ownerGroups.map((g) => ({
            value: `owner:${g.id}`,
            label: g.name,
            group: t("Your groups"),
          })),
          ...(groups.yours.length && !ownerGroups.length
            ? [{ value: "yours", label: t("Your groups"), group: t("Your groups") }]
            : []),
          ...(ownerGroups.length ? [] : groups.yours).map((g) => ({
            value: `name:${g}`,
            label: g,
            group: t("Your groups"),
          })),
          ...(groups.provider.length
            ? [
                {
                  value: "provider",
                  label: t("Provider groups"),
                  group: t("Provider groups"),
                },
              ]
            : []),
          ...groups.provider.map((g) => ({
            value: `name:${g}`,
            label: g,
            group: t("Provider groups"),
          })),
        ]}
      />
    </div>
  );

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col overflow-x-hidden">
      {sub !== "grid" ? (
        <div className="flex min-w-0 shrink-0 flex-wrap items-center gap-2 border-b border-border bg-surface px-3 py-2 sm:px-4">
          {subTabs}
        </div>
      ) : null}

      {sub === "grid" ? (
        <TVGuide
          channels={guideChannels}
          app={app}
          group={ownerGroup ? "" : group}
          query={query}
          now={now}
          guideUntil={guideUntil}
          toolbarStart={subTabs}
          toolbarFilters={gridFilters}
          selectedChannelId={selectedChannelId}
          onSelectChannel={onSelectChannel}
          onInspect={onInspect}
          onPlay={onPlay}
          onMapChannel={onMapChannel}
          onToggleFavorite={onToggleFavorite}
        />
      ) : (
        <div className="app-scroll flex min-h-0 min-w-0 flex-1 flex-col overflow-x-hidden overflow-y-auto lg:grid lg:grid-cols-[272px_minmax(0,1fr)] lg:overflow-hidden">
          <aside
            aria-label={t("Guide sources")}
            className="app-scroll shrink-0 border-b border-border bg-surface lg:min-h-0 lg:overflow-x-hidden lg:overflow-y-auto lg:border-b-0 lg:border-r"
          >
            <GuideSourceList
              query={query}
              lineup={channels}
              sources={sources}
              selected={guideSourceId}
              onSelect={onSelectGuideSource}
              onAddGuideSource={onAddGuideSource}
              now={now}
            />
          </aside>
          <div className="app-scroll min-h-0 min-w-0 flex-1 lg:overflow-x-hidden lg:overflow-y-auto">
            <GuideWorkbench
              channels={channels}
              query={query}
              sources={sources}
              ownerGroups={ownerGroups}
              coverage={coverage}
              source={guideSource}
              showCoverageCard={false}
              onMapChannel={onMapChannel}
              onAddPlaylistSource={onAddPlaylistSource}
              onEditSource={onEditSource}
              onReload={onReload}
              now={now}
            />
          </div>
        </div>
      )}
    </div>
  );
}

/**
 * The EPG grid over the filtered lineup. GuideGrid virtualises rows and
 * reports the visible range; listings are fetched only for that window
 * (plus the first rows), never for the whole lineup.
 */
function TVGuide({
  channels,
  app,
  group,
  query,
  now,
  guideUntil,
  toolbarStart,
  toolbarFilters,
  selectedChannelId,
  onSelectChannel,
  onInspect,
  onPlay,
  onMapChannel,
  onToggleFavorite,
}: {
  channels: ChannelSummary[];
  app: GuideAppFilter;
  group: string;
  query: string;
  now: number;
  guideUntil?: number;
  toolbarStart?: ReactNode;
  toolbarFilters?: ReactNode;
  selectedChannelId: string | null;
  onSelectChannel: (id: string) => void;
  onInspect: (c: Channel, p: Programme) => void;
  onPlay: (c: Channel) => void;
  onMapChannel: (id: string) => void;
  onToggleFavorite?: (c: Channel) => void;
}) {
  const [shift, setShift] = useState(0);
  const [range, setRange] = useState<[number, number]>([0, 40]);
  const [programmes, setProgrammes] = useState<Programme[]>([]);
  const [loading, setLoading] = useState(false);
  const from = useMemo(() => windowStart(shift), [shift]);
  const until = useMemo(() => new Date(from.getTime() + 24 * HOUR), [from]);

  const needle = query.trim().toLowerCase();
  const rows = useMemo(() => {
    const filtered = sortByNumber(filterGuideChannels(channels, { app, group }));
    return needle
      ? filtered.filter((c) =>
          `${c.number} ${c.name} ${c.group ?? ""}`.toLowerCase().includes(needle),
        )
      : filtered;
  }, [channels, app, group, needle]);

  const guideIDs = useMemo(() => {
    const ids = new Set<string>();
    rows.slice(0, 120).forEach((c) => ids.add(c.id));
    rows.slice(range[0], range[1]).forEach((c) => ids.add(c.id));
    if (selectedChannelId) ids.add(selectedChannelId);
    return [...ids].join(",");
  }, [rows, range, selectedChannelId]);
  const debouncedIDs = useDebouncedValue(guideIDs, 150);

  useEffect(() => {
    if (!debouncedIDs) {
      setProgrammes([]);
      return;
    }
    const ctl = new AbortController();
    setLoading(true);
    tv.guide(from, until, debouncedIDs.split(","), ctl.signal)
      .then((ps) => {
        if (!ctl.signal.aborted) setProgrammes(ps ?? []);
      })
      .catch((e) => {
        if (!ctl.signal.aborted)
          toast.error(t("Couldn't load the guide"), { description: errorMessage(e) });
      })
      .finally(() => {
        if (!ctl.signal.aborted) setLoading(false);
      });
    return () => ctl.abort();
  }, [debouncedIDs, from, until]);

  const guide = useMemo(() => groupBy(programmes, (p) => p.channelId), [programmes]);
  const onRange = useCallback(
    (first: number, last: number) =>
      setRange((prev) => (prev[0] === first && prev[1] === last ? prev : [first, last])),
    [],
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <GuideGrid
        toolbarStart={toolbarStart}
        toolbarFilters={toolbarFilters}
        // Summaries carry everything the grid's channel column reads.
        channels={rows as Channel[]}
        guide={guide}
        from={from}
        until={until}
        now={now}
        selected={selectedChannelId}
        onSelect={onSelectChannel}
        onInspect={onInspect}
        onPlay={onPlay}
        onMapGuide={(c) => onMapChannel(c.id)}
        onToggleFavorite={onToggleFavorite}
        guideUntil={guideUntil}
        onShift={(h) => setShift((s) => s + h)}
        onNow={() => setShift(0)}
        onRange={onRange}
        loading={loading}
        emptyHint={<p className="text-xs text-fg-muted">{t("No channels match.")}</p>}
      />
    </div>
  );
}
