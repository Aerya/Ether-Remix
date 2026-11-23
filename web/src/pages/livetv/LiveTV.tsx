import { ChannelDescribeDialog } from "./ChannelDescribeDialog";
import Library, { LibraryAside } from "./library/Library";
import { styleCopy, stylesName } from "./onair/naming";
import { useLooks, LooksAside, LooksGallery, LookSurfaces } from "./onair/LooksGallery";
import {
  Badge,
  Button,
  Input,
  Popover,
  SubtabNav,
  cn,
  confirmDialog,
  toast,
} from "@braedonsaunders/appkit-ui";
import {
  CalendarRange,
  ListVideo,
  Palette,
  Plus,
  Puzzle,
  Save,
  Search,
  Sparkles,
  Tv,
  Undo2,
  Upload,
  UserRound,
} from "lucide-react";
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel } from "../../components/feedback";
import { Workspace } from "../../components/workspace";
import { api, errorMessage } from "../../lib/api";
import { t, tn } from "../../i18n";
import { useDebouncedValue, useInterval, useMediaQuery } from "../../lib/hooks";
import type { EtherUser } from "../../lib/types";
import {
  ChannelStudio,
  compose,
  studioDraft,
  type CollectionChoice,
  type StudioDraft,
} from "./ChannelStudio";
import { GuideMatchPanel } from "./GuideData";
import { type ChannelPatch, patchChannel } from "./ChannelBulkBar";
import { ChannelDrawerBody } from "./ChannelDrawerBody";
import { ChannelsWorkspace, type ChannelFilter, type ChannelSort } from "./ChannelsWorkspace";
import type { ReorderPlan } from "./reorder";
import { GuidePage } from "./GuidePage";
import { LivePreview } from "./LivePreview";
import { ProgrammeInspector, type InspectedProgramme } from "./ProgrammeInspector";
import type { Selection } from "./Lineup";
import { SourceDrawer, type SourceKind } from "./SourceEditor";
import {
  MINUTE,
  TEMPLATES,
  browserTimeZone,
  compileOwnerGroupMatcher,
  compileOwnerGroupRegExp,
  emptyChannel,
  plexLineupName,
  tv,
  type Channel,
  type ChannelAppChange,
  type ChannelSummary,
  type Coverage,
  type LiveTVPolicy,
  type OwnerGroup,
  type Programme,
  type Source,
} from "./model";

function useCurrentCallback<A extends unknown[], R>(callback: (...args: A) => R) {
  const latest = useRef(callback);
  useLayoutEffect(() => {
    latest.current = callback;
  });
  return useCallback((...args: A) => latest.current(...args), []);
}

const HOUR = 60 * MINUTE;
function windowStart(shiftHours: number): Date {
  const d = new Date();
  d.setMinutes(0, 0, 0);
  return new Date(d.getTime() - HOUR + shiftHours * HOUR);
}

function selectionFrom(params: URLSearchParams): Selection {
  const c = params.get("channel");
  if (c === "new") return { kind: "new" };
  if (c) return { kind: "channel", id: c };
  const g = params.get("guide");
  if (g) return { kind: "map", id: g };
  return { kind: "guide" };
}

function nextChannelNumber(channels: readonly ChannelSummary[] | null): string {
  if (!channels || channels.length === 0) return "1";
  let max = 0;
  for (const c of channels) {
    const n = Math.floor(parseFloat(c.number) || 0);
    if (n > max) max = n;
  }
  return String(max + 1);
}

type LiveTVTab = "channels" | "guide" | "looks" | "library";

