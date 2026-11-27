import { LayoutTemplates } from "../../pse/LayoutTemplates";
import { DiscoveryCardsNotice } from "../../pse/DiscoveryCardsSetup";
// Recommendations: the designer for every page of rows (design §2.6). A slim
// toolbar picks the page, who it's edited for and how it's previewed; the
// page settings live in a drawer. The page itself takes the full width: it
// is the only list of rows, and it is the live preview.
// One drawer adds and edits rows. Every edit goes into one page draft, kept
// in local storage until Save or Discard.
import {
  Badge,
  Button,
  EmptyState,
  Input,
  Spinner,
  Switch,
  cn,
  toast,
} from "@braedonsaunders/appkit-ui";
import {
  AlertTriangle,
  CalendarClock,
  Check,
  ChevronDown,
  ChevronRight,
  Eye,
  EyeOff,
  GripVertical,
  Library,
  Pin,
  Plus,
  RotateCcw,
  Save,
  Settings2,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  Undo2,
  User,
  Users,
} from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel } from "../../components/feedback";
import { SubtabNav } from "../../components/layout";
import { useViewers, viewerName } from "../../components/pickers";
import { ToolbarPicker } from "../../components/ToolbarPicker";
import { Workspace } from "../../components/workspace";
import { t, tn } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { ApiError, api, errorMessage, isAbort } from "../../lib/api";
import type { Device, Viewer } from "../../lib/contract";
import { useAsync } from "../../lib/hooks";
import {
  PAGE_HOME,
  changeCount,
  clearOverride,
  deviceLabel,
  effectiveLength,
  effectiveMode,
  effectiveRow,
  experienceLabel,
  formatPreviewAt,
  hasOverride,
  insertRow,
  normalizePins,
  loadDraft,
  moveRow,
  hasCollectionCardSelection,
  peekDraft,
  removeRow,
  replaceRow,
  rowForSource,
  samePage,
  scheduleSummary,
  setOverride,
  storeDraft,
  toLocalInput,
  withMode,
  type PageDoc,
  type PageLength,
  type PageMode,
  type PageRow,
  type PageSource,
  type PageSummary,
  type PagePreview,
  type PreviewRow,
  type RowPage,
} from "../../lib/pages";
import { RowDrawer, type DrawerState } from "./RowDrawer";
import { KindChip, PlacementChip, RowBody, VelaOnlyChip } from "./RowVisuals";
import { TuningPanel } from "./TuningPanel";

const DEVICES: Device[] = ["tv", "mobile", "desktop", "web"];

// ---------------------------------------------------------------- small parts

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="space-y-2" aria-label={title}>
      <div>
        <h3 className="text-xs font-semibold uppercase tracking-wider text-fg-subtle">{title}</h3>
        {hint ? <p className="mt-0.5 text-sm text-fg-muted">{hint}</p> : null}
      </div>
      <div className="space-y-3 rounded-xl border border-border bg-surface p-3.5 text-sm">
        {children}
      </div>
    </section>
  );
}

function Line({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="space-y-1 border-t border-border-subtle pt-3 first:border-0 first:pt-0">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <span className="font-medium text-fg">{label}</span>
        {children}
      </div>
      {hint ? <p className="text-xs text-fg-muted">{hint}</p> : null}
    </div>
  );
}

function LengthInput({
  label,
  value,
  placeholder,
  onChange,
}: {
  label: string;
  value: number | undefined;
  placeholder?: number;
  onChange: (n: number | undefined) => void;
}) {
  return (
    <Input
      aria-label={label}
      type="number"
      inputMode="numeric"
      min={1}
      max={50}
      className="h-8 w-16"
      placeholder={placeholder ? String(placeholder) : undefined}
      value={value ?? ""}
      onChange={(e) => {
        const raw = e.currentTarget.value;
        if (raw === "") return onChange(undefined);
        const n = Math.round(Number(raw));
        if (Number.isFinite(n)) onChange(Math.min(50, Math.max(1, n)));
      }}
    />
  );
}

/**
 * The gap between two rows: "+" inserts a new row at exactly this position.
 * It has its own height in the flow, so it never covers a row's posters or
 * text; on wide screens the label shows on hover, on touch screens always.
 */
function InsertGap({
  at,
  onAdd,
  active,
  onDrop,
  dropIndex,
  dropTarget,
}: {
  at: number;
  onAdd: (at: number) => void;
  active?: boolean;
  onDrop?: () => void;
  /** The row a drop here lands before (-1: the end), for touch drags. */
  dropIndex?: number;
  dropTarget?: boolean;
}) {
  const [over, setOver] = useState(false);
  const line = cn(
    "h-px flex-1 transition-colors",
    active || over || dropTarget
      ? "h-0.5 bg-primary"
      : "bg-border-subtle group-hover/gap:bg-primary/50 group-focus-within/gap:bg-primary/50",
  );
  return (
    <div
      className="group/gap flex h-10 items-center gap-2 px-2"
      data-drop-index={dropIndex}
      onDragOver={(e) => {
        if (!onDrop) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        onDrop?.();
      }}
    >
      <div className={line} />
      <button
        type="button"
        aria-label={t("Add a row at position {at}", { at })}
        onClick={() => onAdd(at)}
        className={cn(
          "inline-flex h-7 shrink-0 items-center gap-1 rounded-full border px-2.5 text-xs font-medium transition-colors",
          active
            ? "border-primary bg-primary text-primary-fg"
            : "border-dashed border-border bg-bg text-fg-muted hover:border-solid hover:border-primary hover:text-primary focus-visible:border-primary focus-visible:text-primary",
        )}
      >
        <Plus size={12} />
        {active ? (
          t("New row at #{at}", { at })
        ) : (
          <span className="sm:hidden sm:group-hover/gap:inline sm:group-focus-within/gap:inline">
            {t("Add a row here")}
          </span>
        )}
      </button>
      <div className={line} />
    </div>
  );
}

function Folded({
  icon,
  title,
  hint,
  children,
}: {
  icon: ReactNode;
  title: string;
  hint: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded-xl border border-dashed border-border">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left hover:bg-surface/60"
      >
        <span className="flex items-center gap-2 text-sm text-fg">
          {icon}
          <span>
            <b>{title}</b>
            <span className="block text-xs text-fg-muted">{hint}</span>
          </span>
        </span>
        {open ? (
          <ChevronDown size={16} className="text-fg-subtle" />
        ) : (
          <ChevronRight size={16} className="text-fg-subtle" />
        )}
      </button>
      {open ? <div className="space-y-1 border-t border-border-subtle p-2">{children}</div> : null}
    </div>
  );
}

// ---------------------------------------------------------------- one row in the page

interface DisplayRow {
  row: PreviewRow;
  /** 1-based position among shown rows; ghosts sit between without one. */
  position?: number;
  index: number;
}

