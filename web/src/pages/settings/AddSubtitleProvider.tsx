import { Link2, Plug } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { AddAddonDrawer } from "../../components/addons/AddAddonDrawer";
import { Drawer } from "../../components/Drawer";
import { t } from "../../i18n";

/**
 * Add a subtitle provider beyond the built-in accounts: a Stremio add-on by
 * its install link, or a plugin. Either one then shows as a card in the
 * Subtitles group of Integrations (the add-on drawer refreshes the registry).
 */
export function AddSubtitleProvider({ onClose }: { onClose: () => void }) {
  const [addon, setAddon] = useState(false);
  if (addon)
    return (
      <AddAddonDrawer
        open
        role="subtitles"
        title={t("Add a Stremio add-on")}
        submitLabel={t("Add add-on")}
        onClose={onClose}
        onAdded={() => {}}
      />
    );
  return (
    <Drawer open onClose={onClose} title={t("Add subtitle provider")}>
      <div className="flex flex-col gap-3">
        <button
          type="button"
          className="flex items-center gap-4 rounded-xl border border-border p-4 text-left hover:bg-surface-hover"
          onClick={() => setAddon(true)}
        >
          <Link2 size={22} />
          <span>
            <span className="block font-medium">{t("Stremio add-on")}</span>
            <span className="block text-sm text-fg-muted">
              {t("Paste a manifest link to add its subtitles.")}
            </span>
          </span>
        </button>
        <Link
          className="flex items-center gap-4 rounded-xl border border-border p-4 text-left hover:bg-surface-hover"
          to="/plugins/marketplace"
          onClick={onClose}
        >
          <Plug size={22} />
          <span>
            <span className="block font-medium">{t("Plugin")}</span>
            <span className="block text-sm text-fg-muted">
              {t("Install from the plugin directory or build one in Studio.")}
            </span>
          </span>
        </Link>
      </div>
    </Drawer>
  );
}