export default function LiveTV() {
  const { tab } = useParams();
  const { looks, reels, reload: reloadLooks } = useLooks();
  const navigate = useNavigate();
  const [params, setParams] = useCurrentParams();
  const selection = selectionFrom(params);
  const guideSourceId = params.get("gsrc");
  const rawSourceId = params.get("source");
  const sourceId = rawSourceId && rawSourceId !== "new" ? rawSourceId : null;
  const addingSource = params.get("new") ?? (rawSourceId === "new" ? "playlist" : null);

  useEffect(() => {
    if (tab === "sources") navigate("/sources/iptv", { replace: true });
  }, [tab, navigate]);

  const activeTab: LiveTVTab =
    tab === "library"
      ? "library"
      : tab === "looks"
        ? "looks"
        : tab === "guide" || tab === "guide-data" || (!tab && selection.kind === "map")
          ? "guide"
          : "channels";

  const [mappingJumpNonce, setMappingJumpNonce] = useState(0);
  const [bulkBusy, setBulkBusy] = useState(false);
  const [addingDecades, setAddingDecades] = useState(false);
  const bulkLock = useRef(false);

  const [allChannels, setAllChannels] = useState<ChannelSummary[] | null>(null);
  const [ownerGroups, setOwnerGroups] = useState<OwnerGroup[]>([]);
  const [selectedChannel, setSelectedChannel] = useState<Channel | null>(null);
  const [selectionLoading, setSelectionLoading] = useState(false);
  const [guideNonce, setGuideNonce] = useState(0);
  const [coverageNonce, setCoverageNonce] = useState(0);
  const [sources, setSources] = useState<Source[]>([]);
  const [sourcesLoaded, setSourcesLoaded] = useState(false);
  const [users, setUsers] = useState<EtherUser[]>([]);
  const [policies, setPolicies] = useState<Map<number, LiveTVPolicy>>(new Map());
  const [collections, setCollections] = useState<CollectionChoice[]>([]);
  const [drawerProgrammes, setDrawerProgrammes] = useState<Programme[]>([]);
  const [coverageList, setCoverageList] = useState<Coverage[] | null | "unsupported">(null);
  const [error, setError] = useState<unknown>(null);
  const [now, setNow] = useState(() => Date.now());
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<ChannelFilter>("all");
  const [channelSort, setChannelSort] = useState<ChannelSort>("number");
  const [inspected, setInspected] = useState<InspectedProgramme | null>(null);
  const [inspectOpen, setInspectOpen] = useState(false);
  const [previewChannel, setPreviewChannel] = useState<Channel | null>(null);
  const [describing, setDescribing] = useState(false);
  const [draft, setDraft] = useState<StudioDraft | null>(null);
  const [baseline, setBaseline] = useState("");
  const [saving, setSaving] = useState(false);
  const [guideFooter, setGuideFooter] = useState<React.ReactNode>(null);

  useInterval(() => setNow(Date.now()), 30_000);
  const search = useDebouncedValue(q, 200);

  useEffect(() => {
    const ctl = new AbortController();
    Promise.all([
      tv.sources(ctl.signal),
      api.users(),
      api.collections().catch(() => []),
      tv.policies(ctl.signal),
    ])
      .then(([s, u, cs, ps]) => {
        if (ctl.signal.aborted) return;
        setSources(s ?? []);
        setUsers(u);
        setCollections(cs);
        setPolicies(new Map((ps ?? []).map((p) => [p.userId, p])));
      })
      .catch((e) => {
        if (!ctl.signal.aborted) setError(e);
      })
      .finally(() => {
        if (!ctl.signal.aborted) setSourcesLoaded(true);
      });
    return () => ctl.abort();
  }, []);

  const loadCoverage = useCallback(async (signal?: AbortSignal) => {
    try {
      setCoverageList((await tv.coverage(signal)) ?? []);
    } catch {
      if (!signal?.aborted) setCoverageList("unsupported");
    }
  }, []);

  useEffect(() => {
    const ctl = new AbortController();
    void loadCoverage(ctl.signal);
    tv.groups(ctl.signal)
      .then((gs) => {
        if (!ctl.signal.aborted) setOwnerGroups(gs);
      })
      .catch(() => {
        if (!ctl.signal.aborted) setOwnerGroups([]);
      });
    tv.channelSummaries(ctl.signal)
      .then((all) => {
        if (!ctl.signal.aborted) {
          setAllChannels((all ?? []).filter((c) => !c.mergedInto));
          setError(null);
        }
      })
      .catch((e) => {
        if (!ctl.signal.aborted) setError(e);
      });
    return () => ctl.abort();
  }, [coverageNonce, loadCoverage]);

  const savedChannel =
    selection.kind === "channel" && selectedChannel?.id === selection.id
      ? selectedChannel
      : undefined;
  const dirty = draft !== null && JSON.stringify(draft) !== baseline;
  const hasInvalidMergeRule = Boolean(
    draft?.channel.inputMerge?.rules?.some(
      (r) =>
        r.value !== "" &&
        (r.op === "matches" || r.op === "notMatches") &&
        !compileOwnerGroupRegExp(r.value),
    ),
  );

  useEffect(() => {
    if (selection.kind !== "channel" && selection.kind !== "map") {
      setSelectedChannel(null);
      setSelectionLoading(false);
      return;
    }
    if (selectedChannel?.id === selection.id) {
      setSelectionLoading(false);
      return;
    }
    const ctl = new AbortController();
    setSelectionLoading(true);
    tv.channel(selection.id, ctl.signal)
      .then((c) => {
        if (ctl.signal.aborted) return;
        setSelectedChannel(c);
        setSelectionLoading(false);
      })
      .catch(() => {
        if (ctl.signal.aborted) return;
        setSelectedChannel(null);
        setSelectionLoading(false);
      });
    return () => ctl.abort();
  }, [
    selection.kind,
    selection.kind === "channel" || selection.kind === "map" ? selection.id : "",
    selectedChannel?.id,
  ]);

  useEffect(() => {
    if (selection.kind === "channel" && savedChannel) {
      const d = studioDraft(savedChannel);
      setDraft(d);
      setBaseline(JSON.stringify(d));
    } else if (selection.kind !== "new") {
      setDescribing(false);
      setDraft(null);
      setBaseline("");
    }
    // Only re-seed when the selected record (or its saved copy) changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selection.kind, selection.kind === "channel" ? selection.id : "", savedChannel]);

  // Fetch programmes only for an open channel drawer; GuidePage loads its own visible window.
  useEffect(() => {
    if (selection.kind !== "channel" || !savedChannel?.id) {
      setDrawerProgrammes([]);
      return;
    }
    const ctl = new AbortController();
    const from = windowStart(0);
    const until = new Date(from.getTime() + 24 * HOUR);
    tv.guide(from, until, [savedChannel.id], ctl.signal)
      .then((ps) => {
        if (!ctl.signal.aborted) setDrawerProgrammes(ps ?? []);
      })
      .catch(() => {
        if (!ctl.signal.aborted) setDrawerProgrammes([]);
      });
    return () => ctl.abort();
  }, [selection.kind, savedChannel?.id, guideNonce]);

  const coverage = useMemo(
    () =>
      coverageList && coverageList !== "unsupported"
        ? new Map(coverageList.map((c) => [c.channelId, c]))
        : null,
    [coverageList],
  );

  const covered = useCallback(
    (c: Channel) => {
      if (c.kind === "virtual") return true;
      if (coverage && (coverage.size > 0 || coverage.has(c.id))) {
        return (coverage.get(c.id)?.programmes ?? 0) > 0;
      }
      return Boolean(c.guideId);
    },
    [coverage],
  );

  const sorted = useMemo(() => allChannels ?? [], [allChannels]);
  const liveCount = useMemo(
    () => (allChannels ? allChannels.filter((c) => c.enabled).length : null),
    [allChannels],
  );
  const missingCount = useMemo(() => {
    if (!allChannels) return null;
    return allChannels.filter((c) => {
      if (!c.enabled || c.kind !== "iptv") return false;
      if (coverage && (coverage.size > 0 || coverage.has(c.id))) {
        return (coverage.get(c.id)?.programmes ?? 0) === 0;
      }
      return !c.guideId;
    }).length;
  }, [allChannels, coverage]);

  const guideUntil = useMemo(() => {
    if (!coverage) return undefined;
    let last = 0;
    for (const c of sorted) {
      const cov = c.enabled ? coverage.get(c.id) : undefined;
      if (!cov?.programmes) continue;
      if (c.kind === "virtual") return undefined;
      const until = cov.until ? Date.parse(cov.until) : 0;
      if (until > last) last = until;
    }
    return last;
  }, [coverage, sorted]);

  const loadPolicies = useCallback(async (signal?: AbortSignal) => {
    const list = await tv.policies(signal);
    setPolicies(new Map((list ?? []).map((p) => [p.userId, p])));
  }, []);

  const reloadAll = useCallback(async () => {
    setGuideNonce((n) => n + 1);
    setCoverageNonce((n) => n + 1);
    try {
      setSources((await tv.sources()) ?? []);
    } catch {
      // Keep the sources already on screen; the next save or reload retries.
    }
  }, []);

  useEffect(() => {
    const changed = () => {
      void reloadAll();
    };
    window.addEventListener("ether:channels-changed", changed);
    return () => window.removeEventListener("ether:channels-changed", changed);
  }, [reloadAll]);

  const confirmDiscard = useCallback(
    () =>
      confirmDialog({
        title: t("Discard unsaved changes?"),
        message: draft?.channel.name
          ? t("Your changes to {name} haven't been saved.", { name: draft.channel.name })
          : t("Your changes to this channel haven't been saved."),
        confirmLabel: t("Discard"),
        tone: "danger",
      }),
    [draft],
  );

  const go = useCallback(
    async (next: Selection, opts?: { force?: boolean }) => {
      if (dirty && !opts?.force && !(await confirmDiscard())) return false;
      if (next.kind === "source") {
        setParams((prev) => {
          const p = new URLSearchParams(prev);
          p.delete("channel");
          p.delete("guide");
          p.delete("new");
          p.delete("source");
          if (next.id === "new") p.set("new", "playlist");
          else p.set("source", next.id);
          return p;
        });
        return true;
      }
      const p = new URLSearchParams();
      if (next.kind === "channel") p.set("channel", next.id);
      if (next.kind === "new") p.set("channel", "new");
      if (next.kind === "map") p.set("guide", next.id);
      const gsrc = params.get("gsrc");
      if (gsrc && next.kind !== "channel" && next.kind !== "new") p.set("gsrc", gsrc);
      setParams(p);
      return true;
    },
    [dirty, confirmDiscard, params, setParams],
  );

  const startNew = async (templateKey: string) => {
    const template = TEMPLATES.find((x) => x.key === templateKey) ?? TEMPLATES[0]!;
    const base = emptyChannel();
    base.number = nextChannelNumber(allChannels);
    base.timezone = browserTimeZone();
    const d = studioDraft(templateKey === "describe" ? base : template.apply(base));
    setDraft(d);
    setBaseline(JSON.stringify(d));
    setDescribing(templateKey === "describe");
  };

  const saveDraft = async () => {
    if (!draft) return;
    let body: Channel;
    try {
      body = compose(draft);
    } catch (e) {
      toast.error(t("Can't save yet"), { description: errorMessage(e) });
      return;
    }
    if (!body.name.trim()) {
      toast.error(t("Can't save yet"), { description: t("Give the channel a name.") });
      return;
    }
    setSaving(true);
    try {
      const saved = await tv.saveChannel(body);
      const viewers = draft.viewers;
      let viewersError: unknown = null;
      if (viewers && saved?.id) {
        const ids = Object.keys(viewers);
        try {
          await tv.setChannelViewers(saved.id, {
            allow: ids.filter((id) => viewers[id]).map(Number),
            deny: ids.filter((id) => !viewers[id]).map(Number),
          });
          await loadPolicies();
        } catch (e) {
          viewersError = e;
        }
      }
      toast.success(body.id ? t("Channel saved") : t("Channel created"), {
        description: t("Plex's lineup updates in a moment."),
      });
      if (viewersError)
        toast.error(t("Couldn't save who can watch this channel"), {
          description: errorMessage(viewersError),
        });
      if (saved) setSelectedChannel(saved);
      setGuideNonce((n) => n + 1);
      setCoverageNonce((n) => n + 1);
      if (saved) {
        const d = studioDraft(saved);
        setDraft(d);
        setBaseline(JSON.stringify(d));
        if (!body.id && saved.id) await go({ kind: "channel", id: saved.id }, { force: true });
      }
    } catch (e) {
      toast.error(t("Couldn't save the channel"), { description: errorMessage(e) });
    } finally {
      setSaving(false);
    }
  };

  const quickSave = async (c: Channel | ChannelSummary, message?: string) => {
    try {
      const stored =
        "items" in c && c.items !== undefined ? (c as Channel) : await tv.channel(c.id);
      const saved = await tv.saveChannel({ ...stored, ...c } as Channel);
      setAllChannels((prev) =>
        prev ? prev.map((row) => (row.id === saved.id ? { ...row, ...saved } : row)) : prev,
      );
      if (selectedChannel?.id === saved.id) setSelectedChannel(saved);
      if (message) toast.success(message);
      setGuideNonce((n) => n + 1);
      setCoverageNonce((n) => n + 1);
    } catch (e) {
      toast.error(t("Couldn't update the channel"), { description: errorMessage(e) });
    }
  };

  const latest = useRef({ go, quickSave });
  useLayoutEffect(() => {
    latest.current = { go, quickSave };
  });
  const selectChannel = useCallback(
    (id: string) => void latest.current.go({ kind: "channel", id }),
    [],
  );
  const inspectProgramme = useCallback((channel: Channel, programme: Programme) => {
    setInspected({ channel, programme });
    setInspectOpen(true);
  }, []);
  const toggleFavorite = useCallback(
    (c: Channel) =>
      void latest.current.quickSave(
        { ...c, favorite: !c.favorite },
        c.favorite
          ? t("{name} unfavourited", { name: c.name })
          : t("{name} favourited", { name: c.name }),
      ),
    [],
  );

  const deleteChannel = async () => {
    if (!draft?.channel.id) return;
    const id = draft.channel.id;
    const name = draft.channel.name || t("this channel");
    const ok = await confirmDialog({
      title: t("Delete {name}?", { name }),
      message: t("It disappears from every viewer's guide."),
      confirmLabel: t("Delete {name}", { name }),
      tone: "danger",
    });
    if (!ok) return;
    try {
      await tv.deleteChannel(id);
      toast.success(t("Channel deleted"));
      await go({ kind: "guide" }, { force: true });
      await reloadAll();
    } catch (e) {
      toast.error(t("Couldn't delete the channel"), { description: errorMessage(e) });
    }
  };

  const handleUpdateChannels = useCurrentCallback(
    async (ids: string[], patch: ChannelPatch): Promise<boolean> => {
      if (bulkLock.current || !ids.length) return false;
      const targets = new Set(ids);
      bulkLock.current = true;
      setBulkBusy(true);
      try {
        await tv.updateChannels(ids, patch);
        setAllChannels((cs) =>
          (cs ?? []).map((c) => (targets.has(c.id) ? patchChannel(c, patch) : c)),
        );
        if (selectedChannel && targets.has(selectedChannel.id)) {
          setSelectedChannel((prev) => (prev ? patchChannel(prev, patch) : prev));
        }
        toast.success(t("Channels updated"));
        await reloadAll();
        return true;
      } catch (e) {
        toast.error(t("Couldn't update the channels"), { description: errorMessage(e) });
        return false;
      } finally {
        bulkLock.current = false;
        setBulkBusy(false);
      }
    },
  );

  const applyChannelNumbers = useCurrentCallback(
    async (numbers: Record<string, string>): Promise<boolean> => {
      const ids = Object.keys(numbers);
      if (bulkLock.current || !ids.length) return false;
      bulkLock.current = true;
      setBulkBusy(true);
      try {
        await tv.updateChannels(ids, { numbers });
        setAllChannels((cs) =>
          (cs ?? []).map((c) => {
            const nextNum = Object.prototype.hasOwnProperty.call(numbers, c.id)
              ? numbers[c.id]
              : undefined;
            return nextNum !== undefined ? { ...c, number: nextNum } : c;
          }),
        );
        const selectedNext =
          selectedChannel && Object.prototype.hasOwnProperty.call(numbers, selectedChannel.id)
            ? numbers[selectedChannel.id]
            : undefined;
        if (selectedChannel && selectedNext !== undefined) {
          const updated = { ...selectedChannel, number: selectedNext };
          setSelectedChannel(updated);
          if (draft && !dirty && draft.channel.id === selectedChannel.id) {
            const nextDraft = studioDraft(updated);
            setDraft(nextDraft);
            setBaseline(JSON.stringify(nextDraft));
          }
        }
        await reloadAll();
        return true;
      } catch (e) {
        toast.error(t("Couldn't reorder channels"), { description: errorMessage(e) });
        return false;
      } finally {
        bulkLock.current = false;
        setBulkBusy(false);
      }
    },
  );

  const handleRenumberChannels = useCurrentCallback(async (plan: ReorderPlan): Promise<boolean> => {
    const ok = await applyChannelNumbers(plan.nextNumbers);
    if (!ok) return false;
    let undone = false;
    const shiftedCount = plan.shifted.length;
    const title = t("{name} moved to #{number}", {
      name: plan.channelName,
      number: plan.toNumber,
    });
    const description =
      shiftedCount > 0
        ? tn(
            shiftedCount,
            "{count} following channel shifted.",
            "{count} following channels shifted.",
          )
        : t("Was #{previous}.", { previous: plan.fromNumber });
    toast.success(title, {
      description,
      duration: 10000,
      action: {
        label: t("Undo"),
        onClick: () => {
          if (undone) return;
          undone = true;
          void applyChannelNumbers(plan.prevNumbers).then((restored) => {
            if (restored) toast.success(t("Channel order restored"));
          });
        },
      },
    });
    return true;
  });

  const handleSetChannelsApp = useCurrentCallback(
    async (change: ChannelAppChange): Promise<boolean> => {
      if (bulkLock.current) return false;
      bulkLock.current = true;
      setBulkBusy(true);
      try {
        await tv.setChannelsApp(change);
        const patchOne = <T extends { hiddenApps?: string[]; shownApps?: string[] }>(c: T): T =>
          patchChannel(c, change.show ? { showApps: [change.app] } : { hideApps: [change.app] });

        if ("ownerGroup" in change) {
          setOwnerGroups((gs) =>
            gs.map((g) => {
              if (g.id !== change.ownerGroup) return g;
              const hiddenApps = g.hiddenApps?.filter((a) => a !== change.app);
              return {
                ...g,
                apps: { ...(g.apps ?? {}), [change.app]: change.show },
                hiddenApps: hiddenApps?.length ? hiddenApps : undefined,
              };
            }),
          );
        }

        setAllChannels((cs) => {
          if (!cs) return cs;
          if ("channels" in change) {
            const ids = new Set(change.channels);
            return cs.map((c) => (ids.has(c.id) ? patchOne(c) : c));
          }
          if ("ownerGroup" in change) {
            const g = ownerGroups.find((x) => x.id === change.ownerGroup);
            if (!g) return cs;
            const sourceNames = new Map(sources.map((s) => [s.id, s.name]));
            const matchesGroup = compileOwnerGroupMatcher(g, sourceNames);
            return cs.map((c) =>
              matchesGroup(c) ? patchChannel(c, { clearApps: [change.app] }) : c,
            );
          }
          return cs.map((c) => {
            if (change.source === "virtual") {
              return c.kind === "virtual" ? patchOne(c) : c;
            }
            if (c.kind === "virtual" || (c.sourceId ?? "") !== change.source) return c;
            if (change.group !== undefined && (c.group ?? "") !== change.group) return c;
            return patchOne(c);
          });
        });

        await reloadAll();
        return true;
      } catch (e) {
        toast.error(t("Couldn't update the channels"), { description: errorMessage(e) });
        return false;
      } finally {
        bulkLock.current = false;
        setBulkBusy(false);
      }
    },
  );

  const handleSaveOwnerGroups = useCurrentCallback(async (next: OwnerGroup[]): Promise<boolean> => {
    try {
      const saved = await tv.saveGroups(next);
      setOwnerGroups(saved);
      return true;
    } catch (e) {
      toast.error(t("Couldn't save your groups"), { description: errorMessage(e) });
      return false;
    }
  });

  // ---- header actions ---------------------------------------------------------------

  const editing = (selection.kind === "channel" || selection.kind === "new") && !!draft;
  const updateParams = useCurrentCallback(setParams);
  const openNewSource = (kind: "playlist" | "guide" | "addon" | "xtream") => {
    void (async () => {
      if (dirty && !(await confirmDiscard())) return;
      updateParams((prev) => {
        const p = new URLSearchParams(prev);
        p.delete("channel");
        p.delete("guide");
        p.delete("source");
        p.set("new", kind);
        return p;
      });
    })();
  };
  const addGuideSource = () => openNewSource("guide");
  const addPlaylistSource = () => openNewSource("playlist");
  const closeSourceDrawer = useCurrentCallback(() => {
    setParams((prev) => {
      const p = new URLSearchParams(prev);
      p.delete("source");
      p.delete("new");
      return p;
    });
  });

  const libraryQuery = params.get("q");

  const actions = (
    <div className="flex flex-wrap items-center gap-2">
      <SubtabNav
        ariaLabel={t("Live TV")}
        active={activeTab}
        onSelect={(next) => {
          if (next === "library") navigate("/livetv/library");
          else if (next === "looks") navigate("/livetv/looks");
          else if (next === "guide") navigate("/livetv/guide");
          else navigate("/livetv");
        }}
        tabs={[
          { key: "channels", label: t("Channels") },
          { key: "guide", label: t("Guide") },
          { key: "looks", label: stylesName() },
          { key: "library", label: t("Library") },
        ]}
        className="[&_button]:py-1.5"
      />
      {/* One search box in the same place on every Live TV tab. */}
      <div className="relative w-[min(15rem,calc(100vw-8rem))] shrink-0">
        <Search
          size={13}
          className="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 text-fg-subtle"
          aria-hidden
        />
        <Input
          type="search"
          value={activeTab === "library" ? (libraryQuery ?? "") : q}
          onChange={(e) => {
            const value = e.currentTarget.value;
            if (activeTab !== "library") setQ(value);
            else
              updateParams((p) => {
                if (value) p.set("q", value);
                else p.delete("q");
                return p;
              });
          }}
          placeholder={
            activeTab === "library"
              ? t("Search Library")
              : activeTab === "looks"
                ? styleCopy("Search styles…")
                : t("Find a channel or guide…")
          }
          aria-label={
            activeTab === "library"
              ? t("Search Library")
              : activeTab === "looks"
                ? styleCopy("Search styles")
                : t("Search channels")
          }
          className="h-8 pl-7 text-sm"
        />
      </div>
      <AddMenu
        onChannel={() => void go({ kind: "new" })}
        onPersonal={() =>
          void (async () => {
            if (await go({ kind: "new" })) await startNew("personal");
          })()
        }
        onSource={(k) => openNewSource(k)}
        onAddon={() => openNewSource("addon")}
      />
    </div>
  );

  // ---- render -----------------------------------------------------------------------------

  if (tab === "sources") return null;

  const state = error ? (
    <div className="p-6">
      <ErrorPanel error={error} />
    </div>
  ) : null;

  const aside =
    activeTab === "library" ? (
      <div className="app-scroll min-h-0 flex-1 lg:overflow-y-auto">
        <LibraryAside />
      </div>
    ) : activeTab === "looks" ? (
      <div className="app-scroll min-h-0 flex-1 lg:overflow-y-auto">
        <LooksAside
          looks={looks}
          reels={reels}
          query={q}
          onSelect={(kind, id) => setParams(new URLSearchParams({ [kind]: id }))}
        />
      </div>
    ) : null;

  const main =
    activeTab === "library" ? (
      <div className="p-4 sm:p-6">
        <Library />
      </div>
    ) : activeTab === "looks" ? (
      <LooksGallery
        looks={looks}
        query={q}
        onNew={() => setParams(new URLSearchParams({ look: "new" }))}
        onSelect={(id) => setParams(new URLSearchParams({ look: id }))}
      />
    ) : null;

  const channelOpen = selection.kind === "new" || selection.kind === "channel";
  const guideOpen = selection.kind === "map";
  const guideChannel =
    selection.kind === "map" && selectedChannel?.id === selection.id ? selectedChannel : undefined;
  const missingChannel =
    selection.kind === "channel" &&
    allChannels !== null &&
    !savedChannel &&
    !draft &&
    !selectionLoading;
  const guideSource = guideSourceId ? sources.find((x) => x.id === guideSourceId) : undefined;
  const selectedID = selection.kind === "channel" ? selection.id : null;

  const channelTitle =
    selection.kind === "new"
      ? t("New virtual channel")
      : draft?.channel.kind === "iptv"
        ? t("Edit channel")
        : t("Virtual channel");

  const empty =
    allChannels !== null &&
    allChannels.length === 0 &&
    activeTab === "channels" &&
    selection.kind === "guide";

  const emptyState = (
    <div className="mx-auto flex w-full max-w-3xl flex-col items-center px-6 py-12 text-center sm:py-20">
      <div className="mb-6 rounded-2xl bg-primary/10 p-5">
        <Tv size={40} className="text-primary" aria-hidden />
      </div>
      <h1 className="text-3xl font-semibold tracking-tight">{t("No channels yet")}</h1>
      <p className="mt-4 max-w-xl text-fg-muted">
        {t(
          "Start with four channels from the 1980s, 1990s, 2000s and 2010s, add IPTV, or create your own.",
        )}
      </p>
      <div className="mt-8 flex flex-wrap items-center justify-center gap-3">
        <Button
          disabled={addingDecades}
          onClick={async () => {
            setAddingDecades(true);
            try {
              await tv.addDecades();
              await reloadAll();
            } catch (e) {
              toast.error(errorMessage(e));
            } finally {
              setAddingDecades(false);
            }
          }}
        >
          {t("Add the four decade channels")}
        </Button>
        <Button variant="outline" onClick={() => openNewSource("playlist")}>
          {t("Add IPTV")}
        </Button>
      </div>
      <Button className="mt-5" variant="ghost" onClick={() => void go({ kind: "new" })}>
        {t("New virtual channel")}
      </Button>
      <p className="mt-8 text-sm text-fg-muted">
        {t("A familiar channel guide for your library and IPTV, ready to watch in your apps.")}
      </p>
    </div>
  );

  return (
    <>
      {empty ? (
        emptyState
      ) : (
        <Workspace
          title={t("Live TV")}
          subtitle={
            liveCount !== null ? (
              <>
                {tn(liveCount, "{count} channel on air", "{count} channels on air")}
                {missingCount && missingCount > 0 ? (
                  <>
                    {" · "}
                    <Link
                      to="/livetv/guide-data"
                      onClick={() => setMappingJumpNonce((n) => n + 1)}
                      className="font-medium text-warning underline-offset-2 hover:underline"
                    >
                      {tn(missingCount, "{count} needs a guide", "{count} need a guide")}
                    </Link>
                  </>
                ) : null}
              </>
            ) : undefined
          }
          actions={actions}
          asideLabel={t("Lineup")}
          asideClassName={cn(
            "[scrollbar-gutter:auto] lg:overflow-hidden",
            channelOpen && "lg:relative lg:z-[60]",
          )}
          mainLabel={t("Live TV workspace")}
          aside={aside}
          main={main}
        >
          {state ??
            (activeTab === "guide" ? (
              <GuidePage
                key={tab === "guide-data" ? `mapping-${mappingJumpNonce}` : "grid"}
                initialSub={tab === "guide-data" ? "mapping" : "grid"}
                channels={sorted}
                sources={sources}
                ownerGroups={ownerGroups}
                coverage={coverage}
                query={search}
                guideUntil={guideUntil}
                missingCount={missingCount}
                selectedChannelId={selectedID}
                guideSourceId={guideSourceId}
                onSelectGuideSource={(id) =>
                  setParams(id ? new URLSearchParams({ gsrc: id }) : new URLSearchParams())
                }
                onSelectChannel={selectChannel}
                onInspect={inspectProgramme}
                onPlay={setPreviewChannel}
                onMapChannel={(id) => void go({ kind: "map", id })}
                onToggleFavorite={toggleFavorite}
                onAddGuideSource={addGuideSource}
                onAddPlaylistSource={addPlaylistSource}
                onEditSource={(id) => void go({ kind: "source", id })}
                onReload={reloadAll}
              />
            ) : activeTab === "channels" ? (
              <ChannelsWorkspace
                channels={allChannels}
                sources={sources}
                coverage={coverage}
                ownerGroups={ownerGroups}
                query={search}
                filter={filter}
                onFilterChange={setFilter}
                sort={channelSort}
                onSortChange={setChannelSort}
                missingCount={missingCount}
                selectedChannelId={selectedID}
                busy={bulkBusy}
                onOpenChannel={selectChannel}
                onSaveOwnerGroups={handleSaveOwnerGroups}
                onSetChannelsApp={handleSetChannelsApp}
                onUpdateChannels={handleUpdateChannels}
                onRenumberChannels={handleRenumberChannels}
                onReorderSources={async (ids) => {
                  try {
                    const next = await tv.saveSourceOrder(ids);
                    setSources(next);
                    setCoverageNonce((n) => n + 1);
                    if (selection.kind === "channel" && selectedChannel?.id) {
                      const refreshed = await tv.channel(selectedChannel.id);
                      setSelectedChannel(refreshed);
                    }
                  } catch (e) {
                    toast.error(errorMessage(e));
                  }
                }}
              />
            ) : undefined)}
        </Workspace>
      )}
      <Drawer
        open={guideOpen}
        onClose={() => void go({ kind: "guide" })}
        title={guideChannel ? t("Guide for {channel}", { channel: guideChannel.name }) : t("Guide")}
        documentTitle={
          guideChannel ? t("Guide for {channel}", { channel: guideChannel.name }) : t("Guide")
        }
        description={t(
          "Pick the guide channel its listings come from. Nothing changes until you choose Use this guide.",
        )}
        size="md"
        footer={guideChannel ? guideFooter : undefined}
      >
        {guideChannel ? (
          <GuideMatchPanel
            key={guideChannel.id}
            channel={guideChannel}
            sourceName={sources.find((s) => s.id === guideChannel.sourceId)?.name ?? ""}
            guideSourceKind={
              sources.find((s) => s.id === (guideChannel.guideSourceId || guideChannel.sourceId))
                ?.kind
            }
            coverage={coverage?.get(guideChannel.id)}
            onUse={async (c) => {
              await quickSave(
                c,
                c.guideId
                  ? t("{name} now takes its listings from {guide}", {
                      name: c.name,
                      guide: c.guideId,
                    })
                  : t("Guide removed from {name}", { name: c.name }),
              );
              await go({ kind: "guide" }, { force: true });
            }}
            onOpenChannel={() => void go({ kind: "channel", id: guideChannel.id })}
            sources={sources}
            onReload={reloadAll}
            setFooter={setGuideFooter}
            scopeSourceId={guideSource?.id}
            scopeName={
              guideSource
                ? guideSource.kind === "plex"
                  ? plexLineupName(guideSource.name)
                  : guideSource.name
                : undefined
            }
          />
        ) : allChannels !== null ? (
          <p className="text-sm text-fg-muted">{t("This channel no longer exists.")}</p>
        ) : null}
      </Drawer>
      <Drawer
        open={channelOpen}
        onClose={() => void go({ kind: "guide" })}
        title={channelTitle}
        documentTitle={draft?.channel.name || channelTitle}
        bodyClassName={draft ? "overflow-y-auto p-0" : undefined}
        description={
          draft?.channel.kind === "iptv"
            ? t("Stream, guide and who can watch.")
            : t("What plays, when it plays, and what happens when it runs out.")
        }
        size="xl"
        panelClassName="contain-layout contain-paint will-change-transform"
        footer={
          editing ? (
            <div className="flex w-full flex-wrap items-center gap-2">
              {dirty ? <Badge variant="warning">{t("Unsaved changes")}</Badge> : null}
              <div className="ml-auto flex gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  disabled={(!dirty && selection.kind !== "new") || saving}
                  onClick={() => {
                    if (selection.kind === "new") void go({ kind: "guide" }, { force: true });
                    else setDraft(JSON.parse(baseline) as StudioDraft);
                  }}
                >
                  <Undo2 size={14} /> {selection.kind === "new" ? t("Cancel") : t("Discard")}
                </Button>
                <Button
                  size="sm"
                  disabled={(!dirty && selection.kind !== "new") || saving || hasInvalidMergeRule}
                  onClick={() => void saveDraft()}
                >
                  <Save size={14} />{" "}
                  {saving
                    ? t("Saving…")
                    : selection.kind === "new"
                      ? t("Create channel")
                      : t("Save channel")}
                </Button>
              </div>
            </div>
          ) : null
        }
      >
        {selection.kind === "new" && describing && draft && (
          <ChannelDescribeDialog
            channel={draft.channel}
            onClose={() => {
              setDescribing(false);
              if (!draft.channel.name) setDraft(null);
            }}
            onApply={(channel) => setDraft(studioDraft(channel))}
          />
        )}
        <ChannelDrawerBody>
          {selection.kind === "new" && !draft ? (
            <TemplateGallery onPick={(k) => void startNew(k)} />
          ) : draft ? (
            <ChannelStudio
              draft={draft}
              onChange={setDraft}
              isNew={selection.kind === "new"}
              dirty={dirty}
              guide={drawerProgrammes}
              coverage={
                draft.channel.id
                  ? (coverage?.get(draft.channel.id) ?? {
                      channelId: draft.channel.id,
                      programmes: covered(draft.channel) ? 1 : 0,
                    })
                  : undefined
              }
              sources={sources}
              users={users}
              policies={policies}
              collections={collections}
              now={now}
              onDelete={() => void deleteChannel()}
              onMapGuide={async (c) => {
                await quickSave(
                  c,
                  t("{name} now takes listings from {guide}", {
                    name: c.name,
                    guide: c.guideId ?? "",
                  }),
                );
                setCoverageNonce((n) => n + 1);
              }}
            />
          ) : (
            <p className="text-sm text-fg-muted">
              {missingChannel ? t("That channel no longer exists.") : t("Loading…")}
            </p>
          )}
        </ChannelDrawerBody>
      </Drawer>
      <ProgrammeInspector
        open={inspectOpen}
        inspected={
          inspected && {
            ...inspected,
            channel:
              (selectedChannel?.id === inspected.channel.id ? selectedChannel : undefined) ??
              (allChannels?.find((c) => c.id === inspected.channel.id) as Channel | undefined) ??
              inspected.channel,
          }
        }
        now={now}
        onClose={() => setInspectOpen(false)}
        onExitComplete={() => setInspected(null)}
        onPlay={(c) => {
          setInspectOpen(false);
          setPreviewChannel(c);
        }}
        onOpenChannel={(c) => {
          setInspectOpen(false);
          selectChannel(c.id);
        }}
        onToggleFavorite={toggleFavorite}
      />
      {previewChannel ? (
        <LivePreview
          key={previewChannel.id}
          channel={previewChannel}
          onClose={() => setPreviewChannel(null)}
        />
      ) : null}
      <SourceDrawer
        sourceId={sourceId}
        adding={addingSource}
        sources={sourcesLoaded ? sources : null}
        onClose={closeSourceDrawer}
        onSaved={async (s) => {
          await reloadAll();
          updateParams((prev) => {
            const p = new URLSearchParams(prev);
            p.delete("new");
            p.set("source", s.id);
            return p;
          });
        }}
        onDeleted={async () => {
          const deletedId = sourceId;
          updateParams((prev) => {
            const p = new URLSearchParams(prev);
            p.delete("source");
            p.delete("new");
            if (deletedId && p.get("gsrc") === deletedId) p.delete("gsrc");
            return p;
          });
          await reloadAll();
        }}
      />
      <LookSurfaces looks={looks} reload={reloadLooks} />
    </>
  );
}

function useCurrentParams() {
  return useSearchParams();
}

function AddMenu({
  onChannel,
  onPersonal,
  onSource,
  onAddon,
}: {
  onChannel: () => void;
  onPersonal: () => void;
  onSource: (k: SourceKind) => void;
  onAddon: () => void;
}) {
  const [open, setOpen] = useState(false);
  const phone = useMediaQuery("(max-width: 639px)");
  const navigate = useNavigate();
  const id = useId();
  const menu = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() =>
      menu.current?.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus(),
    );
    return () => cancelAnimationFrame(frame);
  }, [open]);
  const item = (Icon: typeof Sparkles, title: string, hint: string, fn: () => void) => (
    <button
      type="button"
      role="menuitem"
      tabIndex={-1}
      onClick={() => {
        setOpen(false);
        fn();
      }}
      className="flex w-full items-start gap-3 rounded-md px-2.5 py-2 text-left transition-colors hover:bg-surface-hover focus-visible:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/50"
    >
      <Icon size={16} className="mt-0.5 shrink-0 text-primary" aria-hidden />
      <span>
        <span className="block text-sm font-medium text-fg">{title}</span>
        <span className="block text-xs text-fg-muted">{hint}</span>
      </span>
    </button>
  );
  const section = (key: string, label: string, children: React.ReactNode) => (
    <div role="group" aria-labelledby={`${id}-${key}`} className="py-1">
      <p id={`${id}-${key}`} className="px-2.5 py-1.5 text-xs font-medium text-fg-subtle">
        {label}
      </p>
      {children}
    </div>
  );
  return (
    <Popover
      open={open}
      onOpenChange={setOpen}
      align={phone ? "start" : "end"}
      className="w-80 max-w-[calc(100vw-1.5rem)] p-1"
      trigger={
        <Button
          ref={trigger}
          size="sm"
          onClick={() => setOpen((o) => !o)}
          aria-haspopup="menu"
          aria-expanded={open}
          aria-controls={`${id}-menu`}
          onKeyDown={(e) => {
            if (e.key === "ArrowDown" || e.key === "ArrowUp") {
              e.preventDefault();
              setOpen(true);
            }
          }}
        >
          <Plus size={14} />
          {t("Add")}
        </Button>
      }
    >
      <div
        ref={menu}
        id={`${id}-menu`}
        role="menu"
        aria-label={t("Add to Live TV")}
        className="app-scroll max-h-[calc(100dvh-10rem)] overflow-y-auto"
        onKeyDown={(e) => {
          const items = [
            ...(menu.current?.querySelectorAll<HTMLButtonElement>('[role="menuitem"]') ?? []),
          ];
          const current = items.indexOf(document.activeElement as HTMLButtonElement);
          let next: number | undefined;
          if (e.key === "ArrowDown") next = (current + 1) % items.length;
          if (e.key === "ArrowUp") next = (current - 1 + items.length) % items.length;
          if (e.key === "Home") next = 0;
          if (e.key === "End") next = items.length - 1;
          if (next !== undefined) {
            e.preventDefault();
            items[next]?.focus();
          }
          if (e.key === "Escape") {
            e.preventDefault();
            setOpen(false);
            trigger.current?.focus();
          }
          if (e.key === "Tab") setOpen(false);
        }}
      >
        {section(
          "yours",
          t("Your channels"),
          <>
            {item(
              Sparkles,
              t("Virtual channel"),
              t("Programme your own from the catalogue"),
              onChannel,
            )}
            {item(
              UserRound,
              t("Personal channel"),
              t("A channel made for each viewer"),
              onPersonal,
            )}
            {item(Palette, styleCopy("Style"), t("Design your channel’s on-air graphics"), () =>
              navigate("/livetv/looks?look=new"),
            )}
            {item(Upload, t("Upload to library"), t("Bumpers, stings, logos and fonts"), () =>
              navigate("/livetv/library?upload=1"),
            )}
            {item(ListVideo, t("Break reel"), t("Clips to play between programmes"), () =>
              navigate("/livetv/looks?reel=new"),
            )}
          </>,
        )}
        <div role="separator" className="mx-2 border-t border-border" />
        {section(
          "bring",
          t("Bring in channels"),
          <>
            {item(Tv, t("IPTV source"), t("Channels from M3U or Xtream"), () =>
              onSource("playlist"),
            )}
            {item(Puzzle, t("Stremio plugin"), t("Live channels from a Stremio plugin"), onAddon)}
          </>,
        )}
        <div role="separator" className="mx-2 border-t border-border" />
        {section(
          "guide",
          t("Program guide"),
          item(CalendarRange, t("Guide source"), t("XMLTV listings for any channel"), () =>
            onSource("guide"),
          ),
        )}
      </div>
    </Popover>
  );
}

