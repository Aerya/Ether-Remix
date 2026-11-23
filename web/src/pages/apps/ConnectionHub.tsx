import { Button, Input, SettingsSection, Spinner, Switch, toast } from "@braedonsaunders/appkit-ui";
import { AlertTriangle, Check, ChevronDown, ChevronRight, Copy, XCircle } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { MoreOptions } from "../../components/ConfigSection";
import { copyText } from "../../components/CopyButton";
import { ErrorPanel } from "../../components/feedback";
import {
  EmbyMark,
  householdIcon,
  JellyfinMark,
  PlexMark,
  SiloMark,
  VelaMark,
} from "../../components/integration-brands";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { WatchButton } from "../../components/WatchButton";
import { t, tx } from "../../i18n";
import { API_BASE, ApiError, apiBlob, apiFetch, errorMessage } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import type { EtherConfig } from "../../lib/types";
import { SchemaField } from "../settings/SchemaForm";
import { SERVER } from "../settings/schema";
import { StremioMark } from "../sources/marks";
import { appLabel, appPath } from "./catalog";
import {
  isLoopbackAddress,
  isSharedPortFallbackPath,
  usableAddresses,
  type ReachabilityStatus,
} from "./reachability";

export type HubFrontendInfo = {
  descriptor: {
    id: string;
    name: string;
    version?: string;
    certifiedApps?: { name: string; steps: string[] }[];
    defaultPort?: number;
    auth?: string;
  };
  enabled: boolean;
  status: ReachabilityStatus & {
    webPlayerReady?: boolean;
    clients24h?: number;
    clients24hKnown?: boolean;
    liveStreams?: number;
    liveStreamsKnown?: boolean;
    targetVersion?: string;
    conformance?: string;
  };
};

export type ParsedAddress = {
  raw: string;
  origin: string;
  protocol: string;
  host: string;
  port: string;
  pathname: string;
  isIPv6: boolean;
  isTailscale: boolean;
  isLocal: boolean;
  isPlexDirect: boolean;
  isSharedFallback: boolean;
};

export const DEDICATED_PORT_APPS = new Set(["jellyfin", "emby", "silo"]);

export async function fetchEtherConfig(): Promise<EtherConfig> {
  const res = await apiFetch<{ config?: EtherConfig } & Partial<EtherConfig>>("/config");
  return res.config ?? (res as EtherConfig);
}
const PHONE_QR_APPS = new Set(["jellyfin", "emby", "silo", "vela", "webdav", "xtream"]);

export function appBrandMark(id: string): ReactNode {
  switch (id) {
    case "plex":
      return <PlexMark />;
    case "jellyfin":
      return <JellyfinMark />;
    case "emby":
      return <EmbyMark />;
    case "silo":
      return <SiloMark />;
    case "vela":
      return <VelaMark />;
    case "stremio":
      return <StremioMark />;
    default:
      return householdIcon(id);
  }
}

export function parseAddress(address: string): ParsedAddress | null {
  const trimmed = address.trim();
  if (!trimmed || isLoopbackAddress(trimmed)) return null;
  try {
    const url = new URL(trimmed.includes("://") ? trimmed : `http://${trimmed}`);
    const host = url.hostname.replace(/^\[|\]$/g, "").toLowerCase();
    const isIPv6 = host.includes(":");
    const isTailscale =
      /^100[.-](6[4-9]|[7-9]\d|1[01]\d|12[0-7])[.-]/.test(host) ||
      host.startsWith("fd7a:115c:a1e0:") ||
      host.endsWith(".ts.net");
    const isLocal =
      !isTailscale &&
      (/^(192[.-]168[.-]|10[.-]|172[.-](1[6-9]|2\d|3[01])[.-]|169[.-]254[.-])/.test(host) ||
        /^(f[cd][0-9a-f]{0,2}|fe[89ab][0-9a-f]):/i.test(host) ||
        host.endsWith(".local") ||
        (!isIPv6 && !host.includes(".")));
    const pathname = url.pathname || "/";
    return {
      raw: trimmed.includes("://") ? trimmed : url.toString().replace(/\/$/, ""),
      origin: url.origin,
      protocol: url.protocol,
      host,
      port: url.port || (url.protocol === "https:" ? "443" : "80"),
      pathname,
      isIPv6,
      isTailscale,
      isLocal,
      isPlexDirect: host.endsWith(".plex.direct"),
      isSharedFallback: isSharedPortFallbackPath(trimmed),
    };
  } catch {
    return null;
  }
}

function localRank(item: ParsedAddress, preferredPort: string): number {
  let score = 0;
  if (item.isSharedFallback) score += 50;
  if (item.isIPv6) score += 100;
  if (item.isPlexDirect) score += 20;
  if (/^192[.-]168[.-]/.test(item.host)) score += 0;
  else if (/^10[.-]255[.-]/.test(item.host)) score += 30;
  else if (/^10[.-]/.test(item.host)) score += 2;
  else if (/^172[.-]17[.-]/.test(item.host)) score += 25;
  else if (/^172[.-](1[6-9]|2\d|3[01])[.-]/.test(item.host)) score += 4;
  else score += 10;
  if (preferredPort && item.port === preferredPort && !item.isSharedFallback) score -= 5;
  return score;
}

function secondaryLabel(
  item: ParsedAddress,
  publishedSet: ReadonlySet<string>,
  observedSet: ReadonlySet<string>,
  hasDedicated: boolean,
): string {
  if (publishedSet.has(item.raw)) return t("Published");
  if (observedSet.has(item.raw)) return t("Seen recently");
  if (item.isSharedFallback && hasDedicated) {
    if (item.isTailscale) return t("Tailscale (also reachable at)");
    return t("Also reachable at");
  }
  if (item.isTailscale) {
    if (item.isIPv6) return t("Tailscale (IPv6)");
    return t("Tailscale");
  }
  if (item.isLocal) {
    if (item.isPlexDirect) return t("Secure local URL");
    if (item.isIPv6) return t("LAN IPv6");
    return t("Other network interface");
  }
  return t("Remote");
}

