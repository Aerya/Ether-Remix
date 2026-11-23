import {
  Badge,
  Button,
  Input,
  Switch,
  Textarea,
  cn,
  confirmDialog,
  toast,
} from "@braedonsaunders/appkit-ui";
import {
  AlertTriangle,
  CheckCircle2,
  FlaskConical,
  LayoutGrid,
  Maximize,
  Minus,
  Play,
  Plus,
  RotateCw,
  Save,
  Search,
  Sparkles,
  Trash2,
  X,
} from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { Field, NumberInput } from "../../components/fields";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { t } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { errorMessage } from "../../lib/api";
import { flowsApi } from "../../lib/flowsApi";
import { useAsync } from "../../lib/hooks";
import { DescribeFlowDialog } from "./DescribeFlowDialog";
import { NodeInspector } from "./NodeInspector";
import { initialActionParams, pruneUnknownParams, validationFromError } from "./params";
import { categoryTone, iconForNode, newNodeId, useCatalogue } from "./shared";
import { TestRunDrawer } from "./TestRunDrawer";
import type { Flow, FlowEdge, FlowNode, Graph, NodeKind, ValidationResult } from "./types";
import {
  actionGroupLabel,
  autoLayoutGraph,
  buildFieldGroups,
  defaultConfigForNode,
  handleToneColor,
  nodeKindLabel,
  outputHandlesForNode,
  summarizeNode,
  upstreamEventSchemas,
  wouldCreateCycle,
} from "./types";
import "./flows.css";

const NODE_W = 248;

interface DraftSeed {
  name: string;
  description: string;
  graph: Graph;
}

interface View {
  x: number;
  y: number;
  k: number;
}

interface Anchor {
  x: number;
  y: number;
}

const PALETTE_TRIGGERS: NodeKind[] = [
  "trigger.event",
  "trigger.schedule",
  "trigger.webhook",
  "trigger.manual",
];
const PALETTE_LOGIC: NodeKind[] = [
  "condition",
  "logic.switch",
  "logic.delay",
  "logic.wait_event",
  "logic.window",
  "logic.dedupe",
];