function TemplateGallery({ onPick }: { onPick: (key: string) => void }) {
  return (
    <div className="@container mx-auto max-w-4xl space-y-6">
      <div className="space-y-1">
        <p className="text-xs font-semibold uppercase tracking-wider text-primary">
          {t("New channel")}
        </p>
        <h2 className="text-2xl font-semibold tracking-tight text-fg">
          {t("What should it play?")}
        </h2>
      </div>
      <button
        type="button"
        onClick={() => onPick("describe")}
        className="group flex w-full max-w-xl items-center gap-4 rounded-2xl border border-primary/30 bg-gradient-to-br from-primary/15 via-primary/5 to-surface p-5 text-left shadow-xs transition-all hover:-translate-y-0.5 hover:border-primary/60 hover:shadow-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
      >
        <span className="grid size-12 shrink-0 place-items-center rounded-xl bg-primary text-primary-fg shadow-md">
          <Sparkles size={22} />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block text-base font-semibold">{t("Describe it")}</span>
          <span className="mt-1 block text-sm text-fg-muted">
            {t("Programming, schedule and a look, built with you.")}
          </span>
        </span>
        <Plus size={18} className="shrink-0 text-primary" />
      </button>
      <div className="grid gap-3 @md:grid-cols-2 @4xl:grid-cols-3">
        {TEMPLATES.map((template) => (
          <button
            key={template.key}
            type="button"
            onClick={() => onPick(template.key)}
            aria-label={t("{name} template", { name: template.name })}
            className="group relative overflow-hidden rounded-2xl border border-border p-4 text-left shadow-xs transition-all hover:-translate-y-0.5 hover:shadow-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
            style={{
              background: `linear-gradient(140deg, hsl(${template.hue} 70% 50% / 0.18), transparent 60%), rgb(var(--ch-surface))`,
            }}
          >
            <span
              className="mb-6 grid size-10 place-items-center rounded-xl text-white shadow-md"
              style={{
                background: `linear-gradient(135deg, hsl(${template.hue} 70% 55%), hsl(${(template.hue + 40) % 360} 70% 40%))`,
              }}
            >
              {template.key === "blank" ? <Plus size={18} /> : <Tv size={18} />}
            </span>
            <span className="block text-base font-semibold text-fg">{template.name}</span>
            <span className="block text-xs text-fg-muted">{template.tagline}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