function secondarySortWeight(
  item: ParsedAddress,
  publishedSet: ReadonlySet<string>,
  observedSet: ReadonlySet<string>,
): number {
  if (publishedSet.has(item.raw)) return 5;
  if (observedSet.has(item.raw)) return 8;
  if (item.isSharedFallback && !item.isTailscale && !item.isIPv6) return 10;
  if (item.isTailscale && !item.isIPv6 && !item.isSharedFallback) return 20;
  if (!item.isLocal && !item.isTailscale) return 30;
  if (item.isLocal && item.isIPv6) return 40;
  if (item.isTailscale) return 50;
  return 60;
}

export function classifyAppAddresses(
  status: ReachabilityStatus | null | undefined,
  options?: { preferredPort?: string; configuredRemote?: string; publishedAddresses?: string[] },
): {
  primary?: string;
  others: { address: string; label: string }[];
  hasRemoteAddress: boolean;
} {
  const seen = new Set<string>();
  const publishedSet = new Set<string>();
  const observedSet = new Set<string>();
  const parsed: ParsedAddress[] = [];
  const addItem = (
    raw: string,
    bucket?: Set<string>,
    filter?: (item: ParsedAddress) => boolean,
  ) => {
    const item = parseAddress(raw);
    if (!item || (filter && !filter(item))) return;
    if (bucket) bucket.add(item.raw);
    if (seen.has(item.raw)) return;
    seen.add(item.raw);
    parsed.push(item);
  };

  for (const candidate of usableAddresses(status)) {
    addItem(candidate);
  }
  for (const candidate of options?.publishedAddresses ?? []) {
    addItem(candidate, publishedSet);
  }
  for (const candidate of status?.publishedAddresses ?? []) {
    addItem(candidate, publishedSet);
  }
  for (const candidate of status?.observedAddresses ?? []) {
    addItem(candidate, observedSet);
  }
  if (options?.configuredRemote) {
    addItem(options.configuredRemote, undefined, (item) => !item.isLocal);
  }
  if (status?.wanAddress) {
    addItem(status.wanAddress, undefined, (item) => !item.isLocal && !item.isTailscale);
  }

  const preferredPort = options?.preferredPort ?? "";
  const hasDedicated = parsed.some(
    (item) => !item.isSharedFallback && !publishedSet.has(item.raw) && !observedSet.has(item.raw),
  );
  const localIPv4 = parsed
    .filter((item) => item.isLocal && !item.isIPv6 && !item.isPlexDirect)
    .sort((a, b) => localRank(a, preferredPort) - localRank(b, preferredPort));
  const tailscaleIPv4 = parsed.filter(
    (item) => item.isTailscale && !item.isIPv6 && (!hasDedicated || !item.isSharedFallback),
  );
  const localAny = parsed
    .filter((item) => item.isLocal)
    .sort((a, b) => localRank(a, preferredPort) - localRank(b, preferredPort));

  const primaryItem = localIPv4[0] ?? tailscaleIPv4[0] ?? localAny[0] ?? parsed[0];
  const others = parsed
    .filter((item) => item.raw !== primaryItem?.raw)
    .sort(
      (a, b) =>
        secondarySortWeight(a, publishedSet, observedSet) -
        secondarySortWeight(b, publishedSet, observedSet),
    )
    .map((item) => ({
      address: item.raw,
      label: secondaryLabel(item, publishedSet, observedSet, hasDedicated),
    }));

  const hasRemoteAddress = parsed.some((item) => !item.isLocal);
  return { primary: primaryItem?.raw, others, hasRemoteAddress };
}

export type PortConflict = {
  busyPort: number;
  actualPort: number;
  holder?: string;
};

function portFromEndpoint(value?: string): number | undefined {
  if (!value) return undefined;
  const parsed = parseAddress(value);
  if (parsed?.port) {
    const n = Number(parsed.port);
    if (Number.isInteger(n) && n > 0) return n;
  }
  const match = /:(\d+)(?:\/|$)/.exec(value.trim());
  if (!match) return undefined;
  const n = Number(match[1]);
  return Number.isInteger(n) && n > 0 ? n : undefined;
}

export function portConflictInfo(
  status: ReachabilityStatus | null | undefined,
): PortConflict | null {
  if (!status) return null;
  if (status.busyPort && status.busyPort > 0) {
    const actual =
      (status.actualPort && status.actualPort > 0 ? status.actualPort : undefined) ??
      portFromEndpoint(status.addresses?.[0]) ??
      portFromEndpoint(usableAddresses(status)[0]);
    if (actual && actual !== status.busyPort) {
      const holder = status.busyPortHolder?.trim();
      return {
        busyPort: status.busyPort,
        actualPort: actual,
        ...(holder ? { holder } : {}),
      };
    }
  }
  for (const warning of status.warnings ?? []) {
    const match =
      /^Another server on this computer is using port (\d+); Ether still works at (.+)$/.exec(
        warning,
      );
    if (match) {
      const busyPort = Number(match[1]);
      const actualPort =
        portFromEndpoint(match[2]) ??
        portFromEndpoint(status.addresses?.[0]) ??
        portFromEndpoint(usableAddresses(status)[0]);
      if (busyPort > 0 && actualPort && actualPort !== busyPort) {
        const holder = status.busyPortHolder?.trim();
        return { busyPort, actualPort, ...(holder ? { holder } : {}) };
      }
    }
  }
  return null;
}

export function formatPortConflict(conflict: PortConflict): string {
  if (conflict.holder) {
    return t("Using {actual}: port {busy} is taken by {program} on this computer", {
      actual: String(conflict.actualPort),
      busy: String(conflict.busyPort),
      program: conflict.holder,
    });
  }
  return t("Using {actual}: port {busy} is taken by another program on this computer", {
    actual: String(conflict.actualPort),
    busy: String(conflict.busyPort),
  });
}