function PageRowView({
  item,
  pageRow,
  curated,
  showRating,
  personal,
  selected,
  fresh,
  justViewer,
  sourceTitle,
  pluginName,
  libraryName,
  viewers,
  onEdit,
  onPin,
  onHide,
  onReset,
  onDragStart,
  onDragEnd,
  onDropBefore,
  onTouchDrag,
  dropTarget,
  continued,
}: {
  item: DisplayRow;
  pageRow: PageRow | undefined;
  curated: boolean;
  showRating: boolean;
  /** The source's Personal flag (titles differ per viewer). */
  personal: boolean;
  selected: boolean;
  fresh: boolean;
  justViewer: string | null;
  sourceTitle: string;
  pluginName?: string;
  libraryName?: string;
  viewers: Viewer[];
  onEdit: () => void;
  onPin: () => void;
  onHide: () => void;
  onReset: () => void;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDropBefore: () => void;
  /** Touch screens have no HTML drag: the handle drags with the finger. */
  onTouchDrag: (e: ReactPointerEvent) => void;
  /** A finger dragging a row is over this one. */
  dropTarget: boolean;
  /** The next library of a one-row-per-library row: its pin is the block's. */
  continued?: boolean;
}) {
  const r = item.row;
  const [over, setOver] = useState(false);
  const mode = pageRow?.placement.mode ?? r.mode ?? "auto";
  const audience = pageRow?.placement.audience;
  const names = (ids: number[] | undefined) =>
    (ids ?? [])
      .map((id) => viewers.find((v) => v.id === id))
      .filter(Boolean)
      .map((v) => viewerName(v!))
      .join(", ");
  const audienceText =
    audience?.mode === "only"
      ? t("Only {viewers}", { viewers: names(audience.viewers) })
      : audience?.mode === "except"
        ? t("Everyone except {viewers}", { viewers: names(audience.viewers) })
        : "";
  const when = pageRow ? scheduleSummary(pageRow.placement.when, pageRow.placement.devices) : "";
  // Every row on the page moves, Ether's own rows too: dropping one of
  // them keeps it on the page, pinned where it landed.
  const draggable = !r.ghost;
  const misplaced =
    !continued &&
    mode === "pinned" &&
    Boolean(pageRow?.placement.position && item.position) &&
    pageRow!.placement.position !== item.position;
  return (
    <section
      aria-label={
        item.position ? t("#{n}: {title}", { n: item.position, title: r.title }) : r.title
      }
      draggable={draggable}
      data-drop-index={item.index}
      onDragStart={(e) => {
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", r.hubId);
        onDragStart();
      }}
      onDragEnd={onDragEnd}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        onDropBefore();
      }}
      className={cn(
        "group/row rounded-xl px-2 py-3 transition-colors hover:bg-surface/60",
        selected && "bg-surface/70 ring-2 ring-primary/70",
        fresh && "border border-dashed border-primary/70 bg-primary-subtle/20",
        r.ghost && "opacity-55",
        (over || dropTarget) && "border-t-2 border-primary",
      )}
    >
      {/* The whole header opens the row's drawer; its own buttons keep their job. */}
      <div
        className="mb-1.5 flex cursor-pointer flex-wrap items-center gap-x-2 gap-y-1"
        onClick={(e) => {
          if (!(e.target as HTMLElement).closest("button, a, input")) onEdit();
        }}
      >
        {draggable ? (
          <span
            className="-m-1.5 cursor-grab touch-none p-1.5 text-fg-subtle"
            onPointerDown={onTouchDrag}
            title={
              curated && mode !== "pinned"
                ? t("Drag to move")
                : t("Drag to move; drop to pin it there")
            }
            aria-hidden
          >
            <GripVertical size={14} />
          </span>
        ) : (
          <span className="w-3.5" />
        )}
        <span
          className={cn(
            "grid size-6 place-items-center rounded-md text-xs font-semibold tabular-nums",
            misplaced
              ? "bg-warning-subtle text-warning"
              : mode === "pinned"
                ? "bg-success-subtle text-success"
                : "bg-bg-subtle text-fg-muted",
          )}
          title={
            misplaced
              ? t("Pinned to #{pin}, shown at #{at}: see Why here", {
                  pin: pageRow!.placement.position!,
                  at: item.position!,
                })
              : undefined
          }
        >
          {item.position ?? "–"}
        </span>
        <button
          type="button"
          onClick={onEdit}
          title={t("Edit this row")}
          className="text-left text-base font-semibold text-fg hover:underline"
        >
          {r.title}
        </button>
        {fresh ? <Badge variant="info">{t("New · in the draft")}</Badge> : null}
        {r.ghost ? (
          <Badge variant="outline" className="gap-1 font-normal">
            <CalendarClock size={11} /> {t("Not right now")}
          </Badge>
        ) : null}
        {continued ? null : (
          <PlacementChip mode={mode} position={pageRow?.placement.position} curated={curated} />
        )}
        <KindChip personal={personal} />
        {r.present.surface ? <VelaOnlyChip /> : null}
        {audienceText ? (
          <Badge variant="outline" className="gap-1 font-normal">
            <Users size={11} /> {audienceText}
          </Badge>
        ) : null}
        {when ? (
          <Badge variant="outline" className="gap-1 font-normal">
            <CalendarClock size={11} /> {when}
          </Badge>
        ) : null}
        {justViewer ? (
          <Badge variant="info" className="gap-1">
            <User size={11} /> {t("Just {viewer}", { viewer: justViewer })}
            <button
              type="button"
              className="ml-1 underline"
              onClick={onReset}
              aria-label={t("Reset {row} to Everyone", { row: r.title })}
            >
              {t("Reset to Everyone")}
            </button>
          </Badge>
        ) : null}
        <div className="ml-auto flex gap-0.5 opacity-100 sm:opacity-0 sm:group-focus-within/row:opacity-100 sm:group-hover/row:opacity-100">
          {r.ghost ? null : (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 gap-1 px-2 text-xs"
              onClick={onPin}
              aria-label={t("Pin {row} here", { row: r.title })}
            >
              <Pin size={13} /> {t("Pin here")}
            </Button>
          )}
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 px-2 text-xs"
            onClick={onHide}
            aria-label={t("Hide {row}", { row: r.title })}
          >
            <EyeOff size={13} /> {t("Hide")}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-7 gap-1 px-2 text-xs"
            onClick={onEdit}
            aria-label={t("Edit {row}", { row: r.title })}
          >
            <SlidersHorizontal size={13} /> {t("Edit")}
          </Button>
        </div>
      </div>
      <p className="mb-2 max-w-3xl text-xs text-fg-muted">
        <span className="font-medium text-fg">{t("Why here:")} </span>
        {r.why || r.reason}
        {sourceTitle ? <span className="ml-1 text-fg-subtle">· {sourceTitle}</span> : null}
        {libraryName ? <span className="ml-1 text-fg-subtle">· {libraryName}</span> : null}
        {r.note ? <span className="ml-1 text-fg-subtle">· {r.note}</span> : null}
      </p>
      {(r.flags ?? []).map((f) => (
        <p
          key={f}
          className="mb-2 mr-2 inline-flex items-center gap-1.5 rounded-md bg-warning-subtle px-2 py-1 text-xs text-warning"
        >
          <AlertTriangle size={12} /> {f}
        </p>
      ))}
      <RowBody
        row={r}
        showRating={showRating}
        style={pageRow?.look.style}
        card={pageRow?.look.card}
        numbered={pageRow?.look.numbered ?? undefined}
        pluginName={pluginName}
      />
    </section>
  );
}

/** Fan-out rows stay one block: a bracket names the row the owner made. */
function Block({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div
      className="relative mt-2.5 rounded-2xl border border-border-subtle pl-1"
      role="group"
      aria-label={label}
    >
      <span className="absolute -top-2.5 left-4 bg-bg px-1.5 text-[11px] font-medium text-fg-subtle">
        {label}
      </span>
      {children}
    </div>
  );
}

// ---------------------------------------------------------------- the designer

interface Pending {
  row: PageRow;
  beforeId: string | null;
  at: number;
  /**
   * A row on the page that isn't stored yet, opened to edit: it looks and
   * behaves like every other row, and becomes a stored row (keepEtherRow)
   * the moment it's changed. Opening it changes nothing.
   */
  onPage?: boolean;
}

