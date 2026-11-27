import { confirmDialog } from "@braedonsaunders/appkit-ui";
import { t } from "../../i18n";
import { ApiError } from "../../lib/api";

export async function confirmLibraryRebuild(error: unknown): Promise<string | undefined> {
  if (!(error instanceof ApiError) || error.code !== "library_levels_changed") return;
  const confirmation = (error.body as { error?: { confirmation?: unknown } } | undefined)?.error
    ?.confirmation;
  if (typeof confirmation !== "string" || !confirmation) return;
  const accepted = await confirmDialog({
    title: t("Rebuild the changed library?"),
    message: t(
      "On the next sync, Ether clears the affected libraries’ items and cached item details, then rebuilds them from the plugin. Watch progress, favourites, and collection membership are kept for items the plugin brings back with the same ID.",
    ),
    confirmLabel: t("Rebuild and install"),
    cancelLabel: t("Cancel"),
    tone: "danger",
  });
  if (accepted) return confirmation;
}