export function appLiveStatus(info: HubFrontendInfo): {
  tone: "ok" | "off" | "warning" | "error";
  text: string;
} {
  if (!info.enabled) {
    return { tone: "off", text: t("Off") };
  }
  if (info.status.error) {
    return { tone: "error", text: info.status.error };
  }
  const conflict = portConflictInfo(info.status);
  if (conflict) {
    return { tone: "warning", text: formatPortConflict(conflict) };
  }
  if (info.descriptor.id === "vela" && info.status.webPlayerReady === false) {
    return { tone: "off", text: t("Not installed") };
  }
  return { tone: "ok", text: t("Listening") };
}

function CopyAddressButton({ address, ariaLabel }: { address: string; ariaLabel?: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const timer = window.setTimeout(() => setCopied(false), 1600);
    return () => window.clearTimeout(timer);
  }, [copied]);
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      aria-label={ariaLabel ?? t("Copy {address}", { address })}
      onClick={(e) => {
        e.stopPropagation();
        void copyText(address).then((ok) => {
          if (ok) setCopied(true);
        });
      }}
    >
      {copied ? <Check size={15} /> : <Copy size={15} />}
      {copied ? <span>{t("Copied")}</span> : null}
    </Button>
  );
}

export function OverviewPortConflictChip({ apps }: { apps: HubFrontendInfo[] }) {
  const conflicts = apps.flatMap((info) => {
    if (!info.enabled) return [];
    const conflict = portConflictInfo(info.status);
    if (!conflict) return [];
    return [{ info, conflict }];
  });
  if (conflicts.length === 0) return null;
  const first = conflicts[0]!;
  const appName = appLabel(first.info.descriptor.id, first.info.descriptor.name);
  const label =
    conflicts.length === 1
      ? t("Port conflict · {app} using {actual} ({busy} in use)", {
          app: appName,
          actual: String(first.conflict.actualPort),
          busy: String(first.conflict.busyPort),
        })
      : t("{count} port conflicts", { count: String(conflicts.length) });
  return (
    <Link
      to={`/apps/${appPath(first.info.descriptor.id)}`}
      data-testid="overview-port-conflict-chip"
      className="inline-flex items-center gap-2 rounded-full border border-warning/40 bg-warning/10 px-3 py-1.5 text-xs font-medium text-warning transition-colors hover:bg-warning/15"
    >
      <AlertTriangle size={14} aria-hidden />
      <span>{label}</span>
    </Link>
  );
}

function deriveServerAddresses(
  frontends: HubFrontendInfo[],
  serverPort: number,
  publicHost?: string,
  customUrls?: string[] | null,
): {
  local?: string;
  tailscale?: string;
  remote?: string;
} {
  const seen = new Set<string>();
  const origins: ParsedAddress[] = [];
  const addOrigin = (raw?: string, keepOriginOnly = false) => {
    if (!raw) return;
    const item = parseAddress(raw);
    if (!item) return;
    const normalized = keepOriginOnly ? item.origin : item.raw;
    const parsedNorm = parseAddress(normalized);
    if (!parsedNorm || seen.has(parsedNorm.raw)) return;
    seen.add(parsedNorm.raw);
    origins.push(parsedNorm);
  };

  const plex = frontends.find((f) => f.descriptor.id === "plex");
  for (const url of usableAddresses(plex?.status)) {
    addOrigin(url, true);
  }
  for (const url of plex?.status.observedAddresses ?? []) {
    addOrigin(url, true);
  }
  for (const url of plex?.status.publishedAddresses ?? []) {
    addOrigin(url, true);
  }
  if (plex?.status.wanAddress) {
    addOrigin(plex.status.wanAddress, true);
  }
  for (const app of frontends) {
    if (app.descriptor.id === "plex") continue;
    for (const url of usableAddresses(app.status)) {
      const parsed = parseAddress(url);
      if (parsed && parsed.port === String(serverPort)) {
        addOrigin(parsed.origin, true);
      }
    }
  }
  if (
    typeof window !== "undefined" &&
    window.location?.origin &&
    window.location.origin !== "null"
  ) {
    const loc = parseAddress(window.location.origin);
    if (loc && loc.port === String(serverPort)) {
      addOrigin(loc.origin, true);
    }
  }
  if (publicHost?.trim()) {
    const rawHost = publicHost.trim();
    addOrigin(rawHost.includes("://") ? rawHost : `https://${rawHost}`);
  }
  for (const custom of customUrls ?? []) {
    const parsed = parseAddress(custom);
    if (parsed && !parsed.isLocal && !parsed.isTailscale) {
      addOrigin(parsed.origin, true);
    }
  }

  const localCandidates = origins
    .filter((item) => item.isLocal && !item.isIPv6 && !item.isPlexDirect)
    .sort((a, b) => localRank(a, String(serverPort)) - localRank(b, String(serverPort)));
  const localFallback = origins
    .filter((item) => item.isLocal)
    .sort((a, b) => localRank(a, String(serverPort)) - localRank(b, String(serverPort)));
  const tailscaleCandidates = origins.filter((item) => item.isTailscale && !item.isIPv6);
  const tailscaleFallback = origins.filter((item) => item.isTailscale);
  const remoteCandidates = origins.filter((item) => !item.isLocal && !item.isTailscale);

  return {
    local: (localCandidates[0] ?? localFallback[0])?.raw,
    tailscale: (tailscaleCandidates[0] ?? tailscaleFallback[0])?.raw,
    remote: remoteCandidates[0]?.raw,
  };
}

