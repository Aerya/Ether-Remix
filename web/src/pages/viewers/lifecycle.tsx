import { Badge } from "@braedonsaunders/appkit-ui";
import { localized, t } from "../../i18n";
import type { Viewer, ViewerLifecycle, ViewerSource } from "../../lib/contract";
import { viewerStatus } from "./ViewerStatus";

/**
 * The viewer's lifecycle, from the API when present. Older rows without one
 * are described from the fields the list has always carried.
 */
export function lifecycleOf(viewer: Viewer): ViewerLifecycle {
  if (viewer.lifecycle) return viewer.lifecycle;
  if (viewer.isOwner) {
    return {
      source: "owner",
      paused: false,
      managedBy: "",
      canPause: false,
      canRemove: false,
      canMerge: false,
    };
  }
  const fromPlex = viewer.plexId > 0;
  return {
    source: fromPlex ? "plex" : "local",
    paused: !fromPlex && viewerStatus(viewer) === "inactive",
    plexShared: fromPlex ? viewerStatus(viewer) === "active" : undefined,
    managedBy: fromPlex ? "plex" : "",
    canPause: true,
    canRemove: !fromPlex,
    canMerge: !fromPlex,
  };
}

export function isPlexSource(source: ViewerSource): boolean {
  return source === "plex" || source === "plexHome";
}

const SOURCE_LABELS: Record<ViewerSource, string> = localized(() => ({
  owner: t("Owner"),
  local: t("Local"),
  invite: t("Invite"),
  plex: t("Plex"),
  plexHome: t("Plex Home"),
  dlnaGuest: t("Living-room guest"),
}));

function sourceLabel(source: ViewerSource): string {
  return SOURCE_LABELS[source];
}

/** True when the viewer can watch right now. */
export function isActiveViewer(viewer: Viewer): boolean {
  const life = lifecycleOf(viewer);
  if (life.paused) return false;
  if (isPlexSource(life.source) && life.plexShared === false) return false;
  return ["owner", "active"].includes(viewerStatus(viewer));
}

/**
 * Status and source badges for one person. Plex accounts distinguish the
 * owner's pause ("Paused by owner") from plex.tv share status
 * ("Not shared on plex.tv" / "Managed on plex.tv").
 */
export function LifecycleBadges({
  viewer,
  plexConnected,
}: {
  viewer: Viewer;
  plexConnected: boolean;
}) {
  const life = lifecycleOf(viewer);
  if (viewer.isOwner) {
    return <Badge variant="default">{t("Owner")}</Badge>;
  }
  const badges: {
    label: string;
    variant: "success" | "warning" | "default" | "secondary" | "outline";
  }[] = [];
  if (isPlexSource(life.source) && plexConnected) {
    if (life.paused) {
      badges.push({ label: t("Paused by owner"), variant: "warning" });
    }
    if (life.plexShared === false) {
      badges.push({ label: t("Not shared on plex.tv"), variant: "outline" });
    } else {
      badges.push({ label: t("Managed on plex.tv"), variant: "secondary" });
    }
  } else {
    if (viewerStatus(viewer) === "imported") {
      badges.push({ label: t("Imported history"), variant: "default" });
    } else if (life.paused) {
      badges.push({ label: t("Paused"), variant: "warning" });
    } else if (isActiveViewer(viewer)) {
      badges.push({ label: t("Active"), variant: "success" });
    } else if (life.source !== "dlnaGuest") {
      badges.push({ label: t("No current access"), variant: "default" });
    }
  }
  if (life.source !== "owner" && (plexConnected || !isPlexSource(life.source))) {
    badges.push({ label: sourceLabel(life.source), variant: "secondary" });
  }
  return (
    <>
      {badges.map((b) => (
        <Badge key={b.label} variant={b.variant}>
          {b.label}
        </Badge>
      ))}
    </>
  );
}
