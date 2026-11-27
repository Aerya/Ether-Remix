import { toast } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { normalizeRules, StreamRulesFields, type StreamRules } from "../viewers/shared";

/**
 * Audio and subtitle choices for every viewer, unless a profile sets its own.
 * Saved to the household record (`/stream-preferences/0`). Playback shows the
 * audio rows and the subtitle rows on their own tabs; the draft is one, so
 * edits on both save together.
 */
export function HouseholdAudio({
  part,
  hidden = false,
}: {
  part: "audio" | "subtitles";
  /** Keeps the draft and its save bar while another sub-tab is showing. */
  hidden?: boolean;
}) {
  const rules = useAsync(
    async (signal) =>
      normalizeRules(await apiFetch<StreamRules>("/stream-preferences/0", { signal })),
    [],
  );
  const { draft, setDraft, dirty, reset } = useDraft<StreamRules>(rules.data);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const save = async () => {
    if (!draft) return;
    setSaving(true);
    setError(null);
    try {
      await apiFetch("/stream-preferences/0", { method: "PUT", body: JSON.stringify(draft) });
      rules.setData(draft);
      toast.success(t("Audio and subtitles saved"), {
        description: t("Every viewer uses these unless their profile sets its own."),
      });
    } catch (e) {
      setError(e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      {hidden ? null : rules.error ? (
        <ErrorPanel error={rules.error} onRetry={() => void rules.reload()} />
      ) : !draft ? (
        <LoadingRows rows={4} />
      ) : (
        <>
          <p className="max-w-3xl text-sm text-fg-muted">
            {t("Every viewer uses these unless their profile sets its own.")}
          </p>
          <StreamRulesFields value={draft} onChange={setDraft} disabled={saving} part={part} />
        </>
      )}
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onSave={save}
        onDiscard={() => {
          reset();
          setError(null);
        }}
      />
    </>
  );
}