function ServerAddressSlot({
  label,
  setting,
  address,
  emptyText,
}: {
  label: string;
  setting: string;
  address?: string;
  emptyText: string;
}) {
  return (
    <div className="flex min-w-0 items-center justify-between gap-2 rounded-lg border border-border bg-bg-subtle/60 px-3.5 py-2.5">
      <div className="min-w-0 flex-1">
        <p data-setting={setting} className="text-xs font-medium text-fg-muted">
          {label}
        </p>
        {address ? (
          <p className="mt-0.5 truncate font-mono text-sm text-fg" title={address}>
            {address}
          </p>
        ) : (
          <p className="mt-0.5 text-sm text-fg-muted">{emptyText}</p>
        )}
      </div>
      {address ? (
        <CopyAddressButton
          address={address}
          ariaLabel={t("Copy {label} address", { label: label.toLowerCase() })}
        />
      ) : null}
    </div>
  );
}

export function ServerAddressPanel({
  frontends,
  configData,
  configError,
  onReloadConfig,
}: {
  frontends: HubFrontendInfo[];
  configData?: EtherConfig;
  configError?: unknown;
  onReloadConfig?: () => Promise<unknown>;
}) {
  const internalConfig = useAsync(
    () =>
      configData || configError !== undefined ? Promise.resolve(undefined) : fetchEtherConfig(),
    [Boolean(configData), configError !== undefined],
  );
  const resolvedConfig = configData ?? internalConfig.data;
  const resolvedError = configError ?? internalConfig.error;
  const reloadConfig = onReloadConfig ?? (() => internalConfig.reload());

  const server = resolvedConfig?.server;
  const { draft, setDraft, dirty, reset } = useDraft(server);
  const [savingSwitch, setSavingSwitch] = useState(false);
  const [savingMore, setSavingMore] = useState(false);
  const [remoteOverride, setRemoteOverride] = useState<boolean>();
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<unknown>();

  const moreFields = (SERVER.find((group) => group.id === "connect")?.more ?? []).filter(
    (field) => field.key !== "allowRemoteConsole",
  );
  const serverPort = server?.port ?? 32400;
  const allowRemote =
    remoteOverride ??
    Boolean((server as unknown as Record<string, unknown> | undefined)?.allowRemoteConsole);
  const { local, tailscale, remote } = deriveServerAddresses(
    frontends,
    serverPort,
    resolvedConfig?.plex?.publicHost,
    resolvedConfig?.plex?.customUrls,
  );

  return (
    <SettingsSection
      title={t("Server address")}
      description={t("Ether's main address for this console and shared-port apps.")}
    >
      {resolvedError ? (
        <ErrorPanel error={resolvedError} onRetry={() => void reloadConfig()} />
      ) : !resolvedConfig ? (
        <div className="px-5 py-4">
          <Spinner size={18} label={t("Loading settings")} />
        </div>
      ) : (
        <>
          <div className="space-y-3 px-5 py-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <span className="text-xs font-medium uppercase tracking-wide text-fg-muted">
                {t("Addresses")}
              </span>
              <span
                data-setting="server-port"
                className="inline-flex items-center gap-1.5 rounded-md border border-border bg-bg-subtle px-2.5 py-1 font-mono text-xs text-fg"
              >
                <span className="font-sans text-fg-muted">{t("Main port")}</span>
                <span>{serverPort}</span>
              </span>
            </div>
            <div className="grid gap-2.5 lg:grid-cols-3">
              <ServerAddressSlot
                label={t("Local")}
                setting="local-address"
                address={local}
                emptyText={t("Not reported")}
              />
              <ServerAddressSlot
                label={t("Remote")}
                setting="remote-address"
                address={remote}
                emptyText={t("Not configured")}
              />
              <ServerAddressSlot
                label={t("Tailscale")}
                setting="tailscale-address"
                address={tailscale}
                emptyText={t("Not detected")}
              />
            </div>
          </div>
          <SettingsRow
            setting="allowRemoteConsole"
            title={t("Open this console from outside your network")}
            description={t(
              "For reaching this console through your own domain or reverse proxy when you're away. Only the Plex account that owns this server can sign in. Off keeps it to your home network and VPN.",
            )}
            control={
              <Switch
                aria-label={t("Open this console from outside your network")}
                checked={allowRemote}
                disabled={savingSwitch}
                onChange={(e) => {
                  const next = e.currentTarget.checked;
                  setRemoteOverride(next);
                  setSavingSwitch(true);
                  void apiFetch("/config/server", {
                    method: "PUT",
                    body: JSON.stringify({ allowRemoteConsole: next }),
                  })
                    .then(async () => {
                      await reloadConfig();
                      setRemoteOverride(undefined);
                    })
                    .catch((err: unknown) => {
                      setRemoteOverride(undefined);
                      toast.error(t("Couldn't save"), { description: errorMessage(err) });
                    })
                    .finally(() => setSavingSwitch(false));
                }}
              />
            }
          />
          {moreFields.length > 0 && draft ? (
            <MoreOptions
              setting="connect"
              label={t("More options: Server address")}
              hasError={moreFields.some((f) => Boolean(errors[`server.${f.key}`]))}
            >
              {moreFields.map((field) => (
                <SchemaField
                  key={field.key}
                  def={field}
                  value={(draft as unknown as Record<string, unknown>)[field.key]}
                  path={`server.${field.key}`}
                  error={errors[`server.${field.key}`]}
                  errors={errors}
                  onChange={(value) => setDraft({ ...draft, [field.key]: value })}
                />
              ))}
              <SaveBar
                dirty={dirty}
                saving={savingMore}
                error={error}
                onDiscard={() => {
                  reset();
                  setErrors({});
                  setError(undefined);
                }}
                onSave={async () => {
                  if (!draft) return;
                  setSavingMore(true);
                  setErrors({});
                  setError(undefined);
                  try {
                    const values = draft as unknown as Record<string, unknown>;
                    await apiFetch("/config/server", {
                      method: "PUT",
                      body: JSON.stringify(
                        Object.fromEntries(moreFields.map((f) => [f.key, values[f.key]])),
                      ),
                    });
                    await reloadConfig();
                  } catch (e) {
                    if (e instanceof ApiError && e.fields) {
                      setErrors(Object.fromEntries(e.fields.map((f) => [f.field, f.message])));
                    }
                    setError(e);
                  } finally {
                    setSavingMore(false);
                  }
                }}
              />
            </MoreOptions>
          ) : null}
        </>
      )}
    </SettingsSection>
  );
}

