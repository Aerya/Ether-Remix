import { Badge, Button, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { Gauge, Globe, Server } from "lucide-react";
import { useMemo, useState } from "react";
import { useApp } from "../../app-context";
import { NumberInput } from "../../components/fields";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { SettingsRow } from "../../components/SettingsRow";
import { t, tn } from "../../i18n";
import { api } from "../../lib/api";
import { fixed } from "../../lib/format";
import { useAsync, useInterval } from "../../lib/hooks";
import type { UploadBudgetStatus } from "../../lib/types";

type Draft = {
  capMbps: number;
  uploadBudgetMbps: number;
};

function formatMbps(kbps: number): string {
  const mbps = kbps / 1000;
  if (mbps >= 10) return String(Math.round(mbps));
  return fixed(mbps, 1);
}

function formatMbpsPrecise(kbps: number): string {
  const mbps = kbps / 1000;
  return mbps >= 100 ? String(Math.round(mbps)) : fixed(mbps, 1);
}

function UploadBudgetPanel({ status }: { status: UploadBudgetStatus }) {
  const effective = Math.max(0, status.effectiveKbps);
  const usable = Math.max(0, status.usableKbps);
  const etherKbps = Math.max(0, status.etherRemoteKbps);
  const externalKbps = Math.max(0, status.otherPmsKbps);
  const totalKbps = Math.max(0, status.totalUsedKbps || etherKbps + externalKbps);
  const servers = (status.otherServers ?? []).filter(
    (s) => s.bandwidthKbps > 0 || s.remoteStreams > 0,
  );

  // Bar scale: effective upload capacity when known; otherwise a graceful
  // reference ceiling so active remote traffic still renders proportionally.
  const barMax = effective > 0 ? Math.max(effective, totalKbps) : Math.max(totalKbps, 20_000);
  const etherPct = barMax > 0 ? Math.min(100, (etherKbps / barMax) * 100) : 0;
  const externalPct = barMax > 0 ? Math.min(100 - etherPct, (externalKbps / barMax) * 100) : 0;
  const targetPct = effective > 0 && usable > 0 ? Math.min(100, (usable / barMax) * 100) : null;
  const overTarget = usable > 0 && totalKbps > usable;

  const headline =
    status.source === "override"
      ? t("Upload speed set to {rate} Mbps", { rate: formatMbps(status.overrideKbps) })
      : status.source === "measured"
        ? t("Ether measured about {rate} Mbps", { rate: formatMbps(status.measuredKbps) })
        : t("Learning your upload speed");

  const modeBadge =
    status.source === "override"
      ? { label: t("Manual"), variant: "info" as const }
      : status.source === "measured"
        ? { label: t("Automatic"), variant: "success" as const }
        : { label: t("Automatic"), variant: "secondary" as const };

  return (
    <div data-testid="upload-budget-panel" className="space-y-3 bg-surface-raised/30 px-5 py-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-2">
            <Gauge size={15} className="shrink-0 text-primary" aria-hidden />
            <p className="text-sm font-semibold text-fg">{headline}</p>
            <Badge variant={modeBadge.variant}>{modeBadge.label}</Badge>
          </div>
          {status.source === "learning" ? (
            <p className="text-xs text-fg-muted">
              {t(
                "No upload budget is applied yet. Ether estimates your upload speed passively from away-from-home streams.",
              )}
            </p>
          ) : status.source === "override" && status.measuredKbps > 0 ? (
            <p className="text-xs text-fg-muted">
              {t("Ether measured about {rate} Mbps from remote streams.", {
                rate: formatMbps(status.measuredKbps),
              })}
            </p>
          ) : (
            <p className="text-xs text-fg-muted">
              {t("Keeps combined away-from-home streams within {rate} Mbps (85% of upload).", {
                rate: formatMbpsPrecise(usable),
              })}
            </p>
          )}
        </div>

        <div className="shrink-0 text-right">
          <p className="font-mono text-sm font-semibold tabular-nums text-fg">
            {usable > 0
              ? t("{used} / {budget} Mbps", {
                  used: formatMbpsPrecise(totalKbps),
                  budget: formatMbpsPrecise(usable),
                })
              : t("{used} Mbps", { used: formatMbpsPrecise(totalKbps) })}
          </p>
          <p className="text-[11px] text-fg-muted">
            {totalKbps > 0 ? t("Remote upload in use") : t("No remote streams")}
          </p>
        </div>
      </div>

      <div className="space-y-2">
        <div
          role="meter"
          aria-label={t("Remote upload usage")}
          aria-valuemin={0}
          aria-valuemax={Math.round(barMax / 1000)}
          aria-valuenow={Math.round((totalKbps / 1000) * 10) / 10}
          className="relative h-2.5 w-full overflow-hidden rounded-full bg-surface-subtle ring-1 ring-inset ring-border/70"
        >
          <div className="flex h-full w-full">
            {etherPct > 0 ? (
              <div
                className={
                  overTarget
                    ? "h-full bg-warning transition-[width] duration-300"
                    : "h-full bg-primary transition-[width] duration-300"
                }
                style={{ width: `${etherPct}%` }}
              />
            ) : null}
            {externalPct > 0 ? (
              <div
                className="h-full bg-info transition-[width] duration-300"
                style={{ width: `${externalPct}%` }}
              />
            ) : null}
          </div>
          {targetPct !== null ? (
            <span
              aria-hidden
              title={t("85% budget target ({rate} Mbps)", { rate: formatMbpsPrecise(usable) })}
              className="pointer-events-none absolute inset-y-0 w-0.5 bg-fg/45"
              style={{ left: `${targetPct}%` }}
            />
          ) : null}
        </div>

        <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-1.5 text-xs text-fg-muted">
          <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <span className="inline-flex items-center gap-1.5">
              <span
                className={
                  overTarget
                    ? "size-2 shrink-0 rounded-full bg-warning"
                    : "size-2 shrink-0 rounded-full bg-primary"
                }
                aria-hidden
              />
              <Globe size={12} className="shrink-0 text-fg-subtle" aria-hidden />
              <span>
                {status.etherRemoteStreams > 0
                  ? t("Ether: {rate} Mbps ({streams})", {
                      rate: formatMbpsPrecise(etherKbps),
                      streams: tn(status.etherRemoteStreams, "{count} stream", "{count} streams"),
                    })
                  : t("Ether: idle")}
              </span>
            </span>

            {servers.map((srv, idx) => (
              <span
                key={srv.machineId || `${srv.name}-${idx}`}
                className="inline-flex items-center gap-1.5"
              >
                <span className="size-2 shrink-0 rounded-full bg-info" aria-hidden />
                <Server size={12} className="shrink-0 text-fg-subtle" aria-hidden />
                <span>
                  {t("{name}: {rate} Mbps ({streams})", {
                    name: srv.name || t("Plex Media Server"),
                    rate: formatMbpsPrecise(srv.bandwidthKbps),
                    streams: tn(srv.remoteStreams, "{count} stream", "{count} streams"),
                  })}
                </span>
              </span>
            ))}
          </div>

          {status.budgetCappedStreams > 0 ? (
            <span className="font-medium text-fg">
              {tn(
                status.budgetCappedStreams,
                "{count} stream capped by budget",
                "{count} streams capped by budget",
              )}
            </span>
          ) : null}
        </div>
      </div>
    </div>
  );
}

/**
 * Away-from-home bandwidth controls: the automatic shared upload budget
 * (with optional owner override) alongside the per-stream bitrate limit.
 */
export function RemoteBitrateSection() {
  const { refreshDashboard } = useApp();
  const config = useAsync(() => api.getConfig(), []);
  const budget = useAsync((signal) => api.uploadBudgetStatus(signal), []);
  useInterval(() => void budget.reload(), 5_000);

  const kbps = config.data?.transcode?.remoteMaxBitrateKbps;
  const uploadKbps = config.data?.transcode?.remoteUploadBudgetKbps;
  const saved = useMemo<Draft | null>(
    () =>
      kbps === undefined
        ? null
        : {
            capMbps: Math.round((kbps ?? 0) / 100) / 10,
            uploadBudgetMbps: Math.round((uploadKbps ?? 0) / 100) / 10,
          },
    [kbps, uploadKbps],
  );
  const { draft, setDraft, dirty, reset } = useDraft(saved);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);

  if (!draft || !saved) return null;

  async function submit() {
    if (!draft || !saved) return;
    setSaving(true);
    setError(null);
    try {
      const patch: Record<string, number> = {};
      if (draft.capMbps !== saved.capMbps) {
        patch.remoteMaxBitrateKbps = Math.round(Math.max(0, draft.capMbps) * 1000);
      }
      if (draft.uploadBudgetMbps !== saved.uploadBudgetMbps) {
        patch.remoteUploadBudgetKbps = Math.round(Math.max(0, draft.uploadBudgetMbps) * 1000);
      }
      if (Object.keys(patch).length > 0) {
        await api.putSection("transcode", patch);
      }
      toast.success(t("Settings saved"), { description: t("Away from home") });
      await Promise.all([config.reload(), budget.reload()]);
      void refreshDashboard();
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }

  return (
    <SettingsSection
      title={t("Away from home")}
      description={t(
        "Shares your upload capacity across remote streams and caps individual streams. Home streams are never affected.",
      )}
    >
      {budget.data ? <UploadBudgetPanel status={budget.data} /> : null}

      <SettingsRow
        title={
          <label htmlFor="away-upload-budget" data-setting="upload-budget">
            {t("Internet upload speed")}
          </label>
        }
        description={t(
          "Optional override for your total upload speed. Leave at 0 to let Ether estimate it automatically.",
        )}
        control={
          <span className="flex items-center gap-2">
            {draft.uploadBudgetMbps > 0 ? (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setDraft({ ...draft, uploadBudgetMbps: 0 })}
              >
                {t("Use automatic")}
              </Button>
            ) : null}
            <NumberInput
              id="away-upload-budget"
              className="w-24"
              min={0}
              step={1}
              value={draft.uploadBudgetMbps}
              onChange={(uploadBudgetMbps) => setDraft({ ...draft, uploadBudgetMbps })}
            />
            <span className="text-sm text-fg-muted">{t("Mbps")}</span>
          </span>
        }
      />

      <SettingsRow
        title={
          <label htmlFor="away-bitrate-cap" data-setting="bitrate-limit">
            {t("Per-stream bitrate limit")}
          </label>
        }
        description={t("Maximum bitrate for any single remote stream. 0 means no limit.")}
        control={
          <span className="flex items-center gap-2">
            <NumberInput
              id="away-bitrate-cap"
              className="w-24"
              min={0}
              step={0.5}
              value={draft.capMbps}
              onChange={(capMbps) => setDraft({ ...draft, capMbps })}
            />
            <span className="text-sm text-fg-muted">{t("Mbps")}</span>
          </span>
        }
      />

      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onSave={submit}
        onDiscard={() => {
          reset();
          setError(null);
        }}
      />
    </SettingsSection>
  );
}
