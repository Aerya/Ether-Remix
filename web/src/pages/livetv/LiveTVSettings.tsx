import { Button, Spinner, confirmDialog, toast } from "@braedonsaunders/appkit-ui";
import { useId, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, MutationError } from "../../components/feedback";
import { useUnsavedGuard } from "../../components/SaveBar";
import { t, tn } from "../../i18n";
import { api } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import type { EtherConfig } from "../../lib/types";

const DEFAULT_MINUTES = 60;
const MAX_MINUTES = 240;
type LiveTVConfig = NonNullable<EtherConfig["liveTV"]>;

export function LiveTVSettings({ onClose }: { onClose: () => void }) {
  const sliderId = useId();
  const helperId = useId();
  const config = useAsync(() => api.getSection<LiveTVConfig>("livetv"), []);
  const saved = config.data?.value?.timeshiftMinutes ?? DEFAULT_MINUTES;
  const [draft, setDraft] = useState<number | null>(null);
  const minutes = draft ?? saved;
  const dirty = minutes !== saved;
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  useUnsavedGuard(dirty);

  async function close() {
    if (saving) return;
    if (
      dirty &&
      !(await confirmDialog({
        title: t("Discard unsaved changes?"),
        message: t("Your changes to the rewind limit haven't been saved."),
        confirmLabel: t("Discard"),
        tone: "danger",
      }))
    )
      return;
    onClose();
  }

  async function save() {
    setSaving(true);
    setError(null);
    try {
      const response = await api.putSection("livetv", { timeshiftMinutes: minutes });
      config.setData({ ...response, value: response.value as LiveTVConfig });
      setDraft(null);
      toast.success(t("Settings saved"));
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Drawer
      open
      onClose={() => void close()}
      title={t("Live TV settings")}
      documentTitle={t("Live TV settings")}
      size="md"
      footer={
        config.data ? (
          <Button
            className="ml-auto"
            size="sm"
            disabled={!dirty || saving}
            onClick={() => void save()}
          >
            {saving ? t("Saving…") : t("Save changes")}
          </Button>
        ) : undefined
      }
    >
      {config.error ? (
        <ErrorPanel error={config.error} onRetry={() => void config.reload()} />
      ) : !config.data ? (
        <Spinner size={20} label={t("Loading settings")} />
      ) : (
        <div className="space-y-3 rounded-xl border border-border bg-surface px-4 py-4">
          <label htmlFor={sliderId} className="block text-sm font-medium text-fg">
            {tn(
              minutes,
              "Rewind live TV up to {count} minute",
              "Rewind live TV up to {count} minutes",
            )}
          </label>
          <input
            id={sliderId}
            type="range"
            min={0}
            max={MAX_MINUTES}
            value={minutes}
            disabled={saving}
            aria-describedby={helperId}
            className="block h-6 w-full accent-primary"
            onChange={(e) => setDraft(e.currentTarget.valueAsNumber)}
          />
          <p id={helperId} className="text-sm text-fg-muted">
            {t(
              "Uses about 3 GB of disk space per viewer-hour at 6–8 Mbps. Set to 0 for live playback only.",
            )}
          </p>
          {error ? <MutationError error={error} /> : null}
        </div>
      )}
    </Drawer>
  );
}