function StatusIndicator({
  tone,
  text,
}: {
  tone: "ok" | "off" | "warning" | "error";
  text: string;
}) {
  if (tone === "warning") {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium text-warning">
        <AlertTriangle size={13} className="shrink-0" aria-hidden />
        <span>{text}</span>
      </span>
    );
  }
  if (tone === "error") {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs font-medium text-danger">
        <XCircle size={13} className="shrink-0" aria-hidden />
        <span>{text}</span>
      </span>
    );
  }
  if (tone === "ok") {
    return (
      <span className="inline-flex items-center gap-1.5 text-xs text-fg-muted">
        <span className="size-2 shrink-0 rounded-full bg-success" aria-hidden />
        <span className="text-fg">{text}</span>
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-fg-muted">
      <span className="size-2 shrink-0 rounded-full bg-fg-muted/45" aria-hidden />
      <span>{text}</span>
    </span>
  );
}

export function AppOverviewRow({
  info,
  busy,
  disabled,
  onToggle,
}: {
  info: HubFrontendInfo;
  busy: boolean;
  disabled: boolean;
  onToggle: (enabled: boolean) => void;
}) {
  const id = info.descriptor.id;
  const name = appLabel(id, info.descriptor.name);
  const href = `/apps/${appPath(id)}`;
  const live = appLiveStatus(info);
  const conflict = portConflictInfo(info.status);
  const preferredPort = String(
    conflict?.actualPort ?? (id === "vela" ? "" : (info.descriptor.defaultPort ?? "")),
  );
  const { primary } = classifyAppAddresses(info.status, { preferredPort });

  return (
    <div
      data-app-id={id}
      className="group relative flex flex-wrap items-center justify-between gap-x-4 gap-y-2 px-5 py-3.5 transition-colors hover:bg-bg-subtle/60"
    >
      <div className="flex min-w-0 flex-1 items-center gap-3.5">
        <div className="flex size-9 shrink-0 items-center justify-center rounded-lg border border-border bg-bg-subtle/70">
          {appBrandMark(id)}
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-x-2.5 gap-y-0.5">
            <Link
              to={href}
              className="font-medium text-fg after:absolute after:inset-0 after:content-[''] group-hover:text-primary"
            >
              {name}
            </Link>
          </div>
          <div className="mt-0.5">
            <StatusIndicator tone={live.tone} text={live.text} />
          </div>
        </div>
      </div>

      <div className="flex shrink-0 flex-wrap items-center gap-3">
        {info.enabled && primary ? (
          <div
            className="relative z-10 flex items-center gap-1.5 rounded-lg border border-border bg-bg-subtle px-2.5 py-1"
            onClick={(e) => e.stopPropagation()}
          >
            <code className="break-all font-mono text-xs text-fg sm:text-sm">{primary}</code>
            <CopyAddressButton
              address={primary}
              ariaLabel={t("Copy {app} address", { app: name })}
            />
          </div>
        ) : null}
        <div
          className="relative z-10 flex shrink-0 items-center gap-2.5"
          onClick={(e) => e.stopPropagation()}
        >
          <Switch
            aria-label={t("{app} enabled", { app: name })}
            checked={info.enabled}
            disabled={busy || disabled}
            onChange={(e) => onToggle(e.currentTarget.checked)}
          />
          <ChevronRight
            size={16}
            aria-hidden
            className="text-fg-muted transition-transform group-hover:translate-x-0.5 group-hover:text-fg"
          />
        </div>
      </div>
    </div>
  );
}

