/**
 * Console HTTP client for Ether Flows (/ether/api/flows/*).
 * Matches docs/design/flows.md §7 and unwraps both envelope and bare responses
 * so mock mode and the live Go API (F4) stay seamlessly compatible.
 */
import { apiFetch } from "./api";
import type {
  ActionSpec,
  EventSchema,
  Flow,
  FlowLimits,
  FlowSummary,
  FlowTemplate,
  Graph,
  Run,
  RunStats,
  RunStatus,
  RunStep,
  StatsRange,
  TestRunRequest,
  TestRunResult,
  ValidationResult,
} from "../pages/flows/types";

function qs(params: Record<string, string | number | undefined | null>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== null && v !== "") q.set(k, String(v));
  }
  const s = q.toString();
  return s ? `?${s}` : "";
}

function send<T>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
  return apiFetch<T>(path, {
    method,
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  });
}

function unwrapFlow(raw: Flow | { flow: Flow }): Flow {
  return "flow" in raw && raw.flow ? raw.flow : (raw as Flow);
}

export interface RunListResponse {
  runs: Run[];
  nextCursor?: string;
}

export interface RunDetailResponse {
  run: Run;
  steps: RunStep[];
}

/** A draft flow built from a plain-language description (Describe-it). */
export interface DescribeFlowResult {
  name: string;
  description: string;
  graph: Graph;
  explanation: string;
}

export interface CustomizeNotificationRequest {
  channelId: string;
  event?: string;
  events?: string[];
  name?: string;
  description?: string;
  enabled?: boolean;
  removeLegacyRule?: boolean;
}

export interface CustomizeNotificationResult {
  flow: Flow;
  channelId: string;
  migrated: string[];
  kept: string[];
  notes?: string[];
}

