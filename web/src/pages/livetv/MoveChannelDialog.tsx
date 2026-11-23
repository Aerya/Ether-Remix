import { Button, Dialog, Input } from "@braedonsaunders/appkit-ui";
import { ArrowRight } from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { t, tn } from "../../i18n";
import { formatNumber } from "../../lib/format";
import type { ChannelSummary } from "./model";
import { computeReorderPlan, type ReorderPlan } from "./reorder";

const PREVIEW_LIMIT = 6;

export function MoveChannelDialog({
  channel,
  lineup,
  busy,
  onClose,
  onApply,
}: {
  channel: ChannelSummary | null;
  lineup: readonly ChannelSummary[];
  busy: boolean;
  onClose: () => void;
  onApply: (plan: ReorderPlan) => Promise<boolean>;
}) {
  const inputId = useId();
  const currentIndex = useMemo(
    () => (channel ? lineup.findIndex((c) => c.id === channel.id) : -1),
    [channel, lineup],
  );
  const currentPosition = currentIndex >= 0 ? currentIndex + 1 : 1;
  const total = lineup.length;
  const [rawPos, setRawPos] = useState(String(currentPosition));

  useEffect(() => {
    if (channel && currentIndex >= 0) {
      setRawPos(String(currentIndex + 1));
    }
  }, [channel, currentIndex]);

  const parsedPos = useMemo(() => {
    const trimmed = rawPos.trim();
    if (!/^\d+$/.test(trimmed)) return null;
    const n = Number.parseInt(trimmed, 10);
    return n >= 1 && n <= total ? n : null;
  }, [rawPos, total]);

  const plan = useMemo(() => {
    if (!channel || parsedPos === null) return null;
    return computeReorderPlan(lineup, channel.id, parsedPos - 1);
  }, [channel, lineup, parsedPos]);

  const invalid = rawPos.trim() !== "" && parsedPos === null;

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!plan || busy) return;
    const ok = await onApply(plan);
    if (ok) onClose();
  };

  const shiftedPreview = plan ? plan.shifted.slice(0, PREVIEW_LIMIT) : [];
  const remainingShifted = plan ? Math.max(0, plan.shifted.length - PREVIEW_LIMIT) : 0;
  const lastShifted =
    plan && plan.shifted.length > 0 ? plan.shifted[plan.shifted.length - 1] : null;

  return (
    <Dialog
      open={channel !== null}
      onClose={onClose}
      size="sm"
      title={channel ? t("Move {name}", { name: channel.name }) : ""}
      description={
        channel
          ? t("Currently position {position} of {total} (#{number}).", {
              position: formatNumber(currentPosition),
              total: formatNumber(total),
              number: channel.number,
            })
          : undefined
      }
      footer={
        <>
          <Button type="button" variant="outline" onClick={onClose} disabled={busy}>
            {t("Cancel")}
          </Button>
          <Button type="submit" form="move-channel-position-form" disabled={!plan || busy}>
            {t("Move channel")}
          </Button>
        </>
      }
    >
      {channel ? (
        <form
          id="move-channel-position-form"
          onSubmit={(e) => void submit(e)}
          className="space-y-4"
        >
          <div className="space-y-1.5">
            <div className="flex items-center justify-between gap-2">
              <label htmlFor={inputId} className="text-xs font-medium text-fg">
                {t("New position (1–{total})", { total: formatNumber(total) })}
              </label>
              <div className="flex items-center gap-1">
                <button
                  type="button"
                  disabled={busy || currentPosition === 1}
                  onClick={() => setRawPos("1")}
                  className="rounded px-1.5 py-0.5 text-[11px] font-medium text-fg-muted transition-colors hover:bg-surface-hover hover:text-fg disabled:opacity-40"
                >
                  {t("First")}
                </button>
                <button
                  type="button"
                  disabled={busy || currentPosition === total}
                  onClick={() => setRawPos(String(total))}
                  className="rounded px-1.5 py-0.5 text-[11px] font-medium text-fg-muted transition-colors hover:bg-surface-hover hover:text-fg disabled:opacity-40"
                >
                  {t("Last")}
                </button>
              </div>
            </div>
            <Input
              id={inputId}
              type="number"
              inputMode="numeric"
              min={1}
              max={total}
              value={rawPos}
              onChange={(e) => setRawPos(e.target.value)}
              autoFocus
            />
            {invalid ? (
              <p className="text-xs text-danger">
                {t("Enter a number from 1 to {total}.", { total: formatNumber(total) })}
              </p>
            ) : null}
          </div>

          {/* Live renumbering preview */}
          <div className="rounded-lg border border-border bg-bg-subtle p-3 text-xs">
            {plan ? (
              <div className="space-y-2">
                <div className="flex items-center justify-between gap-2">
                  <span className="min-w-0 truncate font-medium text-fg">{plan.channelName}</span>
                  <span className="inline-flex shrink-0 items-center gap-1 font-mono tabular-nums">
                    <span className="text-fg-subtle line-through">{plan.fromNumber}</span>
                    <ArrowRight size={11} className="text-primary" aria-hidden />
                    <span className="rounded bg-primary-subtle px-1.5 py-0.5 font-semibold text-primary">
                      {plan.toNumber}
                    </span>
                  </span>
                </div>
                {plan.shifted.length === 0 ? (
                  <p className="text-fg-muted">
                    {t("Fits in the gap — no other channels change.")}
                  </p>
                ) : (
                  <div className="space-y-1.5 border-t border-border pt-2">
                    <p className="text-fg-muted">
                      {tn(
                        plan.shifted.length,
                        "{count} following channel will shift:",
                        "{count} following channels will shift:",
                      )}
                    </p>
                    <ul className="space-y-1">
                      {shiftedPreview.map((ch) => (
                        <li key={ch.id} className="flex items-center justify-between gap-2">
                          <span className="min-w-0 truncate text-fg-muted">{ch.name}</span>
                          <span className="inline-flex shrink-0 items-center gap-1 font-mono tabular-nums">
                            <span className="text-fg-subtle line-through">{ch.fromNumber}</span>
                            <ArrowRight size={10} className="text-fg-subtle" aria-hidden />
                            <span className="font-medium text-fg">{ch.toNumber}</span>
                          </span>
                        </li>
                      ))}
                    </ul>
                    {remainingShifted > 0 && lastShifted ? (
                      <p className="text-fg-subtle">
                        {t("and {count} more (through #{last})", {
                          count: formatNumber(remainingShifted),
                          last: lastShifted.toNumber,
                        })}
                      </p>
                    ) : null}
                  </div>
                )}
              </div>
            ) : (
              <p className="text-fg-muted">
                {t("Choose a different position to preview the new channel numbers.")}
              </p>
            )}
          </div>
        </form>
      ) : null}
    </Dialog>
  );
}