export function AppAddressCard({
  info,
  configuredRemote,
  publishedAddresses,
  onEditPublishedAddresses,
  velaPort,
  port,
  onPortChange,
  onSavePlexPort,
  serverPort,
  busy,
  onToggle,
}: {
  info: HubFrontendInfo;
  configuredRemote?: string;
  publishedAddresses?: string[];
  onEditPublishedAddresses?: () => void;
  velaPort?: { port?: number; serveOnMainPort: boolean; onEdit: () => void };
  port?: number;
  onPortChange?: (port: number | undefined) => void;
  onSavePlexPort?: (port: number) => Promise<void>;
  serverPort?: number;
  busy?: boolean;
  onToggle?: (enabled: boolean) => void;
}) {
  const id = info.descriptor.id;
  const name = appLabel(id, info.descriptor.name);
  const live = appLiveStatus(info);
  const conflict = portConflictInfo(info.status);
  const headerStatus =
    info.enabled && conflict && !info.status.error
      ? { tone: "ok" as const, text: t("Listening") }
      : live;
  const preferredPort = String(
    conflict?.actualPort ??
      (id === "vela"
        ? (velaPort?.port ?? port ?? serverPort ?? "")
        : (port ?? info.descriptor.defaultPort ?? "")),
  );
  const effectivePublished = publishedAddresses ?? info.status.publishedAddresses ?? [];
  const { primary, others, hasRemoteAddress } = classifyAppAddresses(info.status, {
    preferredPort,
    configuredRemote,
    publishedAddresses: effectivePublished,
  });
  const showRemoteHint =
    info.enabled &&
    Boolean(primary) &&
    !hasRemoteAddress &&
    id !== "dlna" &&
    info.descriptor.auth !== "none_lan";
  const remoteSetupPath = id === "plex" ? "/apps/plex/remote" : "/apps/overview";
  const [showOthers, setShowOthers] = useState(true);
  const savedPlexPort = port ?? serverPort ?? 32400;
  const [plexPortText, setPlexPortText] = useState(String(savedPlexPort));
  const [plexPortSaving, setPlexPortSaving] = useState(false);
  const [plexPortError, setPlexPortError] = useState<unknown>();

  useEffect(() => {
    setPlexPortText(String(savedPlexPort));
  }, [savedPlexPort]);

  const editableDedicatedPort = DEDICATED_PORT_APPS.has(id);
  const editablePlexPort = id === "plex" && onSavePlexPort != null;
  const plexPortDirty = plexPortText.trim() !== String(savedPlexPort);

  return (
    <SettingsSection>
      <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-4">
        <div className="flex min-w-0 items-center gap-3.5">
          <div className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-border bg-bg-subtle/70">
            {appBrandMark(id)}
          </div>
          <div className="min-w-0">
            <h2 className="text-base font-semibold text-fg">{name}</h2>
            <div className="mt-0.5">
              <StatusIndicator tone={headerStatus.tone} text={headerStatus.text} />
            </div>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-3">
          {id === "vela" && info.status.webPlayerReady ? (
            <WatchButton app="vela" variant="outline" />
          ) : null}
          {onToggle ? (
            <Switch
              aria-label={t("{app} enabled", { app: name })}
              checked={info.enabled}
              disabled={busy}
              onChange={(e) => onToggle(e.currentTarget.checked)}
            />
          ) : null}
        </div>
      </div>

      {info.enabled && conflict ? (
        <div
          role="status"
          className="flex items-start gap-2.5 border-b border-warning/25 bg-warning/10 px-5 py-3 text-sm text-warning"
        >
          <AlertTriangle size={16} className="mt-0.5 shrink-0" aria-hidden />
          <span>{formatPortConflict(conflict)}</span>
        </div>
      ) : null}

      {info.status.error ? (
        <div
          role="alert"
          className="flex items-start gap-2.5 border-b border-danger/25 bg-danger/10 px-5 py-3 text-sm text-danger"
        >
          <XCircle size={16} className="mt-0.5 shrink-0" aria-hidden />
          <span>{info.status.error}</span>
        </div>
      ) : null}

      <div className="space-y-3 px-5 py-4">
        {info.enabled && primary ? (
          <>
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-bg-subtle px-3.5 py-2.5">
              <div className="min-w-0 flex-1">
                <p className="text-xs font-medium text-fg-muted">{t("Primary address")}</p>
                <a
                  href={primary}
                  className="mt-0.5 block break-all font-mono text-sm font-medium text-primary hover:underline"
                >
                  {primary}
                </a>
              </div>
              <CopyAddressButton address={primary} ariaLabel={t("Copy address")} />
            </div>
            {showRemoteHint ? (
              <p className="text-xs text-fg-muted">
                {tx("Only local requests seen so far. {link} to reach {app} away from home.", {
                  link: (
                    <Link to={remoteSetupPath} className="font-medium text-primary hover:underline">
                      {t("Set up remote access")}
                    </Link>
                  ),
                  app: name,
                })}
              </p>
            ) : null}
          </>
        ) : !info.enabled ? (
          <p className="text-sm text-fg-muted">
            {t("Turn on {app} to start listening for connections.", { app: name })}
          </p>
        ) : (
          <p className="text-sm text-fg-muted">{t("No connection address reported yet.")}</p>
        )}

        {info.enabled && others.length > 0 ? (
          <div className="pt-1">
            <button
              type="button"
              className="inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:underline"
              aria-expanded={showOthers}
              onClick={() => setShowOthers((v) => !v)}
            >
              <span>{t("Other addresses")}</span>
              <span className="text-fg-muted">({others.length})</span>
              <ChevronDown
                size={14}
                aria-hidden
                className={showOthers ? "rotate-180 transition-transform" : "transition-transform"}
              />
            </button>
            {showOthers ? (
              <div className="mt-2 divide-y divide-border rounded-lg border border-border bg-surface">
                {others.map((item) => (
                  <div
                    key={item.address}
                    className="flex flex-wrap items-center justify-between gap-2 px-3.5 py-2"
                  >
                    <div className="min-w-0 flex-1">
                      <span className="text-xs text-fg-muted">{item.label}</span>
                      <p className="break-all font-mono text-sm text-fg">{item.address}</p>
                    </div>
                    <CopyAddressButton
                      address={item.address}
                      ariaLabel={t("Copy {address}", { address: item.address })}
                    />
                  </div>
                ))}
              </div>
            ) : null}
          </div>
        ) : null}
      </div>

      {editableDedicatedPort ? (
        <SettingsRow
          setting="port"
          title={<label htmlFor={`${id}-port`}>{t("Port")}</label>}
          description={t("Default port is {port}. Saving switches the listener right away.", {
            port: String(info.descriptor.defaultPort ?? ""),
          })}
          control={
            <Input
              id={`${id}-port`}
              aria-label={t("Port")}
              type="number"
              min={1}
              max={65535}
              className="w-28"
              placeholder={String(info.descriptor.defaultPort ?? "")}
              value={port ?? ""}
              disabled={!onPortChange}
              onChange={(e) => {
                const raw = e.currentTarget.value.trim();
                onPortChange?.(raw === "" ? undefined : Number(raw));
              }}
            />
          }
        />
      ) : editablePlexPort ? (
        <>
          <SettingsRow
            setting="port"
            title={<label htmlFor="plex-port">{t("Port")}</label>}
            description={t(
              "Ether's main port on this computer — Plex Media Server uses 32400. Saving restarts Ether and reconnects this page.",
            )}
            control={
              <div className="flex items-center gap-2">
                <Input
                  id="plex-port"
                  aria-label={t("Port")}
                  type="number"
                  min={1}
                  max={65535}
                  className="w-28"
                  value={plexPortText}
                  disabled={plexPortSaving}
                  onChange={(e) => setPlexPortText(e.currentTarget.value)}
                />
                {plexPortDirty ? (
                  <Button
                    type="button"
                    size="sm"
                    disabled={plexPortSaving}
                    onClick={() => {
                      const nextPort = Number(plexPortText.trim());
                      if (!Number.isInteger(nextPort) || nextPort < 1 || nextPort > 65535) {
                        setPlexPortError(new Error(t("Enter a port between 1 and 65535.")));
                        return;
                      }
                      setPlexPortSaving(true);
                      setPlexPortError(undefined);
                      void onSavePlexPort(nextPort)
                        .catch((err: unknown) => setPlexPortError(err))
                        .finally(() => setPlexPortSaving(false));
                    }}
                  >
                    {t("Save port")}
                  </Button>
                ) : null}
              </div>
            }
          />
          {plexPortError != null ? <ErrorPanel error={plexPortError} /> : null}
        </>
      ) : velaPort ? (
        <SettingsRow
          setting="port"
          title={t("Port")}
          description={
            velaPort.port && !velaPort.serveOnMainPort
              ? t("Listening on dedicated port {port}. /vela is turned off on Ether's main port.", {
                  port: String(velaPort.port),
                })
              : velaPort.port
                ? t("Listening on dedicated port {port} and Ether's main port ({mainPort}).", {
                    port: String(velaPort.port),
                    mainPort: String(serverPort ?? 32400),
                  })
                : t(
                    "Shared on Ether's main port. Use a dedicated port if your reverse proxy gives Vela its own address.",
                  )
          }
          control={
            <div className="flex items-center gap-2">
              <span className="rounded-md border border-border bg-bg-subtle px-2.5 py-1 font-mono text-xs text-fg">
                {velaPort.port || serverPort || 32400}
              </span>
              <Button type="button" variant="outline" size="sm" onClick={velaPort.onEdit}>
                {velaPort.port ? t("Edit") : t("Set port")}
              </Button>
            </div>
          }
        />
      ) : (
        <SettingsRow
          title={t("Port")}
          description={t("Shared on Ether's main port.")}
          control={
            <span className="rounded-md border border-border bg-bg-subtle px-2.5 py-1 font-mono text-xs text-fg">
              {serverPort ?? 32400}
            </span>
          }
        />
      )}
      {onEditPublishedAddresses ? (
        <SettingsRow
          setting="publishedAddresses"
          title={t("Published addresses")}
          description={
            effectivePublished.length > 0
              ? t("Used for QR codes, invites, and share links.")
              : t("Optional. Set addresses to use in QR codes, invites, and share links.")
          }
          control={
            <Button type="button" variant="outline" size="sm" onClick={onEditPublishedAddresses}>
              {effectivePublished.length > 0
                ? t("Edit ({count})", { count: String(effectivePublished.length) })
                : t("Add")}
            </Button>
          }
        />
      ) : null}
    </SettingsSection>
  );
}

