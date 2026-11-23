import { useEffect, useState } from "react";
import { Button, Textarea } from "@braedonsaunders/appkit-ui";
import { Download, Link } from "lucide-react";
import { Drawer } from "../../../components/Drawer";
import { Field, Segmented } from "../../../components/fields";
import { t } from "../../../i18n";
import { apiFetch, errorMessage } from "../../../lib/api";
import { friendlyMediaError } from "./mediaErrors";
import { onair, type DownloaderStatus } from "./model";

/** Shared import action placed beside uploads in Reels and Library. */
export function LinkImport({
  reelId,
  onImported,
  ensureReel,
}: {
  reelId?: string;
  ensureReel?: () => Promise<string>;
  onImported: () => void | Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [links, setLinks] = useState("");
  const [kind, setKind] = useState<"video" | "audio" | "image">("video");
  const [busyMode, setBusyMode] = useState<"link" | "import" | null>(null);
  const [installing, setInstalling] = useState(false);
  const [status, setStatus] = useState<DownloaderStatus | null>(null);
  const [error, setError] = useState("");

  const isReel = Boolean(reelId || ensureReel);
  const needsDownloader = isReel || kind === "video" || kind === "audio";
  const busy = busyMode !== null;

  useEffect(() => {
    if (!open) return;
    let active = true;
    void onair
      .downloaderStatus()
      .then((s) => {
        if (active) setStatus(s);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [open]);

  const close = () => {
    if (busy) return;
    setOpen(false);
    setError("");
  };

  const handleInstall = async () => {
    setInstalling(true);
    setError("");
    try {
      const next = await onair.installDownloader(false);
      setStatus(next);
    } catch (e) {
      setError(friendlyMediaError(errorMessage(e)));
    } finally {
      setInstalling(false);
    }
  };

  const submit = async (mode: "link" | "import") => {
    setBusyMode(mode);
    setError("");
    try {
      const targetReel = ensureReel ? await ensureReel() : reelId;
      const body: Record<string, unknown> = {
        urls: links
          .split(/\r?\n/)
          .map((s) => s.trim())
          .filter(Boolean),
        kind,
      };
      if (mode === "link") {
        body.mode = "link";
      }
      await apiFetch(
        `/onair/${targetReel ? `reels/${encodeURIComponent(targetReel)}/clips` : "assets"}/import`,
        {
          method: "POST",
          body: JSON.stringify(body),
        },
      );
      setLinks("");
      setOpen(false);
      await onImported();
    } catch (e) {
      setError(friendlyMediaError(errorMessage(e)));
    } finally {
      setBusyMode(null);
    }
  };

  return (
    <>
      <Button
        size="sm"
        variant="outline"
        onClick={() => {
          setError("");
          setOpen(true);
        }}
      >
        <Link size={14} />
        {t("Add from link")}
      </Button>
      <Drawer
        open={open}
        onClose={close}
        title={t("Add from link")}
        description={
          isReel
            ? t(
                "Paste a YouTube, Vimeo, Archive.org or direct video link · up to 3 min and 500 MB each",
              )
            : t(
                "Paste a YouTube, Vimeo, Archive.org or direct media link · up to 30 s and 32 MB each",
              )
        }
        size="md"
        footer={
          <div className="flex w-full flex-wrap items-center justify-end gap-2">
            <Button variant="outline" disabled={busy} onClick={close}>
              {t("Cancel")}
            </Button>
            <Button
              variant={isReel ? "outline" : "default"}
              disabled={busy || !links.trim()}
              onClick={() => void submit("import")}
            >
              {busyMode === "import" ? t("Importing…") : t("Import")}
            </Button>
            {isReel ? (
              <Button disabled={busy || !links.trim()} onClick={() => void submit("link")}>
                {busyMode === "link" ? t("Adding…") : t("Add as link")}
              </Button>
            ) : null}
          </div>
        }
      >
        <div className="space-y-5">
          {needsDownloader && status && !status.available ? (
            <div className="flex flex-wrap items-start justify-between gap-3 rounded-xl border border-border bg-bg-subtle p-3.5">
              <div className="min-w-0 flex-1 space-y-1">
                <p className="text-sm font-medium text-fg">
                  {t("Video downloader isn’t installed yet")}
                </p>
                <p className="text-xs text-fg-muted">
                  {status.canInstall
                    ? t(
                        "Direct MP4 and Archive.org links work right away. Install the video downloader to import from YouTube, Vimeo and other video sites.",
                      )
                    : t(
                        "Direct MP4 and Archive.org links work right away. Install a video downloader on the server to import from video sites.",
                      )}
                </p>
              </div>
              {status.canInstall ? (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={installing}
                  onClick={() => void handleInstall()}
                >
                  <Download size={14} />
                  {installing ? t("Installing downloader…") : t("Install downloader")}
                </Button>
              ) : null}
            </div>
          ) : null}
          {!isReel ? (
            <Field label={t("Media type")}>
              <Segmented
                ariaLabel={t("Asset type")}
                value={kind}
                onChange={setKind}
                options={[
                  { value: "video", label: t("Video") },
                  { value: "audio", label: t("Audio") },
                  { value: "image", label: t("Image") },
                ]}
              />
            </Field>
          ) : null}
          <Field
            label={t("Media links")}
            hint={t("Paste one web link per line.")}
            error={error || undefined}
          >
            <Textarea
              className="w-full"
              rows={4}
              aria-label={t("Media links")}
              placeholder={t("Paste one link per line (https://www.youtube.com/watch?v=…)")}
              value={links}
              onChange={(e) => setLinks(e.currentTarget.value)}
            />
          </Field>
        </div>
      </Drawer>
    </>
  );
}
