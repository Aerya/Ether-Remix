import { Button, Select, toast } from "@braedonsaunders/appkit-ui";
import { ListOrdered } from "lucide-react";
import { useState } from "react";
import { Drawer } from "../../../components/Drawer";
import { Field } from "../../../components/fields";
import { getLocale, t } from "../../../i18n";
import { apiFetch } from "../../../lib/api";
import {
  builtinMetadataSources,
  type EpisodeOrder,
  type EpisodeOrderDetails,
  type EpisodeOrderProvenance,
} from "../../../lib/metadataSources";

function orderLabel(order: EpisodeOrder) {
  if (order.source === "tmdb") {
    if (order.kind === "aired") return t("TMDB aired order");
    if (order.kind.startsWith("group:")) {
      return order.label
        ? t("TMDB episode group ({group})", { group: order.label })
        : t("TMDB episode group");
    }
  }
  if (order.source === "tvdb") {
    if (order.kind === "aired" || order.kind === "official") return t("TheTVDB official (aired)");
    if (order.kind === "dvd") return t("TheTVDB DVD");
    if (order.kind === "absolute") return t("TheTVDB absolute");
  }
  return order.label ?? order.source;
}

function sourceLabel(order: EpisodeOrder) {
  if (order.source === "tmdb" || order.source === "tvdb") {
    return builtinMetadataSources[order.source].name;
  }
  return order.label ?? order.source;
}

function libraryReason(details: EpisodeOrderProvenance) {
  const order = orderLabel(details.libraryDefault);
  if (!order) return t("No source configured");
  return details.libraryName
    ? t("{order}, from the {library} library", { order, library: details.libraryName })
    : t("Library default ({source})", { source: order });
}

export function episodeOrderReason(details: EpisodeOrderProvenance) {
  return details.override
    ? t("{order}, set for this show", { order: orderLabel(details.effective) })
    : libraryReason(details);
}

function orderKey(order: EpisodeOrder | null | undefined) {
  return order ? JSON.stringify([order.source, order.kind]) : "";
}

export function TitleEpisodeOrder({
  details,
  titleKey,
  open,
  onOpenChange,
  onSaved,
}: {
  details: EpisodeOrderDetails;
  titleKey: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved: () => Promise<void>;
}) {
  const [selection, setSelection] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const current = orderKey(details.override);
  const defaultSource = sourceLabel(details.libraryDefault);
  const reason = episodeOrderReason(details);
  const value = selection ?? current;
  const chosen = details.options.find((option) => orderKey(option) === value);
  const gap = details.gap && details.gap.count > 0 ? details.gap : null;

  function close() {
    setSelection(null);
    setError(false);
    onOpenChange(false);
  }

  async function save(order: EpisodeOrder | null) {
    setBusy(true);
    setError(false);
    try {
      await apiFetch(`/catalog/title/${encodeURIComponent(titleKey)}/episode-order`, {
        method: "PUT",
        body: JSON.stringify({ order: order ? { source: order.source, kind: order.kind } : null }),
      });
      await onSaved();
      close();
      toast.success(t("Episode order saved"));
    } catch {
      setError(true);
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-2 border-b border-border px-5 py-3 text-sm">
      <p className="flex items-center gap-2 text-fg-muted">
        <ListOrdered size={16} className="shrink-0" aria-hidden />
        {reason}
      </p>
      {gap ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border bg-surface px-3 py-2">
          <p>
            {t("{source} lists more episodes ({count}). Use this order for this show?", {
              source: sourceLabel(gap.order),
              count: new Intl.NumberFormat(getLocale()).format(gap.count),
            })}
          </p>
          <Button variant="outline" size="sm" disabled={busy} onClick={() => void save(gap.order)}>
            {t("Use this order")}
          </Button>
        </div>
      ) : null}
      {error && !open ? (
        <p role="alert" className="text-danger">
          {t("Could not save the episode order")}
        </p>
      ) : null}
      {open ? (
        <Drawer
          open
          title={t("Episode order")}
          description={reason}
          onClose={busy ? () => {} : close}
          footer={
            <div className="flex w-full justify-end gap-2">
              <Button variant="ghost" disabled={busy} onClick={close}>
                {t("Cancel")}
              </Button>
              <Button
                disabled={busy || value === current || Boolean(value && !chosen)}
                onClick={() => void save(chosen ?? null)}
              >
                {t("Save")}
              </Button>
            </div>
          }
        >
          <div className="space-y-3">
            <Field
              label={t("Episode order")}
              hint={details.override ? libraryReason(details) : undefined}
            >
              <Select
                searchable={false}
                sheetTitle={t("Episode order")}
                aria-label={t("Episode order")}
                value={value}
                disabled={busy}
                onChange={(event) => setSelection(event.currentTarget.value)}
              >
                <option value="">
                  {defaultSource
                    ? t("Library default ({source})", { source: defaultSource })
                    : t("No source configured")}
                </option>
                {details.override &&
                !details.options.some((option) => orderKey(option) === current) ? (
                  <option value={current} disabled>
                    {t("{source} (unavailable)", { source: orderLabel(details.override) })}
                  </option>
                ) : null}
                {details.options.map((option) => (
                  <option key={orderKey(option)} value={orderKey(option)}>
                    {orderLabel(option)}
                  </option>
                ))}
              </Select>
            </Field>
            {error ? (
              <p role="alert" className="text-danger">
                {t("Could not save the episode order")}
              </p>
            ) : null}
          </div>
        </Drawer>
      ) : null}
    </div>
  );
}