function inlineCode(text: string): ReactNode {
  return (
    <code className="rounded border border-border bg-bg-subtle px-1.5 py-0.5 font-mono text-xs text-fg">
      {text}
    </code>
  );
}

function connectInstructions(
  info: HubFrontendInfo,
  address: string,
  serverName: string,
): {
  clients: string;
  steps: ReactNode[];
  accountLink?: boolean;
} {
  const id = info.descriptor.id;
  switch (id) {
    case "jellyfin":
      return {
        clients: t("Jellyfin, Swiftfin, Streamyfin, Infuse"),
        steps: [
          tx("Open the app and choose {action}.", {
            action: <strong className="font-medium text-fg">{t("Add server")}</strong>,
          }),
          tx("Enter {address}.", { address: inlineCode(address) }),
          t(
            "Sign in with your Ether viewer username and password (or Quick Connect; Infuse uses an app password from your account page).",
          ),
        ],
      };
    case "emby":
      return {
        clients: t("Emby for iPhone, iPad, Android, Windows and macOS, Infuse, SenPlayer"),
        steps: [
          tx("Open the app and choose {action} (skip Emby Connect).", {
            action: <strong className="font-medium text-fg">{t("Add server")}</strong>,
          }),
          tx("Enter {address}.", { address: inlineCode(address) }),
          t("Sign in with your Ether viewer username and password."),
        ],
      };
    case "silo":
      return {
        clients: t("Silo for iPhone, iPad, Apple TV and Mac"),
        steps: [
          tx("Open Silo and choose {action}.", {
            action: <strong className="font-medium text-fg">{t("Add server")}</strong>,
          }),
          tx("Enter {address}.", { address: inlineCode(address) }),
          t("Sign in with your Ether viewer username and password."),
        ],
      };
    case "vela":
      return {
        clients: t("Web browser, Vela for macOS, Windows, iPhone, iPad and Apple TV"),
        steps: [
          tx("Open {address} in a browser, or open the Vela app and choose {action}.", {
            address: inlineCode(address),
            action: <strong className="font-medium text-fg">{t("Add server")}</strong>,
          }),
          t("Sign in with your Ether viewer account."),
        ],
      };
    case "plex":
      return {
        clients: t("Plex for TVs, Apple TV, iPhone, iPad, Android, Roku and web"),
        steps: [
          t("Sign in and claim this server below so Plex apps find it automatically."),
          t("Open the Plex app on your device and sign in with your Plex account."),
          tx("Pick {server} from your libraries (local address: {address}).", {
            server: <strong className="font-medium text-fg">{serverName}</strong>,
            address: inlineCode(address),
          }),
        ],
      };
    case "stremio":
      return {
        clients: t("Stremio on TV, desktop, Android and web"),
        steps: [
          t("Open your account page and copy your personal Stremio addon link."),
          tx("Paste that link into Stremio › Addons (served from {address}).", {
            address: inlineCode(address),
          }),
          t("Stremio syncs the addon to your TV and phone automatically."),
        ],
        accountLink: true,
      };
    case "dlna":
      return {
        clients: t("Smart TVs, VLC and players on your home Wi-Fi"),
        steps: [
          t("Choose which viewer DLNA devices browse as below."),
          t("Open your TV's Media Player or VLC › Local Network on the same Wi-Fi."),
          tx("Select {server} when it appears (or open {address}).", {
            server: <strong className="font-medium text-fg">{serverName}</strong>,
            address: inlineCode(address),
          }),
        ],
      };
    case "webdav":
      return {
        clients: t("Infuse, VidHub, VLC, Finder, Windows Explorer, Files on iPhone and iPad"),
        steps: [
          t("Open your account page and create an app password for Files."),
          tx("In your app's Connect to Server screen, enter {address}.", {
            address: inlineCode(address),
          }),
          t("Sign in with your viewer username and that app password."),
        ],
        accountLink: true,
      };
    case "m3u":
      return {
        clients: t("UHF, TiviMate, Channels, VLC"),
        steps: [
          t("Open your account page to copy your personal M3U playlist and guide links."),
          tx("Paste the playlist link into your IPTV app (served from {address}).", {
            address: inlineCode(address),
          }),
          t("Paste the guide link into the EPG field."),
        ],
        accountLink: true,
      };
    case "xtream":
      return {
        clients: t("TiviMate, UHF, IPTV Smarters, iMPlayer"),
        steps: [
          t(
            "Create an Xtream login below (or on your account page) to get a username and password.",
          ),
          tx("In your IPTV app, choose Add Xtream Codes and enter {address}.", {
            address: inlineCode(address),
          }),
          t("Enter that Xtream username and password."),
        ],
        accountLink: true,
      };
    default:
      return {
        clients:
          info.descriptor.certifiedApps?.map((a) => a.name).join(", ") || info.descriptor.name,
        steps: [
          t("Open the app on your device."),
          tx("Enter {address}.", { address: inlineCode(address) }),
          t("Sign in with your Ether viewer account."),
        ],
      };
  }
}