export const flowsApi = {
  async listFlows(signal?: AbortSignal): Promise<FlowSummary[]> {
    const raw = await apiFetch<FlowSummary[] | { flows: FlowSummary[] | null }>("/flows", {
      signal,
    });
    if (Array.isArray(raw)) return raw;
    return raw?.flows ?? [];
  },

  /** Overview aggregates for one window, bucketed in the browser's time zone. */
  async stats(range: StatsRange, signal?: AbortSignal): Promise<RunStats> {
    let tz = "";
    try {
      tz = Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
    } catch {
      // Older engines: the server buckets in UTC.
    }
    const raw = await apiFetch<{ range: StatsRange; stats: RunStats }>(
      `/flows/stats${qs({ range, tz })}`,
      { signal },
    );
    return raw.stats;
  },

  async getFlow(id: string, signal?: AbortSignal): Promise<Flow> {
    const raw = await apiFetch<Flow | { flow: Flow }>(`/flows/${encodeURIComponent(id)}`, {
      signal,
    });
    return unwrapFlow(raw);
  },

  async createFlow(flow: Partial<Flow> & { name: string; graph: Graph }): Promise<Flow> {
    const raw = await send<Flow | { flow: Flow }>("POST", "/flows", flow);
    return unwrapFlow(raw);
  },

  async updateFlow(
    id: string,
    flow: Partial<Flow> & { name: string; graph: Graph },
  ): Promise<Flow> {
    const raw = await send<Flow | { flow: Flow }>("PUT", `/flows/${encodeURIComponent(id)}`, flow);
    return unwrapFlow(raw);
  },

  async deleteFlow(id: string): Promise<void> {
    await apiFetch<unknown>(`/flows/${encodeURIComponent(id)}`, { method: "DELETE" });
  },

  async enableFlow(id: string): Promise<Flow> {
    const raw = await send<Flow | { flow: Flow }>(
      "POST",
      `/flows/${encodeURIComponent(id)}/enable`,
      {},
    );
    return unwrapFlow(raw);
  },

  async disableFlow(id: string, cancelActive = false): Promise<Flow> {
    const raw = await send<Flow | { flow: Flow }>(
      "POST",
      `/flows/${encodeURIComponent(id)}/disable`,
      { cancelActive },
    );
    return unwrapFlow(raw);
  },

  validateGraph(
    body: { name?: string; graph: Graph; limits?: FlowLimits },
    signal?: AbortSignal,
  ): Promise<ValidationResult> {
    return send<ValidationResult>("POST", "/flows/validate", body, signal);
  },

  /** Test an unsaved draft graph (same payload as `/{id}/test`, no flow needed). */
  testDraft(req: TestRunRequest, signal?: AbortSignal): Promise<TestRunResult> {
    return send<TestRunResult>("POST", "/flows/test", req, signal);
  },

  testRun(id: string, req: TestRunRequest, signal?: AbortSignal): Promise<TestRunResult> {
    return send<TestRunResult>("POST", `/flows/${encodeURIComponent(id)}/test`, req, signal);
  },

  async runManual(id: string, inputs?: Record<string, unknown>): Promise<Run> {
    const raw = await send<Run | { run: Run }>("POST", `/flows/${encodeURIComponent(id)}/run`, {
      inputs: inputs ?? {},
    });
    return "run" in raw && raw.run ? raw.run : (raw as Run);
  },

  rotateWebhookToken(id: string): Promise<{ token: string; webhookUrl: string }> {
    return send<{ token: string; webhookUrl: string }>(
      "POST",
      `/flows/${encodeURIComponent(id)}/webhook-token/rotate`,
      {},
    );
  },

  async listRuns(
    params?: {
      flowId?: string;
      status?: RunStatus | "";
      eventType?: string;
      cursor?: string;
      limit?: number;
    },
    signal?: AbortSignal,
  ): Promise<RunListResponse> {
    const raw = await apiFetch<Run[] | { runs: Run[] | null; nextCursor?: string }>(
      `/flows/runs${qs(params ?? {})}`,
      { signal },
    );
    if (Array.isArray(raw)) return { runs: raw };
    return { runs: raw?.runs ?? [], nextCursor: raw?.nextCursor };
  },

  async listFlowRuns(
    flowId: string,
    params?: { status?: RunStatus | ""; cursor?: string; limit?: number },
    signal?: AbortSignal,
  ): Promise<RunListResponse> {
    const raw = await apiFetch<Run[] | { runs: Run[] | null; nextCursor?: string }>(
      `/flows/${encodeURIComponent(flowId)}/runs${qs(params ?? {})}`,
      { signal },
    );
    if (Array.isArray(raw)) return { runs: raw };
    return { runs: raw?.runs ?? [], nextCursor: raw?.nextCursor };
  },

  getRun(runId: string, signal?: AbortSignal): Promise<RunDetailResponse> {
    return apiFetch<RunDetailResponse>(`/flows/runs/${encodeURIComponent(runId)}`, { signal });
  },

  cancelRun(runId: string): Promise<Run> {
    return send<Run>("POST", `/flows/runs/${encodeURIComponent(runId)}/cancel`, {});
  },

  async eventCatalogue(signal?: AbortSignal): Promise<EventSchema[]> {
    const raw = await apiFetch<
      | EventSchema[]
      | { events?: EventSchema[] | null; domains?: { domain: string; events: EventSchema[] }[] }
    >("/flows/catalogue/events", { signal });
    if (Array.isArray(raw)) return raw;
    if (raw?.events) return raw.events;
    if (raw?.domains) return raw.domains.flatMap((d) => d.events ?? []);
    return [];
  },

  async actionCatalogue(signal?: AbortSignal): Promise<ActionSpec[]> {
    const raw = await apiFetch<ActionSpec[] | { actions?: ActionSpec[] | null }>(
      "/flows/catalogue/actions",
      { signal },
    );
    if (Array.isArray(raw)) return raw;
    return raw?.actions ?? [];
  },

  async listTemplates(signal?: AbortSignal): Promise<FlowTemplate[]> {
    const raw = await apiFetch<FlowTemplate[] | { templates?: FlowTemplate[] | null }>(
      "/flows/templates",
      { signal },
    );
    if (Array.isArray(raw)) return raw;
    return raw?.templates ?? [];
  },

  describe(prompt: string, signal?: AbortSignal): Promise<DescribeFlowResult> {
    return send<DescribeFlowResult>("POST", "/flows/describe", { prompt }, signal);
  },

  customizeNotification(req: CustomizeNotificationRequest): Promise<CustomizeNotificationResult> {
    return send<CustomizeNotificationResult>("POST", "/flows/customize-notification", req);
  },

  async createFromTemplate(
    templateId: string,
    body?: { name?: string; enabled?: boolean; params?: Record<string, unknown> },
  ): Promise<Flow> {
    const raw = await send<Flow | { flow: Flow }>(
      "POST",
      `/flows/from-template/${encodeURIComponent(templateId)}`,
      body ?? {},
    );
    return unwrapFlow(raw);
  },
};