export default function Recommendations() {
  const [params, setParams] = useSearchParams();
  const { locale } = useLocale();
  const addParam = params.get("add");
  const pageKey = params.get("page") ?? (addParam ? null : PAGE_HOME);
  const editingFor = params.get("editing") ? Number(params.get("editing")) : null;
  const asParam = params.get("as") ? Number(params.get("as")) : null;
  const previewAs = editingFor ?? asParam;
  const setQuery = useCallback(
    (patch: Record<string, string | null>) => {
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          for (const [k, v] of Object.entries(patch)) {
            if (v === null) next.delete(k);
            else next.set(k, v);
          }
          return next;
        },
        { replace: true },
      );
    },
    [setParams],
  );

  const { viewers, error: viewersError } = useViewers();
  const pages = useAsync(async () => {
    const r = await api.pages();
    return (Array.isArray(r) ? r : ((r as { pages?: PageSummary[] }).pages ?? [])) as PageSummary[];
  }, []);
  const doc = useAsync<PageDoc | null>(
    (signal) => (pageKey ? api.page(pageKey, signal) : Promise.resolve(null)),
    [pageKey],
  );
  const sources = useAsync(
    (signal) => (pageKey ? api.pageSources(pageKey, signal) : Promise.resolve(null)),
    [pageKey],
  );

  // ---- the draft ------------------------------------------------------------
  const [draft, setDraft] = useState<RowPage | null>(null);
  const [adopted, setAdopted] = useState<PageDoc | null | undefined>(undefined);
  if (doc.data !== adopted) {
    setAdopted(doc.data);
    if (doc.data) {
      const kept = loadDraft(doc.data.page.key, doc.data.page.revision);
      setDraft(kept ?? doc.data.page);
    } else setDraft(null);
  }
  const saved = doc.data?.page ?? null;
  const dirty = Boolean(draft && saved && !samePage(draft, saved));
  useEffect(() => {
    if (!draft || !saved) return;
    storeDraft(dirty ? draft : null, saved.key, saved.revision);
  }, [draft, saved, dirty]);
  // No "Leave site?" prompt: the draft is kept in local storage, which is
  // what "kept if you leave" promises.

  const [pending, setPending] = useState<Pending | null>(null);
  const [drawer, setDrawer] = useState<DrawerState | null>(null);
  const [device, setDevice] = useState<Device>("tv");
  const [at, setAt] = useState<Date | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsTab, setSettingsTab] = useState<"settings" | "tuning">("settings");
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState(false);

  // Links from Collections and library tabs: ?row= opens a row, ?add= a new one.
  const handledLink = useRef<string | null>(null);
  useEffect(() => {
    if (!draft || !sources.data) return;
    const rowParam = params.get("row");
    const sig = `${draft.key}|${rowParam}|${addParam}`;
    if (handledLink.current === sig) return;
    handledLink.current = sig;
    if (rowParam && draft.rows.some((r) => r.id === rowParam)) {
      setDrawer({ kind: "edit", rowId: rowParam });
    } else if (addParam) {
      const [kind, ref] = [addParam.split(":")[0]!, addParam.split(":").slice(1).join(":")];
      const source = sources.data.sources.find((s) => s.kind === kind);
      const refParam = source?.params?.find((p) => p.type === "ref");
      const at = draft.rows.length + 1;
      if (source) {
        const row = rowForSource(source, ref && refParam ? { [refParam.key]: ref } : {}, {
          page: draft,
          position: at,
          editingFor,
        });
        // "Add to a page…" only said which page: Ether places it (Show), at the end.
        setPending({
          row: { ...row, placement: withMode(row.placement, "show") },
          beforeId: null,
          at,
        });
        setDrawer({ kind: "add", at, picking: false });
      } else {
        setDrawer({ kind: "add", at, picking: true });
      }
    }
  }, [draft, sources.data, params, addParam, editingFor]);

  // An Ether row opened just to look at it changes nothing until it's
  // edited, and a row still missing a required choice (which collection)
  // never goes to the preview.
  const pendingSource = pending
    ? sources.data?.sources.find((s) => s.kind === pending.row.source.kind)
    : undefined;
  const pendingComplete = Boolean(
    pending &&
    hasCollectionCardSelection(pending.row) &&
    !(pendingSource?.params ?? []).some((p) => p.required && !pending.row.source.params?.[p.key]),
  );
  const pendingShown = pending && pendingComplete && !pending.onPage ? pending : null;
  const previewPage = useMemo(
    () =>
      draft && pendingShown
        ? normalizePins(
            insertRow(draft, pendingShown.row, pendingShown.beforeId),
            pendingShown.row.id,
          )
        : draft && normalizePins(draft),
    [draft, pendingShown],
  );

  // ---- the live preview -------------------------------------------------------
  const [preview, setPreview] = useState<PagePreview | null>(null);
  const [previewError, setPreviewError] = useState<unknown>(null);
  const [previewing, setPreviewing] = useState(false);
  const [localOrder, setLocalOrder] = useState<string[] | null>(null);
  const atIso = (at ?? null)?.toISOString() ?? null;
  const previewSig = previewPage
    ? JSON.stringify({
        p: dirty || pendingShown ? previewPage : null,
        k: previewPage.key,
        r: previewPage.revision,
        v: previewAs,
        device,
        atIso,
      })
    : null;
  useEffect(() => {
    if (!previewSig || !previewPage) return;
    const ctl = new AbortController();
    setPreviewing(true);
    const timer = window.setTimeout(() => {
      api
        .previewPage(
          previewPage.key,
          {
            page: dirty || pendingShown ? previewPage : undefined,
            viewer: previewAs ?? undefined,
            device,
            at: atIso ?? new Date().toISOString(),
          },
          ctl.signal,
        )
        .then((p) => {
          setPreview(p);
          setPreviewError(null);
          setLocalOrder(null);
        })
        .catch((e) => {
          if (!isAbort(e)) setPreviewError(e);
        })
        .finally(() => {
          if (!ctl.signal.aborted) setPreviewing(false);
        });
    }, 250);
    return () => {
      window.clearTimeout(timer);
      ctl.abort();
    };
    // previewSig captures every input.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [previewSig]);

  // ---- impact -------------------------------------------------------------------
  const [impact, setImpact] = useState<{ viewers: number; withChanges: number } | null>(null);
  useEffect(() => {
    if (!dirty || !draft) {
      setImpact(null);
      return;
    }
    const ctl = new AbortController();
    const timer = window.setTimeout(() => {
      api
        .pageImpact(draft.key, draft, ctl.signal)
        .then(setImpact)
        .catch(() => setImpact(null));
    }, 600);
    return () => {
      window.clearTimeout(timer);
      ctl.abort();
    };
  }, [draft, dirty]);

  // ---- derived ----------------------------------------------------------------------
  const viewerById = useMemo(() => new Map((viewers ?? []).map((v) => [v.id, v])), [viewers]);
  const editingViewer = editingFor != null ? (viewerById.get(editingFor) ?? null) : null;
  const previewViewer = previewAs != null ? (viewerById.get(previewAs) ?? null) : null;
  const summary = pages.data?.find((p) => p.key === pageKey);
  const pageLibrary = doc.data?.library ?? null;
  const libraries = useMemo(
    () => doc.data?.libraries ?? sources.data?.libraries ?? (pageLibrary ? [pageLibrary] : []),
    [doc.data, sources.data, pageLibrary],
  );
  const mode: PageMode = draft ? effectiveMode(draft, editingFor) : "personalized";
  const curated = mode === "curated";
  const isLibraryPage = Boolean(pageLibrary) || (pageKey ?? "").startsWith("library:");
  const sourceByKind = useMemo(
    () => new Map((sources.data?.sources ?? []).map((s) => [s.kind, s])),
    [sources.data],
  );
  const savedIds = useMemo(() => new Set(saved?.rows.map((r) => r.id) ?? []), [saved]);
  const libraryName = (id?: number) => libraries.find((l) => l.id === id)?.name;

  const display: DisplayRow[] = useMemo(() => {
    let rows = preview?.rows ?? [];
    if (localOrder) {
      const pos = new Map(localOrder.map((id, i) => [id, i]));
      rows = [...rows].sort((a, b) => (pos.get(a.hubId) ?? 0) - (pos.get(b.hubId) ?? 0));
    }
    let n = 0;
    return rows.map((row, index) => ({ row, index, position: row.ghost ? undefined : ++n }));
  }, [preview, localOrder]);

  // ---- edits --------------------------------------------------------------------------
  /**
   * Every edit goes through here, and pins stay unique: the row that just
   * got a position (`prefer`) keeps it, rows it lands on move down one.
   */
  const update = (fn: (p: RowPage) => RowPage, prefer?: string) =>
    setDraft((d) => (d ? normalizePins(fn(d), prefer) : d));
  /** Applies a Look/Placement change for whoever is being edited for. */
  const updateRow = (row: PageRow) => {
    if (!draft) return;
    if (pending && pending.row.id === row.id) {
      if (!pending.onPage) return setPending({ ...pending, row });
      // The first change stores the row, where it is; the drawer stays open on it.
      keepEtherRow(row, pending.beforeId);
      setPending(null);
      setDrawer({ kind: "edit", rowId: row.id, tab: drawer?.tab });
      return;
    }
    if (editingFor != null) {
      update(
        (p) => setOverride(p, editingFor, row.id, { look: row.look, placement: row.placement }),
        row.id,
      );
    } else update((p) => replaceRow(p, row), row.id);
  };
  const rowOf = (id: string | undefined) =>
    id && draft ? draft.rows.find((r) => r.id === id) : undefined;
  const effective = (id: string | undefined) => {
    const r = rowOf(id);
    return r && draft ? effectiveRow(draft, r, editingFor) : undefined;
  };

  /** The page row displayed at or after a position, to insert or move before it. */
  const rowIdAtOrAfter = (index: number, skip?: string): string | null => {
    for (let i = index; i < display.length; i++) {
      const id = display[i]!.row.rowId;
      if (id && id !== skip && rowOf(id)) return id;
    }
    return null;
  };

  const openAdd = (at: number) => {
    setPending(null);
    setDrawer({ kind: "add", at, picking: true });
  };
  /**
   * Opens the drawer for a row that isn't on the page yet (one of Ether's
   * picks): that source, prefilled, at `at`. Adding stores it there, pinned
   * (or shown on a Curated page, or when it has no position).
   */
  const openUnstored = (
    kind: string,
    at: number,
    beforeIndex: number | null,
    opts: { library?: number; show?: boolean; onPage?: boolean } = {},
  ) => {
    if (!draft) return;
    const source = sourceByKind.get(kind);
    if (!source) return openAdd(at);
    const row = rowForSource(
      source,
      {},
      {
        page: draft,
        position: at,
        // A row already on the page is everyone's row; edits for a viewer go in their variant.
        editingFor: opts.onPage ? null : editingFor,
        libraries: opts.library && !pageLibrary ? [opts.library] : undefined,
      },
    );
    // A row already on the page keeps behaving as it does (Auto) until the
    // owner changes its placement; one added from the list is Shown.
    const mode = opts.show || (opts.onPage && curated) ? "show" : opts.onPage ? "auto" : null;
    setPending({
      row: mode ? { ...row, placement: withMode(row.placement, mode) } : row,
      beforeId: beforeIndex == null ? null : rowIdAtOrAfter(beforeIndex),
      at,
      onPage: opts.onPage,
    });
    setDrawer({ kind: "add", at, picking: false });
  };
  /** Every row on the page opens its drawer: stored rows to edit, the rest to add. */
  const openRow = (d: DisplayRow) => {
    const id = d.row.rowId;
    if (id && pending?.row.id === id) return;
    if (id && rowOf(id)) {
      setPending(null);
      setDrawer({ kind: "edit", rowId: id });
      return;
    }
    const at = d.position ?? display.slice(0, d.index).filter((x) => !x.row.ghost).length + 1;
    openUnstored(d.row.kind, at, d.index, { library: d.row.library, onPage: true });
  };
  const pickSource = (source: PageSource, params: Record<string, string>) => {
    if (!draft || drawer?.kind !== "add") return;
    const index = display.findIndex((d) => d.position === drawer.at);
    const beforeId = index < 0 ? null : rowIdAtOrAfter(index);
    const keep = pending?.row;
    const collectionCards = source.kind === "collection-cards";
    const row = rowForSource(source, params, {
      page: draft,
      position: drawer.at,
      editingFor,
      libraries: collectionCards ? undefined : keep?.scope.libraries,
      perLibrary: collectionCards
        ? undefined
        : (keep?.scope.perLibrary ?? (source.split && !pageLibrary ? true : undefined)),
    });
    setPending({
      row: keep
        ? {
            ...row,
            id: keep.id,
            look: collectionCards && keep.source.kind !== source.kind ? {} : keep.look,
            placement: keep.placement,
          }
        : row,
      beforeId,
      at: drawer.at,
    });
    setDrawer({ ...drawer, picking: false });
  };
  const changeSource = () => {
    if (!drawer) return;
    if (drawer.kind === "add") return setDrawer({ ...drawer, picking: true });
    // Editing: the same drawer becomes the picker; the row keeps its look and placement.
    const row = rowOf(drawer.rowId);
    if (!row || !draft) return;
    const at = (display.find((d) => d.row.rowId === row.id)?.position ?? draft.rows.length) || 1;
    const beforeId = draft.rows[draft.rows.findIndex((r) => r.id === row.id) + 1]?.id ?? null;
    update((p) => removeRow(p, row.id));
    setPending({ row, beforeId, at });
    setDrawer({ kind: "add", at, picking: true });
  };
  const closeDrawer = () => {
    setPending(null);
    setDrawer(null);
    if (params.get("row") || params.get("add")) setQuery({ row: null, add: null });
  };
  /**
   * Keeps one of Ether's rows (a pick, Continue Watching…) on the page.
   * Editing for one viewer, the page gets Ether's own row (Auto, for
   * everyone, as if Ether picked it) and the change goes into that viewer's
   * variant, so nobody else's page changes.
   */
  const keepEtherRow = (row: PageRow, beforeId: string | null) => {
    if (editingFor == null) {
      update((p) => insertRow(p, row, beforeId), row.id);
      return;
    }
    const everyone = { mode: "everyone" as const };
    const base: PageRow = {
      ...row,
      name: undefined,
      look: {},
      placement: { mode: "auto", audience: everyone },
    };
    update(
      (p) =>
        setOverride(insertRow(p, base, beforeId), editingFor, row.id, {
          look: row.look,
          placement: { ...row.placement, audience: everyone },
        }),
      row.id,
    );
  };
  const commitAdd = () => {
    if (!pending || !pendingComplete) return;
    if (pending.onPage) keepEtherRow(pending.row, pending.beforeId);
    else update((p) => insertRow(p, pending.row, pending.beforeId));
    setPending(null);
    setDrawer(null);
    if (params.get("add")) setQuery({ add: null });
  };
  const removeOpen = () => {
    if (pending?.onPage) {
      // Not stored yet: removing it keeps it off the page.
      keepEtherRow({ ...pending.row, placement: withMode(pending.row.placement, "hide") }, null);
      return closeDrawer();
    }
    if (drawer?.kind !== "edit") return;
    update((p) => removeRow(p, drawer.rowId));
    closeDrawer();
  };

  const pinAt = (rowId: string, position: number) => {
    const row = effective(rowId);
    if (!row) return;
    updateRow({ ...row, placement: withMode(row.placement, "pinned", position) });
  };
  /** Pins any row on the page where it is; a row not stored yet is stored, pinned. */
  const pinRow = (d: DisplayRow) => {
    if (!d.position) return;
    if (d.row.rowId && rowOf(d.row.rowId)) return pinAt(d.row.rowId, d.position);
    const source = sourceByKind.get(d.row.kind);
    if (!source || !draft) return;
    const row = rowForSource(
      source,
      {},
      {
        page: draft,
        position: d.position,
        editingFor: null,
        libraries: d.row.library && !pageLibrary ? [d.row.library] : undefined,
      },
    );
    keepEtherRow(
      { ...row, placement: withMode(row.placement, "pinned", d.position) },
      rowIdAtOrAfter(d.index),
    );
  };
  const hideRow = (r: PreviewRow) => {
    if (r.rowId) {
      const row = effective(r.rowId);
      if (row) updateRow({ ...row, placement: withMode(row.placement, "hide") });
      return;
    }
    const source = sourceByKind.get(r.kind);
    if (!source || !draft) return;
    const row = rowForSource(source, {}, { page: draft, position: 1, editingFor: null });
    keepEtherRow({ ...row, placement: withMode(row.placement, "hide") }, null);
  };
  const addPick = (kind: string) => {
    const source = sourceByKind.get(kind);
    if (!source || !draft) return;
    const row = rowForSource(source, {}, { page: draft, position: 1, editingFor: null });
    keepEtherRow({ ...row, placement: withMode(row.placement, "show") }, null);
  };

  // Drag and drop: the dropped order stays put until the next preview arrives.
  const dragging = useRef<string | null>(null);
  const dropBefore = (targetIndex: number | null) => {
    const hub = dragging.current;
    dragging.current = null;
    if (!hub || !draft) return;
    const dragged = display.find((d) => d.row.hubId === hub)?.row;
    if (!dragged || dragged.ghost) return;
    // One of Ether's rows (no page row yet) moves alone.
    const rowId = dragged.rowId && rowOf(dragged.rowId) ? dragged.rowId : null;
    const block = rowId
      ? display.filter((d) => d.row.rowId === rowId).map((d) => d.row.hubId)
      : [hub];
    const rest = display.map((d) => d.row.hubId).filter((h) => !block.includes(h));
    const targetHub = targetIndex == null ? null : display[targetIndex]?.row.hubId;
    if (targetHub && block.includes(targetHub)) {
      if (!rowId) return;
      // Inside a one-row-per-library block: the drop reorders its libraries.
      const pr = rowOf(rowId);
      const from = dragged?.library;
      const to = display[targetIndex!]?.row.library;
      if (!pr || !from || !to || from === to || editingFor != null) return;
      const supported = sourceByKind.get(pr.source.kind)?.libraries;
      const base = pr.scope.libraries?.length
        ? [...pr.scope.libraries]
        : (supported ?? display.filter((d) => d.row.rowId === rowId).map((d) => d.row.library!));
      const order = base.filter((id) => id !== from);
      const at = order.indexOf(to);
      order.splice(at < 0 ? order.length : at, 0, from);
      setLocalOrder(null);
      update((p) => replaceRow(p, { ...pr, scope: { ...pr.scope, libraries: order } }));
      return;
    }
    let insertAt = targetHub ? rest.indexOf(targetHub) : rest.length;
    if (insertAt < 0) insertAt = rest.length;
    const order = [...rest.slice(0, insertAt), ...block, ...rest.slice(insertAt)];
    setLocalOrder(order);
    const byHub = new Map(display.map((d) => [d.row.hubId, d.row]));
    let beforeId: string | null = null;
    for (const h of order.slice(insertAt + block.length)) {
      const id = byHub.get(h)?.rowId;
      if (id && id !== rowId && rowOf(id)) {
        beforeId = id;
        break;
      }
    }
    const position = order.slice(0, insertAt).filter((h) => !byHub.get(h)?.ghost).length + 1;
    // The page is the list: every pinned row the drop moved takes its new
    // number, so dropping at #N pins the row there and the rows that were
    // at #N and below move down one, their pins with them.
    const positions = (hubs: string[]) => {
      const at = new Map<string, number>();
      let n = 0;
      for (const h of hubs) {
        const r = byHub.get(h);
        if (!r || r.ghost) continue;
        n++;
        if (r.rowId && !at.has(r.rowId)) at.set(r.rowId, n);
      }
      return at;
    };
    const before = positions(display.map((d) => d.row.hubId));
    const after = positions(order);
    const renumber = (p: RowPage, moved: string | null): RowPage => {
      let next = p;
      for (const [id, n] of after) {
        if (id === moved || before.get(id) === n) continue;
        const base = next.rows.find((r) => r.id === id);
        if (!base) continue;
        const eff = effectiveRow(next, base, editingFor);
        if (eff.placement.mode !== "pinned") continue;
        const placement = { ...eff.placement, position: n };
        next =
          editingFor != null
            ? setOverride(next, editingFor, id, { look: eff.look, placement })
            : replaceRow(next, { ...base, placement });
      }
      return next;
    };
    if (!rowId) {
      // A row Ether placed becomes a stored row where it landed.
      const source = sourceByKind.get(dragged.kind);
      if (!source) return setLocalOrder(null);
      const row = rowForSource(
        source,
        {},
        {
          page: draft,
          position,
          editingFor: null,
          libraries: dragged.library && !pageLibrary ? [dragged.library] : undefined,
        },
      );
      update((p) => renumber(p, null));
      keepEtherRow(
        {
          ...row,
          placement: curated
            ? withMode(row.placement, "show")
            : withMode(row.placement, "pinned", position),
        },
        beforeId,
      );
      return;
    }
    const row = effective(rowId);
    if (!row) return;
    // Curated: the list order is the page order; a pinned row keeps its pin
    // and takes the new position, as on a Personalized page.
    const placement =
      curated && row.placement.mode !== "pinned"
        ? row.placement
        : withMode(row.placement, "pinned", position);
    if (editingFor != null) {
      if (curated && row.placement.mode !== "pinned") return;
      update(
        (p) => setOverride(renumber(p, rowId), editingFor, rowId, { look: row.look, placement }),
        rowId,
      );
    } else {
      update(
        (p) => replaceRow(moveRow(renumber(p, rowId), rowId, beforeId), { ...row, placement }),
        rowId,
      );
    }
  };

  // Touch screens have no HTML drag and drop: the row's handle follows the
  // finger, the row or gap under it lights up, and lifting drops there.
  const [touchOver, setTouchOver] = useState<number | null>(null);
  const touchDrag = (e: ReactPointerEvent, hub: string) => {
    if (e.pointerType === "mouse") return;
    e.preventDefault();
    dragging.current = hub;
    const scroller =
      (e.currentTarget as HTMLElement).closest<HTMLElement>("[data-workspace-scroll]") ??
      (document.scrollingElement as HTMLElement | null);
    let over: number | null = null;
    const move = (ev: PointerEvent) => {
      ev.preventDefault();
      const el = document.elementFromPoint(ev.clientX, ev.clientY)?.closest("[data-drop-index]");
      over = el ? Number(el.getAttribute("data-drop-index")) : null;
      setTouchOver(over);
      if (!scroller) return;
      const r = scroller.getBoundingClientRect();
      if (ev.clientY < r.top + 120) scroller.scrollBy(0, -18);
      else if (ev.clientY > Math.min(r.bottom, window.innerHeight) - 80) scroller.scrollBy(0, 18);
    };
    const end = (ev: PointerEvent) => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", end);
      window.removeEventListener("pointercancel", end);
      setTouchOver(null);
      if (ev.type === "pointerup" && over != null) dropBefore(over < 0 ? null : over);
      else dragging.current = null;
    };
    window.addEventListener("pointermove", move, { passive: false });
    window.addEventListener("pointerup", end);
    window.addEventListener("pointercancel", end);
  };

  // ---- page settings (Everyone, or the viewer's variant) ----------------------------
  const length: PageLength = draft ? effectiveLength(draft, editingFor) : {};
  const setLength = (l: PageLength) => {
    const clean: PageLength = {};
    if (l.rows) clean.rows = l.rows;
    const byDevice = Object.fromEntries(Object.entries(l.byDevice ?? {}).filter(([, v]) => v));
    if (Object.keys(byDevice).length) clean.byDevice = byDevice;
    if (editingFor != null) {
      update((p) => {
        const variants = [...(p.variants ?? [])];
        const i = variants.findIndex((v) => v.viewer === editingFor);
        const base = i >= 0 ? variants[i]! : { viewer: editingFor, rows: [] };
        const next = { ...base, length: clean };
        if (i >= 0) variants[i] = next;
        else variants.push(next);
        return { ...p, variants };
      });
    } else update((p) => ({ ...p, length: clean }));
  };
  // One choice: Personalized means Ether adds its picks, Curated means it
  // adds nothing. A viewer's Personalized version needs the page's picks on
  // (the engine ignores them on a Curated page).
  const withPicks = (p: RowPage): RowPage => ({
    ...p,
    picks: p.mode !== "curated" || (p.variants ?? []).some((v) => v.mode === "personalized"),
  });
  const setMode = (m: PageMode) => {
    if (editingFor != null) {
      update((p) => {
        const variants = [...(p.variants ?? [])];
        const i = variants.findIndex((v) => v.viewer === editingFor);
        const base = i >= 0 ? variants[i]! : { viewer: editingFor, rows: [] };
        const next = { ...base, mode: m === p.mode ? undefined : m };
        if (i >= 0) variants[i] = next;
        else variants.push(next);
        return withPicks({ ...p, variants });
      });
      return;
    }
    // Rows keep their placement: a Curated page shows them in its order and
    // ignores Pinned/Show/Auto, so switching back restores every pin.
    update((p) => withPicks({ ...p, mode: m }));
  };
  const [perDevice, setPerDevice] = useState(false);

  // ---- save / discard / reset ----------------------------------------------------------
  const save = async () => {
    if (!draft || !saved) return;
    setSaving(true);
    try {
      const next = await api.putPage(normalizePins(draft));
      storeDraft(null, saved.key, saved.revision);
      doc.setData(next);
      void pages.reload();
      setConflict(false);
      toast.success(t("Page saved"), {
        description: t("Apps pick it up the next time they load it."),
      });
    } catch (e) {
      if (e instanceof ApiError && (e.status === 409 || e.status === 412)) {
        setConflict(true);
        toast.error(t("Someone else saved this page"), {
          description: t("Your draft is kept. Load their version to start again from it."),
        });
      } else toast.error(t("Couldn't save the page"), { description: errorMessage(e) });
    } finally {
      setSaving(false);
    }
  };
  const discard = () => {
    if (!saved) return;
    storeDraft(null, saved.key, saved.revision);
    setDraft(saved);
    setPending(null);
    setDrawer(null);
  };
  const loadTheirs = async () => {
    if (saved) storeDraft(null, saved.key, saved.revision);
    setConflict(false);
    setAdopted(undefined);
    await doc.reload();
  };
  const [confirmReset, setConfirmReset] = useState(false);
  const resetPage = async () => {
    if (!pageKey) return;
    try {
      if (saved) storeDraft(null, saved.key, saved.revision);
      const next = await api.resetPage(pageKey);
      doc.setData(next);
      void pages.reload();
      setConfirmReset(false);
      toast.success(t("Page reset to its default rows"));
    } catch (e) {
      toast.error(t("Couldn't reset the page"), { description: errorMessage(e) });
    }
  };

  // ---- pickers -----------------------------------------------------------------------
  // The picker counts the draft where there is one: this page's from the
  // designer, other pages' from their kept drafts. This page also shows
  // what its preview shows, so the two never disagree.
  const onPage = preview?.summary ? preview.summary.rows + (preview.summary.appOnly ?? 0) : null;
  const pageOptions = useMemo(
    () =>
      (pages.data ?? []).map((p) => {
        const current = p.key === pageKey;
        const kept = current ? draft : peekDraft(p.key);
        const edited = current ? dirty : Boolean(kept);
        const pageMode = kept ? kept.mode : p.mode;
        return {
          value: p.key,
          label: edited ? `${p.name} •` : p.name,
          group: p.key === PAGE_HOME ? t("Everywhere") : experienceLabel(p.experience),
          icon: p.key === PAGE_HOME ? <Sparkles size={14} /> : <Library size={14} />,
          hint: [
            current && onPage != null
              ? tn(onPage, "{count} row on the page", "{count} rows on the page")
              : null,
            pageMode === "curated" ? t("Curated") : t("Personalized"),
            edited ? t("unsaved changes") : null,
          ]
            .filter(Boolean)
            .join(" · "),
        };
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [pages.data, locale, pageKey, draft, dirty, onPage],
  );
  const changedViewers = useMemo(() => {
    const counts = new Map<number, number>();
    for (const v of draft?.variants ?? []) counts.set(v.viewer, changeCount(draft!, v.viewer));
    return [...(viewers ?? [])]
      .sort(
        (a, b) =>
          (counts.get(b.id) ?? 0) - (counts.get(a.id) ?? 0) ||
          viewerName(a).localeCompare(viewerName(b)),
      )
      .map((v) => ({ v, changes: counts.get(v.id) ?? 0 }));
  }, [draft, viewers]);
  /** A viewer's name, with their age limit when they have one ("Luca · up to PG"). */
  const viewerLabel = (v: Viewer) =>
    v.ageLimit
      ? t("{viewer} · up to {limit}", { viewer: viewerName(v), limit: v.ageLimit })
      : viewerName(v);
  const editingOptions = [
    {
      value: "everyone",
      label: t("Everyone"),
      hint: t("Changes apply to every viewer"),
      icon: <Users size={14} />,
    },
    ...changedViewers.map(({ v, changes }) => ({
      value: String(v.id),
      label: t("Just {viewer}", { viewer: viewerLabel(v) }),
      hint: changes ? tn(changes, "{count} change", "{count} changes") : t("No changes"),
      icon: <User size={14} />,
    })),
  ];

  // ---- layout ------------------------------------------------------------------------
  const pageName = summary?.name ?? (pageKey === PAGE_HOME ? t("Home") : t("This page"));
  const viewerChanges = editingFor != null && draft ? changeCount(draft, editingFor) : 0;

  const actions = (
    <div className="flex flex-wrap items-center gap-2">
      {dirty ? <Badge variant="warning">{t("Unsaved changes · kept if you leave")}</Badge> : null}
      {dirty && editingFor != null ? (
        <span className="text-xs text-fg-muted">
          {t("Affects only {viewer}", {
            viewer: editingViewer ? viewerName(editingViewer) : "",
          })}
        </span>
      ) : dirty && impact ? (
        <span className="text-xs text-fg-muted">
          {t("Affects {viewers} viewers · {changes} have changes", {
            viewers: impact.viewers,
            changes: impact.withChanges,
          })}
        </span>
      ) : null}
      <Button variant="outline" size="sm" onClick={discard} disabled={!dirty || saving}>
        <Undo2 size={14} /> {t("Discard")}
      </Button>
      <Button size="sm" onClick={() => void save()} disabled={!dirty || saving}>
        <Save size={14} /> {saving ? t("Saving…") : t("Save page")}
      </Button>
    </div>
  );

  const drawerRow: PageRow | null =
    drawer?.kind === "add"
      ? (pending?.row ?? null)
      : drawer?.kind === "edit"
        ? (effective(drawer.rowId) ?? null)
        : null;
  const drawerSource = drawerRow ? sourceByKind.get(drawerRow.source.kind) : undefined;
  const editingRowId = drawer?.kind === "edit" ? drawer.rowId : pending?.row.id;

  if (!pageKey) {
    return (
      <Workspace title={t("Recommendations")} subtitle={t("Which page should the row go on?")}>
        <div className="mx-auto max-w-lg space-y-2 p-6">
          {(pages.data ?? []).map((p) => (
            <button
              key={p.key}
              type="button"
              onClick={() => setQuery({ page: p.key })}
              className="flex w-full items-center justify-between rounded-xl border border-border bg-surface px-4 py-3 text-left hover:border-primary"
            >
              <span className="font-medium text-fg">{p.name}</span>
              <ChevronRight size={16} className="text-fg-subtle" />
            </button>
          ))}
          {pages.loading ? <Spinner size={18} /> : null}
        </div>
      </Workspace>
    );
  }

  // ---- page settings drawer ---------------------------------------------------------
  const modeOptions: { value: PageMode; label: string; line: string; hint: string }[] = [
    {
      value: "personalized",
      label: t("Personalized"),
      line: t("Each viewer gets rows that fit them"),
      hint: t(
        "Each viewer gets their own page: your pinned and shown rows always appear, and Ether fills the rest with rows picked for them.",
      ),
    },
    {
      value: "curated",
      label: t("Curated"),
      line: t("Exactly your rows, in your order"),
      hint: t(
        "Every viewer gets the same rows. Ether adds none, and a row with nothing to show is skipped.",
      ),
    },
  ];
  const tuningBody = draft ? (
    <div className="space-y-4">
      {curated ? (
        <p className="rounded-lg bg-bg-subtle px-3 py-2 text-sm text-fg-muted">
          {t(
            "This page is Curated, so Ether adds no rows to it. Switch it to Personalized to tune how Ether picks.",
          )}
        </p>
      ) : null}
      <TuningPanel
        tuning={editingFor != null ? undefined : draft.tuning}
        inherited={editingFor != null ? draft.tuning : undefined}
        onChange={(tuning) => editingFor == null && update((p) => ({ ...p, tuning }))}
        pageName={pageName}
        viewerName={editingViewer ? viewerName(editingViewer) : null}
        dials={editingFor == null && !curated}
      />
    </div>
  ) : null;
  const settingsBody = draft ? (
    <div className="space-y-5">
      <Section title={t("Mode")}>
        <div role="radiogroup" aria-label={t("Mode")} className="grid gap-2">
          {modeOptions.map((o) => {
            const on = mode === o.value;
            return (
              <button
                key={o.value}
                type="button"
                role="radio"
                aria-checked={on}
                onClick={() => setMode(o.value)}
                className={cn(
                  "flex items-start gap-3 rounded-lg border px-3 py-2.5 text-left transition-colors",
                  on
                    ? "border-primary bg-primary-subtle/40"
                    : "border-border hover:border-primary/60",
                )}
              >
                <span
                  className={cn(
                    "mt-0.5 grid size-4 shrink-0 place-items-center rounded-full border",
                    on ? "border-primary bg-primary text-primary-fg" : "border-border",
                  )}
                  aria-hidden
                >
                  {on ? <Check size={10} strokeWidth={3} /> : null}
                </span>
                <span className="min-w-0">
                  <span className="block font-medium text-fg">
                    {o.label}: <span className="font-normal">{o.line}</span>
                  </span>
                  <span className="mt-0.5 block text-xs text-fg-muted">{o.hint}</span>
                </span>
              </button>
            );
          })}
        </div>
      </Section>
      {curated ? null : (
        <Section title={t("Length")}>
          <Line
            label={t("Up to")}
            hint={t(
              "Pinned and shown rows always appear, even past this. Ether fills the rest up to this length.",
            )}
          >
            <span className="flex items-center gap-2">
              <LengthInput
                label={t("Up to how many rows")}
                value={length.rows}
                placeholder={doc.data?.defaultLength}
                onChange={(n) => setLength({ ...length, rows: n })}
              />
              {t("rows")}
            </span>
          </Line>
          <button
            type="button"
            aria-expanded={perDevice || Boolean(length.byDevice)}
            onClick={() => setPerDevice(!perDevice)}
            className="flex w-full items-center justify-between border-t border-border-subtle pt-3 text-left text-fg-muted"
          >
            {t("Different length per device")}
            {perDevice || length.byDevice ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
          </button>
          {perDevice || length.byDevice
            ? DEVICES.map((d) => (
                <Line key={d} label={deviceLabel(d)}>
                  <LengthInput
                    label={t("Rows on {device}", { device: deviceLabel(d) })}
                    value={length.byDevice?.[d]}
                    placeholder={length.rows || doc.data?.defaultLength}
                    onChange={(n) =>
                      setLength({ ...length, byDevice: { ...length.byDevice, [d]: n } })
                    }
                  />
                </Line>
              ))
            : null}
        </Section>
      )}
      {isLibraryPage ? (
        <Section title={t("This library")}>
          <Line
            label={t("Skip titles Home already shows")}
            hint={t(
              "Titles the viewer's Home leads with stay off this page, so switching tabs always shows something new.",
            )}
          >
            <Switch
              aria-label={t("Skip titles Home already shows")}
              checked={draft.skipHome ?? true}
              disabled={editingFor != null}
              onChange={(e) => {
                const skipHome = e.currentTarget.checked;
                update((p) => ({ ...p, skipHome: skipHome ? undefined : false }));
              }}
            />
          </Line>
        </Section>
      ) : null}
      <Section title={t("Shown in")}>
        <p className="text-fg-muted">{appsLine(summary?.apps, pageKey === PAGE_HOME)}</p>
      </Section>
    </div>
  ) : (
    <div className="grid place-items-center p-6">
      <Spinner size={22} label={t("Loading the page")} />
    </div>
  );
  const resetControls =
    doc.data?.saved && editingFor == null ? (
      <div className="flex flex-wrap items-center gap-2">
        {confirmReset ? (
          <>
            <span className="text-sm text-fg-muted">
              {t("Replace this page with its default rows?")}
            </span>
            <Button size="sm" variant="destructive" onClick={() => void resetPage()}>
              {t("Reset page")}
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setConfirmReset(false)}>
              {t("Cancel")}
            </Button>
          </>
        ) : (
          <Button size="sm" variant="ghost" onClick={() => setConfirmReset(true)}>
            <RotateCcw size={13} /> {t("Reset to default")}
          </Button>
        )}
      </div>
    ) : (
      <span />
    );
  const openSettings = () => {
    setPending(null);
    setDrawer(null);
    setSettingsTab("settings");
    setSettingsOpen(true);
  };

  // ---- the toolbar ---------------------------------------------------------------------
  const toolbar = (
    <div className="sticky top-0 z-20 border-b border-border bg-bg/95 px-3 py-2 backdrop-blur sm:px-6">
      <div className="flex flex-wrap items-center gap-2">
        <ToolbarPicker
          ariaLabel={t("Page")}
          value={pageKey}
          onChange={(v) => {
            setPending(null);
            setDrawer(null);
            setQuery({ page: v, row: null, add: null });
          }}
          searchable
          className="order-1 min-w-0 max-w-[60vw] justify-start text-left font-semibold sm:max-w-xs"
          trigger={
            <span className="inline-flex min-w-0 items-center gap-1.5">
              {pageKey === PAGE_HOME ? (
                <Sparkles size={13} className="shrink-0 text-primary" />
              ) : (
                <Library size={13} className="shrink-0 text-primary" />
              )}
              <span className="truncate">{dirty ? `${pageName} •` : pageName}</span>
            </span>
          }
          options={
            pageOptions.length
              ? pageOptions
              : [{ value: pageKey, label: pageName, group: "", icon: null, hint: "" }]
          }
        />
        <div className="app-scroll order-3 -mx-3 flex w-[calc(100%+1.5rem)] flex-nowrap items-center gap-2 overflow-x-auto whitespace-nowrap px-3 [&_button]:shrink-0 [&>*]:shrink-0 sm:order-2 sm:mx-0 sm:w-auto sm:flex-wrap sm:overflow-visible sm:px-0">
          <ToolbarPicker
            ariaLabel={t("Editing for")}
            value={editingFor != null ? String(editingFor) : "everyone"}
            onChange={(v) => {
              setDrawer(null);
              setPending(null);
              setQuery({ editing: v === "everyone" ? null : v, as: null });
            }}
            className={cn(editingFor != null && "border-primary text-primary")}
            trigger={
              <span className="inline-flex items-center gap-1.5">
                <span className="text-fg-muted">{t("Editing for")}</span>
                <b>{editingViewer ? viewerName(editingViewer) : t("Everyone")}</b>
              </span>
            }
            options={editingOptions}
          />
          <span className="hidden h-5 w-px bg-border sm:block" aria-hidden />
          {editingFor != null ? (
            <Badge variant="info" className="h-8 gap-1.5 px-3 text-sm">
              <Eye size={13} />{" "}
              {t("Previewing {viewer}", {
                viewer: editingViewer ? viewerLabel(editingViewer) : "",
              })}
            </Badge>
          ) : (
            <ToolbarPicker
              ariaLabel={t("Preview as")}
              value={asParam != null ? String(asParam) : "typical"}
              onChange={(v) => setQuery({ as: v === "typical" ? null : v })}
              trigger={
                <span className="inline-flex items-center gap-1.5">
                  <Eye size={13} className="text-fg-muted" />
                  <span className="text-fg-muted">{t("Preview as")}</span>
                  <b>{previewViewer ? viewerLabel(previewViewer) : t("Typical viewer")}</b>
                </span>
              }
              options={[
                { value: "typical", label: t("Typical viewer") },
                ...(viewers ?? []).map((v) => ({ value: String(v.id), label: viewerLabel(v) })),
              ]}
            />
          )}
          <ToolbarPicker
            ariaLabel={t("Device")}
            value={device}
            onChange={setDevice}
            options={DEVICES.map((d) => ({ value: d, label: deviceLabel(d) }))}
          />
          <label className="inline-flex shrink-0 items-center gap-1.5 text-sm">
            <span className="sr-only">{t("At")}</span>
            <Input
              aria-label={t("Preview at")}
              type="datetime-local"
              className="h-8 w-44 sm:w-48"
              value={toLocalInput(at ?? new Date())}
              onChange={(e) => {
                const v = e.currentTarget.value;
                setAt(v ? new Date(v) : null);
              }}
            />
            {at ? (
              <Button
                variant="ghost"
                size="sm"
                className="h-7 px-2 text-xs"
                onClick={() => setAt(null)}
              >
                {t("Now")}
              </Button>
            ) : null}
          </label>
        </div>
        <span className="order-2 ml-auto flex items-center sm:order-3">
          {previewing ? <Spinner size={12} label={t("Building the page")} /> : null}
        </span>
        <Button
          variant="outline"
          size="sm"
          className="order-2 h-8 gap-1.5 sm:order-3"
          onClick={openSettings}
          aria-haspopup="dialog"
        >
          <Settings2 size={14} />
          <span className="sm:hidden">{t("Settings")}</span>
          <span className="hidden sm:inline">{t("Page settings")}</span>
        </Button>
      </div>
    </div>
  );

  const groups: { rowId?: string; label?: string; items: DisplayRow[] }[] = [];
  for (const d of display) {
    const last = groups.at(-1);
    const pr =
      rowOf(d.row.rowId) ?? (pending && pending.row.id === d.row.rowId ? pending.row : undefined);
    const fan = Boolean(pr?.scope.perLibrary);
    if (fan && last && last.rowId === d.row.rowId) last.items.push(d);
    else
      groups.push({
        rowId: fan ? d.row.rowId : undefined,
        label: fan && pr ? (sourceByKind.get(pr.source.kind)?.title ?? pr.source.kind) : undefined,
        items: [d],
      });
  }
  // The bracket names the row and its libraries in their order.
  for (const g of groups) {
    if (!g.label) continue;
    g.label = t("{source} · one row per library: {libraries}", {
      source: g.label,
      libraries: g.items
        .map((d) => libraryName(d.row.library))
        .filter(Boolean)
        .join(" → "),
    });
  }

  const pageView = (
    <div>
      {editingFor != null || previewViewer ? (
        <p
          className={cn(
            "mb-3 text-sm",
            editingFor != null ? "font-medium text-primary" : "text-fg-muted",
          )}
        >
          {editingFor != null
            ? tn(
                viewerChanges,
                "Editing {viewer}'s page only: {count} change from Everyone. Rows you add here are for {viewer} only.",
                "Editing {viewer}'s page only: {count} changes from Everyone. Rows you add here are for {viewer} only.",
                { viewer: editingViewer ? viewerName(editingViewer) : "" },
              )
            : previewViewer
              ? t(
                  "Editing everyone's {page}, previewing as {viewer}. Your changes apply to everyone.",
                  {
                    page: pageName,
                    viewer: viewerName(previewViewer),
                  },
                )
              : null}
        </p>
      ) : null}
      {conflict ? (
        <div className="mb-3 flex flex-wrap items-center gap-3 rounded-xl border border-warning/40 bg-warning-subtle px-4 py-3 text-sm">
          <AlertTriangle size={16} className="text-warning" />
          <span className="flex-1">
            {t("Someone else saved this page while you were editing. Your draft is still here.")}
          </span>
          <Button size="sm" variant="outline" onClick={() => void loadTheirs()}>
            {t("Load their version")}
          </Button>
        </div>
      ) : null}
      {preview?.access ? (
        <div
          className="mb-4 flex flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border border-primary/30 bg-primary-subtle/40 px-4 py-3 text-sm"
          aria-label={t("Viewer access")}
        >
          <span className="inline-flex items-center gap-1.5 font-semibold text-fg">
            <ShieldCheck size={16} className="text-primary" />
            {preview.access.ageLimit
              ? t("{viewer} · age limit {limit}", {
                  viewer: preview.access.name,
                  limit: preview.access.ageLimit,
                })
              : preview.access.name}
          </span>
          <span className="text-fg-muted">
            {preview.access.unrated === "hidden"
              ? t("Unrated titles hidden")
              : t("Unrated titles shown")}
          </span>
          <span className="text-fg-muted">
            {preview.access.hiddenLibraries?.length
              ? t("Sees {seen} of {total} libraries (not {hidden})", {
                  seen: preview.access.librariesSeen,
                  total: preview.access.librariesTotal,
                  hidden: preview.access.hiddenLibraries.join(", "),
                })
              : t("Sees all {total} libraries", { total: preview.access.librariesTotal })}
          </span>
          <Link
            to={`/viewers/${preview.access.viewer}`}
            className="ml-auto text-sm font-medium text-primary hover:underline"
          >
            {t("Change in Viewers ›")}
          </Link>
        </div>
      ) : null}
      {previewError ? (
        <ErrorPanel error={previewError} />
      ) : !preview ? (
        <div className="grid place-items-center p-10">
          <Spinner size={22} label={t("Building the page")} />
        </div>
      ) : (
        <div>
          <InsertGap
            at={1}
            onAdd={openAdd}
            active={drawer?.kind === "add" && drawer.at === 1 && drawer.picking}
            onDrop={() => dropBefore(0)}
            dropIndex={0}
            dropTarget={touchOver === 0}
          />
          {display.length === 0 ? (
            <EmptyState
              icon={<Sparkles />}
              title={t("Nothing on this page yet")}
              description={t("Use + to add the first row.")}
            />
          ) : null}
          {groups.map((g, gi) => {
            const body = g.items.map((d, j) => {
              const pr =
                rowOf(d.row.rowId) ??
                (pending && pending.row.id === d.row.rowId ? pending.row : undefined);
              const eff = pr && draft ? effectiveRow(draft, pr, editingFor) : pr;
              const source = pr ? sourceByKind.get(pr.source.kind) : sourceByKind.get(d.row.kind);
              const fresh = Boolean(d.row.rowId && !savedIds.has(d.row.rowId));
              return (
                <PageRowView
                  key={d.row.hubId}
                  item={d}
                  pageRow={eff}
                  curated={curated}
                  showRating={Boolean(preview.access)}
                  personal={source?.personal ?? d.row.personal}
                  selected={Boolean(editingRowId && d.row.rowId === editingRowId)}
                  fresh={fresh}
                  justViewer={
                    editingFor != null &&
                    d.row.rowId &&
                    draft &&
                    hasOverride(draft, editingFor, d.row.rowId)
                      ? editingViewer
                        ? viewerName(editingViewer)
                        : ""
                      : null
                  }
                  sourceTitle={source?.title ?? ""}
                  pluginName={source?.plugin}
                  libraryName={d.row.library ? libraryName(d.row.library) : undefined}
                  viewers={viewers ?? []}
                  onEdit={() => openRow(d)}
                  onPin={() => pinRow(d)}
                  onHide={() => hideRow(d.row)}
                  onReset={() =>
                    d.row.rowId &&
                    editingFor != null &&
                    update((p) => clearOverride(p, editingFor, d.row.rowId!))
                  }
                  onDragStart={() => (dragging.current = d.row.hubId)}
                  onDragEnd={() => (dragging.current = null)}
                  onDropBefore={() => dropBefore(d.index)}
                  onTouchDrag={(e) => touchDrag(e, d.row.hubId)}
                  dropTarget={touchOver === d.index}
                  continued={j > 0}
                />
              );
            });
            const last = g.items[g.items.length - 1]!;
            const nextPos = (last.position ?? 0) + 1;
            const next = display[last.index + 1];
            const gapAt = next?.position ?? nextPos;
            return (
              <div key={`${g.rowId ?? g.items[0]!.row.hubId}-${gi}`}>
                {g.label && g.items.length > 1 ? <Block label={g.label}>{body}</Block> : body}
                <InsertGap
                  at={gapAt}
                  onAdd={openAdd}
                  active={drawer?.kind === "add" && drawer.picking && drawer.at === gapAt}
                  onDrop={() => dropBefore(last.index + 1 < display.length ? last.index + 1 : null)}
                  dropIndex={last.index + 1 < display.length ? last.index + 1 : -1}
                  dropTarget={touchOver === (last.index + 1 < display.length ? last.index + 1 : -1)}
                />
              </div>
            );
          })}
          <div className="mt-3 space-y-2">
            {!curated && (preview.picks?.length ?? 0) > 0 ? (
              <Folded
                icon={<Sparkles size={15} className="text-primary" />}
                title={t("More rows you can add · {count}", { count: preview.picks!.length })}
                hint={t(
                  "Rows that fit this page but aren't on it right now. Add any of them, or hide the ones you never want.",
                )}
              >
                {preview.picks!.map((p) => (
                  <div
                    key={p.kind + p.title}
                    className="flex flex-wrap items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface"
                  >
                    {sourceByKind.has(p.kind) ? (
                      <button
                        type="button"
                        className="font-medium text-fg hover:underline"
                        onClick={() =>
                          openUnstored(p.kind, display.length + 1, null, { show: true })
                        }
                      >
                        {p.title}
                      </button>
                    ) : (
                      <span className="font-medium text-fg">{p.title}</span>
                    )}
                    <KindChip personal={p.personal} />
                    <span className="text-xs text-fg-muted">{p.why}</span>
                    <span className="ml-auto flex gap-1">
                      {sourceByKind.has(p.kind) ? (
                        <>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 px-2 text-xs"
                            onClick={() => addPick(p.kind)}
                          >
                            <Plus size={13} /> {t("Add to page")}
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 px-2 text-xs"
                            onClick={() => hideRow({ kind: p.kind } as PreviewRow)}
                            aria-label={t("Hide {row}", { row: p.title })}
                          >
                            <EyeOff size={13} /> {t("Hide")}
                          </Button>
                        </>
                      ) : null}
                    </span>
                  </div>
                ))}
              </Folded>
            ) : null}
            {(preview.notShown?.length ?? 0) > 0 ? (
              <Folded
                icon={<EyeOff size={15} className="text-fg-muted" />}
                title={t("Not shown right now · {count}", { count: preview.notShown!.length })}
                hint={preview
                  .notShown!.slice(0, 3)
                  .map((n) => `${n.title}: ${n.reason}`)
                  .join(" · ")}
              >
                {preview.notShown!.map((n, i) => (
                  <div
                    key={`${n.row ?? n.title}-${i}`}
                    className="flex flex-wrap items-center gap-2 rounded-lg px-2 py-1.5 hover:bg-surface"
                  >
                    {n.row && rowOf(n.row) ? (
                      <button
                        type="button"
                        className="font-medium text-fg hover:underline"
                        onClick={() => {
                          setPending(null);
                          setDrawer({ kind: "edit", rowId: n.row! });
                        }}
                      >
                        {n.title}
                      </button>
                    ) : (
                      <span className="font-medium text-fg">{n.title}</span>
                    )}
                    <span className="text-xs text-fg-muted">{n.reason}</span>
                    <span className="ml-auto flex gap-1">
                      {n.previewAs ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 px-2 text-xs"
                          onClick={() =>
                            editingFor == null && setQuery({ as: String(n.previewAs) })
                          }
                        >
                          {t("Preview as {viewer} ›", {
                            viewer: viewerById.get(n.previewAs)
                              ? viewerName(viewerById.get(n.previewAs)!)
                              : "",
                          })}
                        </Button>
                      ) : null}
                      {n.previewAt ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 px-2 text-xs"
                          onClick={() => setAt(new Date(n.previewAt!))}
                        >
                          {t("Preview at {when} ›", {
                            when: formatPreviewAt(new Date(n.previewAt)),
                          })}
                        </Button>
                      ) : null}
                      {n.row && rowOf(n.row) ? (
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 px-2 text-xs"
                          onClick={() => {
                            setPending(null);
                            setDrawer({ kind: "edit", rowId: n.row! });
                          }}
                          aria-label={t("Edit {row}", { row: n.title })}
                        >
                          <SlidersHorizontal size={13} /> {t("Edit")}
                        </Button>
                      ) : null}
                    </span>
                  </div>
                ))}
              </Folded>
            ) : null}
          </div>
        </div>
      )}
    </div>
  );

  const state = viewersError ? (
    <div className="p-6">
      <ErrorPanel error={viewersError} />
    </div>
  ) : doc.error ? (
    <div className="p-6">
      <ErrorPanel error={doc.error} onRetry={() => void doc.reload()} />
    </div>
  ) : null;

  return (
    <>
      <Workspace
        title={t("Recommendations")}
        subtitle={t("Home and every library's rows, in one place")}
        actions={actions}
      >
        {state ?? (
          <div
            data-workspace-scroll=""
            className="app-scroll min-h-0 flex-1 overflow-y-auto overflow-x-hidden bg-bg"
          >
            {toolbar}
            <LayoutTemplates dirty={dirty} />
            <DiscoveryCardsNotice />
            <main aria-label={t("The page: edit rows right here")} className="p-3 sm:px-6 sm:py-4">
              {draft ? (
                pageView
              ) : (
                <div className="grid place-items-center p-10">
                  <Spinner size={22} label={t("Loading the page")} />
                </div>
              )}
            </main>
          </div>
        )}
      </Workspace>
      <Drawer
        open={settingsOpen}
        onClose={() => {
          setSettingsOpen(false);
          setConfirmReset(false);
        }}
        title={
          editingViewer
            ? t("Page settings · just {viewer}", { viewer: viewerName(editingViewer) })
            : t("Page settings · {page}", { page: pageName })
        }
        description={
          editingFor != null
            ? t("Mode and length here are just for {viewer}; the rest is everyone's.", {
                viewer: editingViewer ? viewerName(editingViewer) : "",
              })
            : t(
                "Changes go into the page draft and show in the page. Save the page when you're done.",
              )
        }
        size="md"
        footer={
          <div className="flex w-full flex-wrap items-center justify-between gap-2">
            {resetControls}
            <Button onClick={() => setSettingsOpen(false)}>{t("Done")}</Button>
          </div>
        }
      >
        <div className="space-y-4">
          <SubtabNav
            ariaLabel={t("Page settings")}
            active={settingsTab}
            onSelect={(k) => setSettingsTab(k as "settings" | "tuning")}
            tabs={[
              { key: "settings", label: t("Settings") },
              { key: "tuning", label: t("Tuning") },
            ]}
          />
          {settingsTab === "tuning" ? tuningBody : settingsBody}
        </div>
      </Drawer>
      <RowDrawer
        state={drawer}
        row={drawerRow}
        source={drawerSource}
        isNew={drawer?.kind === "add" && !pending?.onPage}
        ctx={{
          pageKey,
          curated,
          library: pageLibrary,
          libraries,
          sources: sources.data?.sources ?? null,
          sourcesError: sources.error,
          viewers: viewers ?? [],
          editingFor: editingViewer,
          rows: draft?.rows,
          preview: {
            viewer: previewAs ?? undefined,
            device,
            at: atIso ?? new Date().toISOString(),
          },
        }}
        onClose={closeDrawer}
        onPick={pickSource}
        onChange={updateRow}
        onAdd={commitAdd}
        onRemove={removeOpen}
        onChangeSource={changeSource}
        onTab={(tab) => drawer && setDrawer({ ...drawer, tab })}
      />
    </>
  );
}

/**
 * Which apps show the page. A library page is a tab in Vela; the other apps
 * show the library's first rows tab as their own Recommended page (the same
 * sentence as the library's Tabs settings).
 */
function appsLine(apps: string[] | undefined, home: boolean): string {
  const names: Record<string, string> = {
    plex: "Plex",
    mediaBrowser: t("Jellyfin and Emby"),
    vela: "Vela",
    silo: "Silo",
  };
  const list = (apps ?? []).map((a) => names[a] ?? a);
  if (list.length === 0)
    return home ? t("Home in every app.") : t("This library's rows in every app.");
  if (home)
    return t(
      "{apps}. Each app draws what it can; rows marked Only in Vela are skipped elsewhere.",
      {
        apps: list.join(", "),
      },
    );
  const others = (apps ?? []).filter((a) => a !== "vela").map((a) => names[a] ?? a);
  if (others.length === 0)
    return t("Vela shows these rows as a tab. Other apps don't show this tab.");
  return t(
    "Vela shows these rows as a tab; {apps} show them as the library's Recommended page. Rows marked Only in Vela are skipped elsewhere.",
    { apps: others.join(", ") },
  );
}