export function FlowEditorDrawer({
  flowId,
  onClose,
  onSaved,
  onDeleted,
}: {
  flowId?: string;
  onClose: () => void;
  onSaved?: (flow: Flow, created: boolean) => void;
  onDeleted?: (id: string) => void;
}) {
  const { locale } = useLocale();
  const location = useLocation();
  const catalogueState = useCatalogue();
  const catalogue = catalogueState.data;
  const seed = (location.state as { draft?: DraftSeed } | null)?.draft;

  const [ready, setReady] = useState(false);
  const [savedId, setSavedId] = useState<string | undefined>(flowId);
  const [createdLocalId, setCreatedLocalId] = useState<string | null>(null);
  const fetchId = flowId && flowId !== createdLocalId ? flowId : "";
  const loaded = useAsync(async () => (fetchId ? flowsApi.getFlow(fetchId) : null), [fetchId]);
  const [name, setName] = useState("");
  const loadedMatches = !flowId || loaded.data?.id === flowId || createdLocalId === flowId;
  const [description, setDescription] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [graph, setGraph] = useState<Graph>({ schemaVersion: 1, nodes: [], edges: [] });
  const [limits, setLimits] = useState<Flow["limits"]>({});
  const [webhookUrl, setWebhookUrl] = useState<string | undefined>(undefined);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [selectedNode, setSelectedNode] = useState<string | null>(null);
  const [selectedEdge, setSelectedEdge] = useState<string | null>(null);
  const [view, setView] = useState<View>({ x: 24, y: 24, k: 1 });
  const [validation, setValidation] = useState<ValidationResult | null>(null);
  const [showIssues, setShowIssues] = useState(false);
  const [paletteQuery, setPaletteQuery] = useState("");
  const [testOpen, setTestOpen] = useState(false);
  const [describeOpen, setDescribeOpen] = useState(false);
  const [connecting, setConnecting] = useState<{
    source: string;
    handle: string;
    x: number;
    y: number;
  } | null>(null);
  const [anchors, setAnchors] = useState<{ out: Map<string, Anchor>; inn: Map<string, Anchor> }>({
    out: new Map(),
    inn: new Map(),
  });

  const canvasRef = useRef<HTMLDivElement>(null);
  const closingPromptRef = useRef(false);
  const dragRef = useRef<
    | { mode: "pan"; startX: number; startY: number; viewX: number; viewY: number }
    | { mode: "node"; id: string; startX: number; startY: number; nodeX: number; nodeY: number }
    | { mode: "connect"; source: string; handle: string }
    | null
  >(null);

  const trimmedName = name.trim();
  const isUntitledNew = !savedId && (!trimmedName || trimmedName === t("Untitled flow"));
  const drawerTitle = savedId
    ? loadedMatches && trimmedName
      ? trimmedName
      : t("Edit flow")
    : isUntitledNew
      ? t("New flow")
      : trimmedName;
  const drawerDocTitle = savedId
    ? loadedMatches && trimmedName
      ? trimmedName
      : null
    : isUntitledNew
      ? t("New flow")
      : trimmedName;

  const requestClose = useCallback(async () => {
    if (closingPromptRef.current || document.getElementById("appkit-confirm-title")) return;
    if (!dirty) {
      onClose();
      return;
    }
    closingPromptRef.current = true;
    try {
      const flowLabel = trimmedName && trimmedName !== t("Untitled flow") ? trimmedName : "";
      const ok = await confirmDialog({
        title: t("Discard unsaved changes?"),
        message: flowLabel
          ? t("Your changes to {name} haven't been saved.", { name: flowLabel })
          : t("Your changes to this flow haven't been saved."),
        confirmLabel: t("Discard"),
        tone: "danger",
      });
      if (ok) onClose();
    } finally {
      closingPromptRef.current = false;
    }
  }, [dirty, onClose, trimmedName]);

  useEffect(() => {
    if (!dirty) return;
    const warn = (e: BeforeUnloadEvent) => {
      e.preventDefault();
    };
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [dirty]);

  // Initialise once from the saved flow, a Describe-it draft, or a blank canvas.
  useEffect(() => {
    if (ready || loaded.loading) return;
    if (loaded.error && fetchId) return;
    const flow = loaded.data;
    if (flow) {
      setName(flow.name);
      setDescription(flow.description ?? "");
      setEnabled(flow.enabled);
      setGraph(flow.graph);
      setLimits(flow.limits ?? {});
      setWebhookUrl(flow.webhookUrl);
      setSavedId(flow.id);
    } else if (!flowId) {
      if (seed) {
        setName(seed.name);
        setDescription(seed.description);
        setGraph(autoLayoutGraph(seed.graph));
        setDirty(true);
      } else {
        setName(t("Untitled flow"));
        const trig: FlowNode = {
          id: newNodeId(),
          kind: "trigger.event",
          label: nodeKindLabel("trigger.event"),
          position: { x: 64, y: 160 },
          config: defaultConfigForNode("trigger.event"),
        };
        setGraph({ schemaVersion: 1, nodes: [trig], edges: [] });
      }
    }
    setReady(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded.loading, loaded.data, loaded.error, fetchId, ready]);

  const touch = useCallback((next: Graph) => {
    setGraph(next);
    setDirty(true);
  }, []);

  // Live server-side validation, debounced.
  useEffect(() => {
    if (!ready) return;
    const ctrl = new AbortController();
    const timer = setTimeout(() => {
      flowsApi
        .validateGraph({ name, graph, limits }, ctrl.signal)
        .then(setValidation)
        .catch(() => undefined);
    }, 450);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [graph, name, limits, ready]);

  const issuesByNode = useMemo(() => {
    const map = new Map<string, { errors: string[]; warnings: string[] }>();
    for (const i of validation?.errors ?? [])
      if (i.nodeId)
        map.set(i.nodeId, {
          errors: [...(map.get(i.nodeId)?.errors ?? []), i.message],
          warnings: map.get(i.nodeId)?.warnings ?? [],
        });
    for (const i of validation?.warnings ?? [])
      if (i.nodeId)
        map.set(i.nodeId, {
          errors: map.get(i.nodeId)?.errors ?? [],
          warnings: [...(map.get(i.nodeId)?.warnings ?? []), i.message],
        });
    return map;
  }, [validation]);

  const toGraphCoords = useCallback(
    (clientX: number, clientY: number) => {
      const rect = canvasRef.current?.getBoundingClientRect();
      if (!rect) return { x: 0, y: 0 };
      return {
        x: (clientX - rect.left - view.x) / view.k,
        y: (clientY - rect.top - view.y) / view.k,
      };
    },
    [view],
  );

  // Measure port positions from the DOM so wires land exactly on the dots.
  const measureAnchors = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const rect = canvas.getBoundingClientRect();
    const out = new Map<string, Anchor>();
    const inn = new Map<string, Anchor>();
    canvas.querySelectorAll<HTMLElement>("[data-port-out]").forEach((el) => {
      const r = el.getBoundingClientRect();
      out.set(el.dataset.portOut!, {
        x: (r.left + r.width / 2 - rect.left - view.x) / view.k,
        y: (r.top + r.height / 2 - rect.top - view.y) / view.k,
      });
    });
    canvas.querySelectorAll<HTMLElement>("[data-port-in]").forEach((el) => {
      const r = el.getBoundingClientRect();
      inn.set(el.dataset.portIn!, {
        x: (r.left + r.width / 2 - rect.left - view.x) / view.k,
        y: (r.top + r.height / 2 - rect.top - view.y) / view.k,
      });
    });
    setAnchors((prev) => {
      const same =
        prev.out.size === out.size &&
        prev.inn.size === inn.size &&
        [...out.entries()].every(([k, v]) => {
          const p = prev.out.get(k);
          return p && Math.abs(p.x - v.x) < 0.5 && Math.abs(p.y - v.y) < 0.5;
        });
      return same ? prev : { out, inn };
    });
  }, [view]);

  useLayoutEffect(measureAnchors, [measureAnchors, graph, selectedNode]);

  const addNode = useCallback(
    (kind: NodeKind, actionType?: string, at?: { x: number; y: number }) => {
      const spec = actionType ? catalogue?.actionsByType.get(actionType) : undefined;
      const center = at ?? { x: 320 + Math.random() * 60, y: 140 + Math.random() * 80 };
      const node: FlowNode = {
        id: newNodeId(),
        kind,
        label: spec?.label ?? nodeKindLabel(kind),
        position: { x: Math.round(center.x), y: Math.round(center.y) },
        config:
          kind === "action"
            ? {
                actionType: actionType || "notify.send",
                params: initialActionParams(
                  catalogue?.actionsByType.get(actionType || "notify.send"),
                  actionType || "notify.send",
                ),
              }
            : defaultConfigForNode(kind, actionType),
      };
      touch({ ...graph, nodes: [...graph.nodes, node] });
      setSelectedNode(node.id);
      setSelectedEdge(null);
    },
    [catalogue, graph, touch],
  );

  const deleteSelected = useCallback(() => {
    if (selectedEdge) {
      touch({ ...graph, edges: graph.edges.filter((e) => e.id !== selectedEdge) });
      setSelectedEdge(null);
    } else if (selectedNode) {
      touch({
        ...graph,
        nodes: graph.nodes.filter((n) => n.id !== selectedNode),
        edges: graph.edges.filter((e) => e.source !== selectedNode && e.target !== selectedNode),
      });
      setSelectedNode(null);
    }
  }, [graph, selectedEdge, selectedNode, touch]);

  // Global pointer handling for pan / node-drag / wire-drag.
  useEffect(() => {
    const onMove = (e: PointerEvent) => {
      const drag = dragRef.current;
      if (!drag) return;
      if (drag.mode === "pan") {
        setView((v) => ({
          ...v,
          x: drag.viewX + e.clientX - drag.startX,
          y: drag.viewY + e.clientY - drag.startY,
        }));
      } else if (drag.mode === "node") {
        const dx = (e.clientX - drag.startX) / view.k;
        const dy = (e.clientY - drag.startY) / view.k;
        setGraph((g) => ({
          ...g,
          nodes: g.nodes.map((n) =>
            n.id === drag.id
              ? {
                  ...n,
                  position: {
                    x: Math.round((drag.nodeX + dx) / 4) * 4,
                    y: Math.round((drag.nodeY + dy) / 4) * 4,
                  },
                }
              : n,
          ),
        }));
        setDirty(true);
      } else if (drag.mode === "connect") {
        const p = toGraphCoords(e.clientX, e.clientY);
        setConnecting({ source: drag.source, handle: drag.handle, x: p.x, y: p.y });
      }
    };
    const onUp = (e: PointerEvent) => {
      const drag = dragRef.current;
      dragRef.current = null;
      if (drag?.mode === "connect") {
        setConnecting(null);
        const el = document
          .elementFromPoint(e.clientX, e.clientY)
          ?.closest<HTMLElement>("[data-node-id]");
        const targetId = el?.dataset.nodeId;
        if (targetId && targetId !== drag.source) {
          const target = graph.nodes.find((n) => n.id === targetId);
          if (target?.kind.startsWith("trigger.")) {
            toast.error(t("Triggers start a flow — nothing connects into one."));
          } else if (wouldCreateCycle(graph, drag.source, targetId)) {
            toast.error(t("That connection would loop the flow back on itself."));
          } else if (
            graph.edges.some(
              (edge) =>
                edge.source === drag.source &&
                edge.target === targetId &&
                (edge.sourceHandle ?? "next") === drag.handle,
            )
          ) {
            // Already wired.
          } else {
            const edge: FlowEdge = {
              id: newNodeId("e"),
              source: drag.source,
              target: targetId,
              sourceHandle: drag.handle,
            };
            touch({ ...graph, edges: [...graph.edges, edge] });
          }
        }
      }
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    return () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
    };
  }, [graph, toGraphCoords, touch, view.k]);

  const fitView = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas || graph.nodes.length === 0) return;
    const rect = canvas.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return;
    const xs = graph.nodes.map((n) => n.position.x);
    const ys = graph.nodes.map((n) => n.position.y);
    const minX = Math.min(...xs);
    const minY = Math.min(...ys);
    const maxX = Math.max(...xs) + NODE_W;
    const maxY = Math.max(...ys) + 160;
    const k = Math.max(
      0.35,
      Math.min(1.15, Math.min(rect.width / (maxX - minX + 80), rect.height / (maxY - minY + 80))),
    );
    setView({ k, x: 40 - minX * k, y: 40 - minY * k });
  }, [graph.nodes]);

  const canRenderCanvas = ready && Boolean(catalogue) && !(loaded.error && flowId);

  useEffect(() => {
    if (canRenderCanvas && graph.nodes.length > 0) {
      const id = requestAnimationFrame(fitView);
      return () => cancelAnimationFrame(id);
    }
    // Only on first ready canvas render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canRenderCanvas]);

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canRenderCanvas || !canvas || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => measureAnchors());
    ro.observe(canvas);
    return () => ro.disconnect();
  }, [canRenderCanvas, measureAnchors]);

  const showSaveProblems = useCallback((result: ValidationResult) => {
    setValidation(result);
    setSelectedNode(null);
    setSelectedEdge(null);
    setShowIssues(true);
    toast.error(t("Fix the highlighted problems before saving."));
  }, []);

  const save = useCallback(async () => {
    if (saving) return;
    setSaving(true);
    try {
      // Hidden params an action does not accept can't be fixed in the
      // inspector; drop them rather than fail the save on them.
      const clean = catalogue ? pruneUnknownParams(graph, catalogue.actionsByType) : graph;
      if (clean !== graph) setGraph(clean);
      const payload = {
        name: name.trim() || t("Untitled flow"),
        description,
        enabled,
        graph: clean,
        limits,
      };
      // Check what is about to be saved, not the last debounced result.
      const fresh = await flowsApi
        .validateGraph({ name: payload.name, graph: clean, limits })
        .catch(() => null);
      if (fresh && !fresh.valid) {
        showSaveProblems(fresh);
        return;
      }
      if (fresh) setValidation(fresh);
      const wasNew = !savedId;
      const saved = savedId
        ? await flowsApi.updateFlow(savedId, payload)
        : await flowsApi.createFlow(payload);
      if (wasNew) setCreatedLocalId(saved.id);
      setSavedId(saved.id);
      if (saved.webhookUrl) setWebhookUrl(saved.webhookUrl);
      setDirty(false);
      toast.success(t("Flow saved"));
      onSaved?.(saved, wasNew);
    } catch (e) {
      const problems = validationFromError(e);
      if (problems) showSaveProblems(problems);
      else toast.error(errorMessage(e));
    } finally {
      setSaving(false);
    }
  }, [
    catalogue,
    description,
    enabled,
    graph,
    limits,
    name,
    onSaved,
    savedId,
    saving,
    showSaveProblems,
  ]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (
        testOpen ||
        describeOpen ||
        document.querySelector('[data-drawer-layer="nested"]') ||
        document.querySelector("[data-ui-overlay]") ||
        document.getElementById("appkit-confirm-title")
      ) {
        return;
      }
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        if (!saving && (dirty || !savedId)) void save();
        return;
      }
      const target = e.target;
      if (target instanceof Element && target.closest("input, textarea, select, [contenteditable]"))
        return;
      if (e.key === "Delete" || e.key === "Backspace") {
        e.preventDefault();
        deleteSelected();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [deleteSelected, describeOpen, dirty, save, savedId, saving, testOpen]);

  const runNow = async () => {
    if (!savedId) return;
    try {
      await flowsApi.runManual(savedId);
      toast.success(t("Flow started — watch it under Runs."));
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  const removeFlow = async () => {
    if (!savedId) return;
    const ok = await confirmDialog({
      title: t("Delete this flow?"),
      message: t("“{name}” and its run history will be removed. Active runs are cancelled.", {
        name,
      }),
      confirmLabel: t("Delete"),
      tone: "danger",
    });
    if (!ok) return;
    try {
      await flowsApi.deleteFlow(savedId);
      toast.success(t("Flow deleted"));
      if (onDeleted) onDeleted(savedId);
      else onClose();
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  const rotateWebhook = async () => {
    if (!savedId) return;
    try {
      const res = await flowsApi.rotateWebhookToken(savedId);
      setWebhookUrl(res.webhookUrl);
      toast.success(t("New webhook URL issued — the old one stops working."), {
        description: res.webhookUrl,
      });
    } catch (e) {
      toast.error(errorMessage(e));
    }
  };

  const selected = graph.nodes.find((n) => n.id === selectedNode) ?? null;
  const fieldGroups = useMemo(() => {
    if (!catalogue || !selected) return [];
    return buildFieldGroups(graph, selected.id, catalogue.events, catalogue.actions, {
      filterableOnly: false,
    });
    // `locale` re-runs the memo so picker group labels follow a language switch.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalogue, graph, selected, locale]);
  const sample = useMemo(() => {
    if (!catalogue || !selected) return undefined;
    return upstreamEventSchemas(graph, selected.id, catalogue.events)[0]?.sample;
  }, [catalogue, graph, selected]);

  const errorCount = validation?.errors?.length ?? 0;
  const warningCount = validation?.warnings?.length ?? 0;
  const hasManual = graph.nodes.some((n) => n.kind === "trigger.manual");
  const hasWebhook = graph.nodes.some((n) => n.kind === "trigger.webhook");

  const paletteActions = (() => {
    if (!catalogue) return [];
    const q = paletteQuery.trim().toLowerCase();
    const groups = new Map<string, typeof catalogue.actions>();
    for (const a of catalogue.actions) {
      if (q && !a.label.toLowerCase().includes(q) && !a.type.includes(q)) continue;
      const list = groups.get(a.group) ?? [];
      list.push(a);
      groups.set(a.group, list);
    }
    return [...groups.entries()];
  })();
  const matchesQuery = (label: string) =>
    !paletteQuery.trim() || label.toLowerCase().includes(paletteQuery.trim().toLowerCase());

  const edgePath = (x1: number, y1: number, x2: number, y2: number) => {
    const dx = Math.max(48, Math.abs(x2 - x1) / 2);
    return `M ${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`;
  };

  return (
    <Drawer
      open
      onClose={() => void requestClose()}
      title={drawerTitle}
      documentTitle={drawerDocTitle}
      size="2xl"
      panelClassName="flow-editor-drawer"
      headerActions={dirty ? <Badge variant="warning">{t("Unsaved changes")}</Badge> : undefined}
      bodyClassName="flex min-h-0 flex-1 flex-col overflow-hidden p-0"
    >
      {loaded.error && flowId ? (
        <div className="p-6">
          <ErrorPanel error={loaded.error} onRetry={() => void loaded.reload()} />
        </div>
      ) : !ready || !catalogue ? (
        <div className="p-6">
          <LoadingRows rows={5} />
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
          {/* Toolbar */}
          <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-surface px-4 py-2.5 sm:px-6">
            <Input
              aria-label={t("Flow name")}
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                setDirty(true);
              }}
              className="h-9 w-64 max-w-[50vw] border-transparent bg-transparent text-base font-semibold shadow-none hover:border-border focus:border-border"
            />
            <label className="flex items-center gap-2 text-sm text-fg-muted">
              <Switch
                aria-label={t("Flow enabled")}
                checked={enabled}
                onChange={(e) => {
                  setEnabled(e.target.checked);
                  setDirty(true);
                }}
              />
              {enabled ? t("On") : t("Off")}
            </label>
            <button
              type="button"
              onClick={() => setShowIssues((s) => !s)}
              aria-expanded={showIssues}
              className={cn(
                "ml-1 inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-colors",
                errorCount > 0
                  ? "border-danger/40 bg-danger-subtle text-danger"
                  : warningCount > 0
                    ? "border-warning/40 bg-warning-subtle text-warning"
                    : "border-success/40 bg-success-subtle text-success",
              )}
            >
              {errorCount > 0 ? <AlertTriangle size={13} /> : <CheckCircle2 size={13} />}
              {validation === null
                ? t("Checking…")
                : errorCount > 0
                  ? t("{count} to fix", { count: errorCount })
                  : warningCount > 0
                    ? t("Ready · {count} heads-up", { count: warningCount })
                    : t("Ready to run")}
            </button>
            <div className="ml-auto flex flex-wrap items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => setDescribeOpen(true)}>
                <Sparkles size={14} /> {t("Describe it")}
              </Button>
              {hasManual && savedId ? (
                <Button variant="outline" size="sm" onClick={() => void runNow()}>
                  <Play size={14} /> {t("Run now")}
                </Button>
              ) : null}
              <Button variant="outline" size="sm" onClick={() => setTestOpen(true)}>
                <FlaskConical size={14} /> {t("Test run")}
              </Button>
              <Button
                size="sm"
                disabled={saving || (!dirty && Boolean(savedId))}
                onClick={() => void save()}
              >
                <Save size={14} /> {saving ? t("Saving…") : savedId ? t("Save") : t("Save flow")}
              </Button>
            </div>
          </div>

          <div className="relative flex min-h-0 flex-1 overflow-hidden bg-surface">
            {/* Palette */}
            <aside
              className="hidden w-60 shrink-0 flex-col border-r border-border md:flex"
              aria-label={t("Step palette")}
            >
              <label className="flex items-center gap-2 border-b border-border px-3 py-2.5">
                <Search size={14} className="shrink-0 text-fg-subtle" />
                <input
                  value={paletteQuery}
                  onChange={(e) => setPaletteQuery(e.target.value)}
                  placeholder={t("Search steps…")}
                  aria-label={t("Search steps")}
                  className="min-w-0 flex-1 bg-transparent text-sm text-fg outline-none placeholder:text-fg-subtle"
                />
              </label>
              <div className="min-h-0 flex-1 overflow-y-auto p-2">
                <PaletteGroup title={t("Start with")}>
                  {PALETTE_TRIGGERS.filter((k) => matchesQuery(nodeKindLabel(k))).map((kind) => (
                    <PaletteItem
                      key={kind}
                      kind={kind}
                      label={nodeKindLabel(kind)}
                      onAdd={() => addNode(kind)}
                    />
                  ))}
                </PaletteGroup>
                <PaletteGroup title={t("Check & steer")}>
                  {PALETTE_LOGIC.filter((k) => matchesQuery(nodeKindLabel(k))).map((kind) => (
                    <PaletteItem
                      key={kind}
                      kind={kind}
                      label={nodeKindLabel(kind)}
                      onAdd={() => addNode(kind)}
                    />
                  ))}
                </PaletteGroup>
                {paletteActions.map(([group, actions]) => (
                  <PaletteGroup key={group} title={actionGroupLabel(group)}>
                    {actions.map((a) => (
                      <PaletteItem
                        key={a.type}
                        kind="action"
                        actionType={a.type}
                        label={a.label}
                        hint={a.description}
                        onAdd={() => addNode("action", a.type)}
                      />
                    ))}
                  </PaletteGroup>
                ))}
              </div>
              <p className="border-t border-border px-3 py-2 text-[11px] leading-snug text-fg-subtle">
                {t(
                  "Click or drag a step onto the canvas. Drag from a step's right-hand dot to connect it.",
                )}
              </p>
            </aside>

            {/* Canvas */}
            <div
              ref={canvasRef}
              data-flow-canvas
              className="flow-canvas relative min-w-0 flex-1 cursor-grab touch-none overflow-hidden active:cursor-grabbing"
              onPointerDown={(e) => {
                if (
                  e.target === e.currentTarget ||
                  (e.target as HTMLElement).dataset.canvasBg === "1"
                ) {
                  dragRef.current = {
                    mode: "pan",
                    startX: e.clientX,
                    startY: e.clientY,
                    viewX: view.x,
                    viewY: view.y,
                  };
                  setSelectedNode(null);
                  setSelectedEdge(null);
                }
              }}
              onWheel={(e) => {
                if (e.ctrlKey || e.metaKey) {
                  e.preventDefault();
                  const rect = canvasRef.current?.getBoundingClientRect();
                  if (!rect) return;
                  const factor = e.deltaY < 0 ? 1.08 : 1 / 1.08;
                  setView((v) => {
                    const k = Math.max(0.3, Math.min(1.6, v.k * factor));
                    const mx = e.clientX - rect.left;
                    const my = e.clientY - rect.top;
                    return { k, x: mx - ((mx - v.x) / v.k) * k, y: my - ((my - v.y) / v.k) * k };
                  });
                }
              }}
              onDragOver={(e) => e.preventDefault()}
              onDrop={(e) => {
                e.preventDefault();
                try {
                  const data = JSON.parse(
                    e.dataTransfer.getData("application/x-flow-node") || "{}",
                  ) as { kind?: NodeKind; actionType?: string };
                  if (data.kind)
                    addNode(data.kind, data.actionType, toGraphCoords(e.clientX, e.clientY));
                } catch {
                  // Not one of ours.
                }
              }}
            >
              <div data-canvas-bg="1" className="absolute inset-0" />
              <div
                className="absolute left-0 top-0 origin-top-left"
                style={{ transform: `translate(${view.x}px, ${view.y}px) scale(${view.k})` }}
              >
                <svg
                  className="pointer-events-none absolute left-0 top-0 overflow-visible"
                  width="1"
                  height="1"
                  aria-hidden
                >
                  {graph.edges.map((edge) => {
                    const from = anchors.out.get(`${edge.source}:${edge.sourceHandle ?? "next"}`);
                    const to = anchors.inn.get(edge.target);
                    const src = graph.nodes.find((n) => n.id === edge.source);
                    const tgt = graph.nodes.find((n) => n.id === edge.target);
                    if (!src || !tgt) return null;
                    const x1 = from?.x ?? src.position.x + NODE_W;
                    const y1 = from?.y ?? src.position.y + 40;
                    const x2 = to?.x ?? tgt.position.x;
                    const y2 = to?.y ?? tgt.position.y + 40;
                    const handle = outputHandlesForNode(src).find(
                      (h) => h.id === (edge.sourceHandle ?? "next"),
                    );
                    const color = handle ? handleToneColor(handle.tone) : "var(--color-fg-subtle)";
                    const isSel = selectedEdge === edge.id;
                    return (
                      <g key={edge.id}>
                        <path
                          d={edgePath(x1, y1, x2, y2)}
                          fill="none"
                          stroke="transparent"
                          strokeWidth={14 / view.k}
                          className="pointer-events-auto cursor-pointer"
                          onClick={(ev) => {
                            ev.stopPropagation();
                            setSelectedEdge(edge.id);
                            setSelectedNode(null);
                          }}
                        />
                        <path
                          d={edgePath(x1, y1, x2, y2)}
                          fill="none"
                          stroke={color}
                          strokeWidth={(isSel ? 3 : 2) / view.k + 0.6}
                          strokeOpacity={isSel ? 1 : 0.75}
                          markerEnd={`url(#flow-arrow)`}
                          className="flow-wire"
                        />
                      </g>
                    );
                  })}
                  {connecting
                    ? (() => {
                        const from = anchors.out.get(`${connecting.source}:${connecting.handle}`);
                        const src = graph.nodes.find((n) => n.id === connecting.source);
                        if (!src) return null;
                        return (
                          <path
                            d={edgePath(
                              from?.x ?? src.position.x + NODE_W,
                              from?.y ?? src.position.y + 40,
                              connecting.x,
                              connecting.y,
                            )}
                            fill="none"
                            stroke="var(--color-primary)"
                            strokeWidth={2}
                            strokeDasharray="6 5"
                            className="flow-wire-live"
                          />
                        );
                      })()
                    : null}
                  <defs>
                    <marker
                      id="flow-arrow"
                      viewBox="0 0 10 10"
                      refX="9"
                      refY="5"
                      markerWidth="6"
                      markerHeight="6"
                      orient="auto-start-reverse"
                    >
                      <path d="M 0 1 L 10 5 L 0 9 z" fill="context-stroke" opacity="0.8" />
                    </marker>
                  </defs>
                </svg>

                {graph.nodes.map((node) => {
                  const Icon = iconForNode(
                    node.kind,
                    String(node.config?.actionType ?? "") || undefined,
                  );
                  const tone = categoryTone(node.kind);
                  const issues = issuesByNode.get(node.id);
                  const handles = outputHandlesForNode(node);
                  const isSel = selectedNode === node.id;
                  return (
                    <article
                      key={node.id}
                      data-node-id={node.id}
                      aria-label={node.label || nodeKindLabel(node.kind)}
                      className={cn(
                        "flow-node absolute rounded-xl border bg-surface shadow-sm transition-shadow",
                        isSel
                          ? "border-primary shadow-md ring-2 ring-primary/30"
                          : issues?.errors.length
                            ? "border-danger/60"
                            : "border-border hover:shadow",
                        node.disabled && "opacity-55",
                      )}
                      style={{ left: node.position.x, top: node.position.y, width: NODE_W }}
                      onPointerDown={(e) => {
                        if ((e.target as HTMLElement).closest("[data-port-out],[data-no-drag]"))
                          return;
                        e.stopPropagation();
                        setSelectedNode(node.id);
                        setSelectedEdge(null);
                        dragRef.current = {
                          mode: "node",
                          id: node.id,
                          startX: e.clientX,
                          startY: e.clientY,
                          nodeX: node.position.x,
                          nodeY: node.position.y,
                        };
                      }}
                    >
                      <span
                        data-port-in={node.id}
                        aria-hidden
                        className="absolute -left-[7px] top-9 size-3.5 rounded-full border-2 border-surface bg-fg-subtle shadow"
                      />
                      <header className="flex cursor-grab items-center gap-2 px-3 pb-1 pt-2.5 active:cursor-grabbing">
                        <span
                          className={cn(
                            "grid size-8 shrink-0 place-items-center rounded-lg",
                            tone.chip,
                          )}
                        >
                          <Icon size={15} />
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-[13px] font-semibold text-fg">
                            {node.label || nodeKindLabel(node.kind)}
                          </span>
                          <span className="block text-[11px] text-fg-subtle">
                            {nodeKindLabel(node.kind)}
                            {node.disabled ? ` · ${t("skipped")}` : ""}
                          </span>
                        </span>
                        {issues?.errors.length ? (
                          <span
                            className="grid size-5 shrink-0 place-items-center rounded-full bg-danger-subtle text-danger"
                            title={issues.errors.join("\n")}
                          >
                            <AlertTriangle size={12} />
                          </span>
                        ) : issues?.warnings.length ? (
                          <span
                            className="size-2 shrink-0 rounded-full bg-warning"
                            title={issues.warnings.join("\n")}
                          />
                        ) : null}
                      </header>
                      <p
                        className="truncate px-3 pb-2 text-xs text-fg-muted"
                        title={summarizeNode(node, catalogue.eventsByType, catalogue.actionsByType)}
                      >
                        {summarizeNode(node, catalogue.eventsByType, catalogue.actionsByType)}
                      </p>
                      <footer className="space-y-0.5 border-t border-border-subtle px-3 py-1.5">
                        {handles.map((h) => (
                          <div
                            key={h.id}
                            className="relative flex items-center justify-end gap-1.5 py-0.5 text-[11px] font-medium text-fg-muted"
                          >
                            {h.label}
                            <button
                              type="button"
                              data-port-out={`${node.id}:${h.id}`}
                              data-no-drag
                              aria-label={t("Connect from {label}", { label: h.label })}
                              className="flow-port -mr-[19px] size-3.5 shrink-0 cursor-crosshair rounded-full border-2 border-surface shadow transition-transform hover:scale-125"
                              style={{ backgroundColor: handleToneColor(h.tone) }}
                              onPointerDown={(e) => {
                                e.stopPropagation();
                                e.preventDefault();
                                dragRef.current = {
                                  mode: "connect",
                                  source: node.id,
                                  handle: h.id,
                                };
                                const p = toGraphCoords(e.clientX, e.clientY);
                                setConnecting({ source: node.id, handle: h.id, x: p.x, y: p.y });
                              }}
                            />
                          </div>
                        ))}
                      </footer>
                    </article>
                  );
                })}
              </div>

              {graph.nodes.length === 0 ? (
                <div className="absolute inset-0 grid place-items-center p-6">
                  <div className="max-w-sm text-center">
                    <p className="text-base font-semibold text-fg">
                      {t("Start with what should happen")}
                    </p>
                    <p className="mt-1 text-sm text-fg-muted">
                      {t(
                        "Describe it in your own words, start from a template, or add a trigger from the palette.",
                      )}
                    </p>
                    <div className="mt-4 flex flex-wrap justify-center gap-2">
                      <Button size="sm" onClick={() => setDescribeOpen(true)}>
                        <Sparkles size={14} /> {t("Describe it")}
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => addNode("trigger.event", undefined, { x: 120, y: 160 })}
                      >
                        <Plus size={14} /> {t("Add an event trigger")}
                      </Button>
                    </div>
                  </div>
                </div>
              ) : null}

              {/* Canvas controls */}
              <div className="absolute bottom-3 left-3 flex items-center gap-1 rounded-lg border border-border bg-surface/95 p-1 shadow-sm backdrop-blur">
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("Zoom out")}
                  onClick={() => setView((v) => ({ ...v, k: Math.max(0.3, v.k / 1.15) }))}
                >
                  <Minus size={14} />
                </Button>
                <span className="w-11 text-center text-xs tabular-nums text-fg-muted">
                  {Math.round(view.k * 100)}%
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("Zoom in")}
                  onClick={() => setView((v) => ({ ...v, k: Math.min(1.6, v.k * 1.15) }))}
                >
                  <Plus size={14} />
                </Button>
                <Button variant="ghost" size="sm" aria-label={t("Fit to view")} onClick={fitView}>
                  <Maximize size={14} />
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  aria-label={t("Tidy up")}
                  onClick={() => {
                    touch(autoLayoutGraph(graph));
                    requestAnimationFrame(fitView);
                  }}
                >
                  <LayoutGrid size={14} /> <span className="hidden sm:inline">{t("Tidy up")}</span>
                </Button>
                {selectedNode || selectedEdge ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={t("Delete")}
                    onClick={deleteSelected}
                    className="text-danger"
                  >
                    <Trash2 size={14} /> <span className="hidden sm:inline">{t("Delete")}</span>
                  </Button>
                ) : null}
              </div>

              {/* Mobile add-step (palette is hidden on small screens) */}
              <div className="absolute bottom-3 right-3 md:hidden">
                <Button size="sm" onClick={() => addNode("action", "notify.send")}>
                  <Plus size={14} /> {t("Add step")}
                </Button>
              </div>
            </div>

            {/* Inspector */}
            <aside
              className={cn(
                "w-[22rem] max-w-[88vw] shrink-0 flex-col border-l border-border md:flex",
                selected
                  ? "flex max-md:absolute max-md:inset-y-0 max-md:right-0 max-md:z-20 max-md:bg-surface max-md:shadow-xl"
                  : "max-md:hidden",
              )}
              aria-label={t("Step settings")}
            >
              <div className="flex items-center justify-between gap-2 border-b border-border px-4 py-3">
                <h2 className="truncate text-sm font-semibold text-fg">
                  {selected
                    ? selected.label || nodeKindLabel(selected.kind)
                    : showIssues
                      ? t("Checks")
                      : t("Flow settings")}
                </h2>
                {selected ? (
                  <Button
                    variant="ghost"
                    size="sm"
                    aria-label={t("Back to flow settings")}
                    onClick={() => setSelectedNode(null)}
                  >
                    <X size={15} />
                  </Button>
                ) : null}
              </div>
              <div className="min-h-0 flex-1 overflow-y-auto px-4 py-4">
                {showIssues && !selected ? (
                  <IssuesPanel
                    validation={validation}
                    onPick={(nodeId) => {
                      setSelectedNode(nodeId);
                      setShowIssues(false);
                    }}
                  />
                ) : selected ? (
                  <NodeInspector
                    key={selected.id}
                    node={selected}
                    catalogue={catalogue}
                    fieldGroups={fieldGroups}
                    sample={sample}
                    onChange={(next) =>
                      touch({
                        ...graph,
                        nodes: graph.nodes.map((n) => (n.id === next.id ? next : n)),
                      })
                    }
                  />
                ) : (
                  <div className="space-y-4">
                    <Field label={t("What this flow does")}>
                      <Textarea
                        aria-label={t("Flow description")}
                        value={description}
                        rows={3}
                        placeholder={t("A sentence future-you will thank you for.")}
                        onChange={(e) => {
                          setDescription(e.target.value);
                          setDirty(true);
                        }}
                      />
                    </Field>
                    <div className="space-y-3 border-t border-border pt-3">
                      <p className="text-sm font-semibold text-fg">{t("Guardrails")}</p>
                      <Field
                        label={t("At most this many runs a minute")}
                        hint={t("Extra triggers are recorded as throttled, never queued forever.")}
                      >
                        <NumberInput
                          aria-label={t("Runs per minute")}
                          value={limits?.rateLimitPerMin ?? 60}
                          min={1}
                          max={600}
                          step={1}
                          onChange={(n) => {
                            setLimits({ ...limits, rateLimitPerMin: n });
                            setDirty(true);
                          }}
                          className="w-28"
                        />
                      </Field>
                      <Field label={t("Stop a run after this many steps")}>
                        <NumberInput
                          aria-label={t("Max steps")}
                          value={limits?.maxSteps ?? 100}
                          min={1}
                          max={250}
                          step={1}
                          onChange={(n) => {
                            setLimits({ ...limits, maxSteps: n });
                            setDirty(true);
                          }}
                          className="w-28"
                        />
                      </Field>
                      <Field label={t("Turn the flow off after this many failures in a row")}>
                        <NumberInput
                          aria-label={t("Auto-disable after")}
                          value={limits?.autoDisableAfter ?? 10}
                          min={1}
                          step={1}
                          onChange={(n) => {
                            setLimits({ ...limits, autoDisableAfter: n });
                            setDirty(true);
                          }}
                          className="w-28"
                        />
                      </Field>
                      <label className="flex items-center justify-between gap-3">
                        <span className="text-sm text-fg">
                          {t("Run events for the same session in order")}
                        </span>
                        <Switch
                          aria-label={t("Ordered by correlation key")}
                          checked={Boolean(limits?.orderedByKey)}
                          onChange={(e) => {
                            setLimits({ ...limits, orderedByKey: e.target.checked });
                            setDirty(true);
                          }}
                        />
                      </label>
                    </div>
                    {hasWebhook ? (
                      <div className="space-y-2 border-t border-border pt-3">
                        <p className="text-sm font-semibold text-fg">{t("Incoming webhook")}</p>
                        {savedId ? (
                          <>
                            <p className="break-all rounded-md bg-bg-subtle px-2.5 py-2 font-mono text-xs text-fg">
                              {webhookUrl ?? `/ether/api/flows/hooks/${savedId}`}
                            </p>
                            <Button
                              variant="outline"
                              size="sm"
                              onClick={() => void rotateWebhook()}
                            >
                              <RotateCw size={13} /> {t("Rotate URL")}
                            </Button>
                          </>
                        ) : (
                          <p className="text-sm text-fg-muted">
                            {t("Save the flow to get its private webhook URL.")}
                          </p>
                        )}
                      </div>
                    ) : null}
                    {validation?.errors?.length || validation?.warnings?.length ? (
                      <div className="border-t border-border pt-3">
                        <IssuesPanel
                          validation={validation}
                          onPick={(nodeId) => setSelectedNode(nodeId)}
                        />
                      </div>
                    ) : null}
                    {savedId ? (
                      <div className="border-t border-border pt-3">
                        <Button
                          variant="ghost"
                          size="sm"
                          className="text-danger"
                          onClick={() => void removeFlow()}
                        >
                          <Trash2 size={14} /> {t("Delete this flow")}
                        </Button>
                      </div>
                    ) : null}
                  </div>
                )}
              </div>
            </aside>
          </div>

          <TestRunDrawer
            open={testOpen}
            onClose={() => setTestOpen(false)}
            flowId={savedId}
            graph={graph}
            catalogue={catalogue}
          />
          <DescribeFlowDialog
            open={describeOpen}
            onClose={() => setDescribeOpen(false)}
            onDraft={(draft) => {
              touch(autoLayoutGraph(draft.graph));
              if (!name || name === t("Untitled flow")) setName(draft.name);
              if (!description) setDescription(draft.description);
              setSelectedNode(null);
              requestAnimationFrame(fitView);
              toast.success(t("Draft on the canvas — review, test, then save."));
            }}
          />
        </div>
      )}
    </Drawer>
  );
}

