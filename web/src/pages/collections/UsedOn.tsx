import { Badge, Skeleton } from "@braedonsaunders/appkit-ui";
import { ArrowRight, Plus } from "lucide-react";
import { Link } from "react-router-dom";
import { ErrorPanel } from "../../components/feedback";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import type { CollectionUse, RowPlacementMode } from "../../lib/contract";
import { useAsync, type AsyncState } from "../../lib/hooks";
import { addToPageHref, designerHref } from "../../lib/recommendations";

const PLACEMENT: Record<RowPlacementMode, () => string> = {
  pinned: () => t("Pinned"),
  show: () => t("Show"),
  auto: () => t("Auto"),
  hide: () => t("Hide"),
};

/** "#3, Fri–Sat evenings" or "Auto": how the row sits on its page. */
export function describeUse(u: CollectionUse): string {
  const where =
    u.placement === "pinned" && u.position
      ? t("Pinned at #{n}", { n: u.position })
      : (PLACEMENT[u.placement]?.() ?? u.placement);
  return u.schedule ? `${where} · ${u.schedule}` : where;
}

export function useCollectionUsage(id: string | undefined) {
  return useAsync((signal) => (id ? api.collectionUsage(id, signal) : Promise.resolve([])), [id]);
}

/**
 * "Used on": the Recommendations pages whose rows show this collection. Each
 * entry opens the designer at that row; placement is edited only there.
 */
export function UsedOn({
  collectionId,
  usage,
}: {
  collectionId?: string;
  usage: AsyncState<CollectionUse[]>;
}) {
  if (!collectionId)
    return (
      <p className="text-sm text-fg-muted">
        {t("Save the collection, then add it to Home or a library's Recommendations.")}
      </p>
    );
  if (usage.error) return <ErrorPanel error={usage.error} onRetry={() => void usage.reload()} />;
  if (!usage.data)
    return (
      <div role="status" aria-label={t("Loading where it's used")} className="space-y-2">
        <Skeleton className="h-10 w-full rounded-lg" />
        <Skeleton className="h-10 w-full rounded-lg" />
      </div>
    );
  return (
    <div className="space-y-2">
      {usage.data.length ? (
        <ul className="divide-y divide-border-subtle rounded-lg border border-border bg-surface">
          {usage.data.map((u) => (
            <li key={`${u.page}/${u.rowId}`}>
              <Link
                to={designerHref(u.page, u.rowId)}
                aria-label={t("Edit the {row} row on {page}", { row: u.rowName, page: u.pageName })}
                className="flex items-center gap-3 px-3 py-2.5 text-sm hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/60"
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium text-fg">{u.pageName}</span>
                  <span className="block truncate text-xs text-fg-muted">
                    {u.rowName} · {describeUse(u)}
                  </span>
                </span>
                {u.placement === "hide" ? <Badge variant="secondary">{t("Hidden")}</Badge> : null}
                <ArrowRight size={14} className="shrink-0 text-fg-subtle" aria-hidden />
              </Link>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-fg-muted">
          {t("Not on any page yet. Add it as a row on Home or a library's Recommendations.")}
        </p>
      )}
      <Link
        to={addToPageHref(collectionId)}
        className="inline-flex items-center gap-1.5 text-sm font-medium text-primary hover:underline"
      >
        <Plus size={14} aria-hidden /> {t("Add to a page…")}
      </Link>
    </div>
  );
}

/** One line for read-only summaries: "Home · Movies › Recommendations". */
export function usedOnSummary(uses: CollectionUse[] | undefined): string {
  if (!uses) return "…";
  if (!uses.length) return t("Not on any page");
  return [...new Set(uses.map((u) => u.pageName))].join(" · ");
}