export function ConnectDeviceCard({
  info,
  configuredRemote,
  publishedAddresses,
  preferredPort,
  serverName,
}: {
  info: HubFrontendInfo;
  configuredRemote?: string;
  publishedAddresses?: string[];
  preferredPort?: string;
  serverName: string;
}) {
  const id = info.descriptor.id;
  const conflict = portConflictInfo(info.status);
  const effectivePublished = publishedAddresses ?? info.status.publishedAddresses ?? [];
  const { primary } = classifyAppAddresses(info.status, {
    preferredPort: String(
      conflict?.actualPort ?? preferredPort ?? info.descriptor.defaultPort ?? "",
    ),
    configuredRemote,
    publishedAddresses: effectivePublished,
  });
  const shareAddress = effectivePublished[0] ?? primary;
  const displayAddress =
    shareAddress ||
    (info.descriptor.defaultPort
      ? `http://<server-ip>:${info.descriptor.defaultPort}`
      : `http://<server-ip>:32400/${id === "plex" ? "" : id}`);
  const showQR = info.enabled && Boolean(shareAddress) && PHONE_QR_APPS.has(id);
  const [qr, setQR] = useState<string>();

  useEffect(() => {
    if (!showQR || !shareAddress) {
      setQR(undefined);
      return;
    }
    const controller = new AbortController();
    let objectURL: string | undefined;
    void apiBlob(
      `${API_BASE}/frontends/qr?url=${encodeURIComponent(shareAddress)}`,
      controller.signal,
    )
      .then((blob) => {
        if (!controller.signal.aborted) {
          objectURL = URL.createObjectURL(blob);
          setQR(objectURL);
        }
      })
      .catch(() => setQR(undefined));
    return () => {
      controller.abort();
      if (objectURL) URL.revokeObjectURL(objectURL);
      setQR(undefined);
    };
  }, [showQR, shareAddress]);

  const guide = connectInstructions(info, displayAddress, serverName || "Ether");

  return (
    <SettingsSection title={t("Connect a device")} description={guide.clients}>
      <div className="flex flex-col justify-between gap-5 px-5 py-4 sm:flex-row sm:items-start">
        <div className="min-w-0 flex-1 space-y-3">
          <ol className="list-decimal space-y-2 pl-5 text-sm text-fg-muted">
            {guide.steps.map((step, index) => (
              <li key={index} className="leading-relaxed">
                {step}
              </li>
            ))}
          </ol>
          {guide.accountLink ? (
            <div className="pt-1">
              <Button asChild variant="outline" size="sm">
                <Link to="/me">{t("Open your account")}</Link>
              </Button>
            </div>
          ) : null}
        </div>
        {qr ? (
          <div className="flex shrink-0 flex-col items-center gap-1.5 rounded-xl border border-border bg-white p-2.5">
            <img
              src={qr}
              alt={t("QR code for {address}", { address: displayAddress })}
              width={136}
              height={136}
              className="rounded"
            />
            <span className="text-[11px] font-medium text-[#525866]">{t("Scan on phone")}</span>
          </div>
        ) : null}
      </div>
    </SettingsSection>
  );
}

/** Reloads /frontends after saving Plex's main listener port and follows port changes. */
export async function saveMainListenerPort(
  nextPort: number,
  currentPort: number,
  reloadFrontends: () => Promise<unknown>,
): Promise<void> {
  await apiFetch("/frontends/plex/settings", {
    method: "PUT",
    body: JSON.stringify({ port: nextPort }),
  });
  await reloadFrontends();
  if (typeof window === "undefined") return;
  const target = new URL(window.location.href);
  const browserPort = Number(target.port || (target.protocol === "https:" ? 443 : 80));
  if (browserPort !== currentPort || nextPort === currentPort) return;
  target.port = String(nextPort);
  await new Promise((resolve) => window.setTimeout(resolve, 1500));
  const probe = new URL("/identity", target);
  for (let attempt = 0; attempt < 60; attempt++) {
    try {
      await fetch(probe, {
        mode: "no-cors",
        credentials: "omit",
        cache: "no-store",
        signal: AbortSignal.timeout(1500),
      });
      window.location.assign(target.href);
      return;
    } catch (err) {
      if (attempt === 59) throw err;
      await new Promise((resolve) => window.setTimeout(resolve, 1000));
    }
  }
}