function PaletteGroup({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mb-3">
      <p className="px-1.5 pb-1 text-[11px] font-semibold uppercase tracking-wide text-fg-subtle">
        {title}
      </p>
      <div className="space-y-0.5">{children}</div>
    </div>
  );
}

function PaletteItem({
  kind,
  actionType,
  label,
  hint,
  onAdd,
}: {
  kind: NodeKind;
  actionType?: string;
  label: string;
  hint?: string;
  onAdd: () => void;
}) {
  const Icon = iconForNode(kind, actionType);
  const tone = categoryTone(kind);
  return (
    <button
      type="button"
      draggable
      title={hint ?? label}
      onDragStart={(e) =>
        e.dataTransfer.setData("application/x-flow-node", JSON.stringify({ kind, actionType }))
      }
      onClick={onAdd}
      className="flex w-full items-center gap-2 rounded-md px-1.5 py-1.5 text-left transition-colors hover:bg-surface-hover"
    >
      <span className={cn("grid size-7 shrink-0 place-items-center rounded-md", tone.chip)}>
        <Icon size={14} />
      </span>
      <span className="min-w-0 truncate text-sm text-fg">{label}</span>
      <Plus size={13} className="ml-auto shrink-0 text-fg-subtle" />
    </button>
  );
}

function IssuesPanel({
  validation,
  onPick,
}: {
  validation: ValidationResult | null;
  onPick: (nodeId: string) => void;
}) {
  if (!validation) return <p className="text-sm text-fg-muted">{t("Checking the flow…")}</p>;
  const all = [
    ...(validation.errors ?? []).map((i) => ({ ...i, tone: "error" as const })),
    ...(validation.warnings ?? []).map((i) => ({ ...i, tone: "warning" as const })),
  ];
  if (all.length === 0)
    return (
      <p className="flex items-center gap-2 rounded-lg bg-success-subtle px-3 py-2.5 text-sm text-success">
        <CheckCircle2 size={15} /> {t("Everything checks out. This flow is ready to run.")}
      </p>
    );
  return (
    <ul className="space-y-1.5">
      {all.map((issue, idx) => (
        <li key={idx}>
          <button
            type="button"
            disabled={!issue.nodeId}
            onClick={() => issue.nodeId && onPick(issue.nodeId)}
            className={cn(
              "flex w-full items-start gap-2 rounded-lg px-3 py-2 text-left text-sm",
              issue.tone === "error"
                ? "bg-danger-subtle/70 text-danger"
                : "bg-warning-subtle/70 text-warning",
              issue.nodeId && "hover:brightness-105",
            )}
          >
            <AlertTriangle size={14} className="mt-0.5 shrink-0" />
            <span>{issue.message}</span>
          </button>
        </li>
      ))}
    </ul>
  );
}
