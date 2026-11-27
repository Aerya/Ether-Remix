import { Button, toast } from "@braedonsaunders/appkit-ui";
import { useRef, useState } from "react";
import ReactMarkdown from "react-markdown";
import { t } from "../i18n";
import { api } from "../lib/api";
import { formatDateTime } from "../lib/format";
import { useAsync } from "../lib/hooks";
import { Drawer } from "./Drawer";
import { ErrorPanel, LoadingInline } from "./feedback";

export const releaseVersion = (version: string) =>
  version.startsWith("v") ? version : `v${version}`;
export const releaseNotesLink = (url?: string) =>
  url && /^https?:\/\//i.test(url) ? url : undefined;

export function UpdateNotesDrawer({
  version,
  available,
  automatic,
  onClose,
}: {
  version: string;
  available?: boolean;
  automatic?: boolean;
  onClose?: () => void;
}) {
  const notes = useAsync((signal) => api.updateNotes(version, signal), [version]);
  const [dismissed, setDismissed] = useState(false);
  const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);
  if (automatic && (!notes.data?.unseen || dismissed)) return null;

  const label = releaseVersion(version);
  const title = available
    ? t("Release notes for {version}", { version: label })
    : t("What's new in {version}", { version: label });
  const link = releaseNotesLink(notes.data?.notesUrl);
  const installed = notes.data?.installed;
  const close = async () => {
    if (!automatic) {
      onClose?.();
      return;
    }
    if (savingRef.current || !notes.data) return;
    savingRef.current = true;
    setSaving(true);
    try {
      await api.dismissUpdateNotes(notes.data.version);
      setDismissed(true);
    } catch {
      toast.error(t("Couldn't dismiss release notes. Try again."));
    } finally {
      savingRef.current = false;
      setSaving(false);
    }
  };

  return (
    <Drawer
      open
      onClose={() => void close()}
      title={title}
      documentTitle={title}
      size="lg"
      description={
        installed
          ? t("Ether installed it by itself on {time}, while nobody was watching.", {
              time: formatDateTime(installed.at),
            })
          : undefined
      }
      footer={
        <div className="flex w-full items-center justify-between gap-3">
          <span>
            {link ? (
              <a
                className="text-sm text-fg-muted underline"
                href={link}
                target="_blank"
                rel="noreferrer"
              >
                {t("Release notes on GitHub")}
              </a>
            ) : null}
          </span>
          <Button onClick={() => void close()} disabled={saving}>
            {automatic ? t("Dismiss") : t("Close")}
          </Button>
        </div>
      }
    >
      {notes.error ? (
        <ErrorPanel
          error={new Error(t("Couldn't load release notes."))}
          onRetry={() => void notes.reload()}
        />
      ) : notes.loading ? (
        <LoadingInline label={t("Loading release notes…")} />
      ) : notes.data?.markdown ? (
        <article className="space-y-4 text-sm leading-relaxed break-words [&_a]:underline [&_h1]:text-xl [&_h1]:font-semibold [&_h2]:border-t [&_h2]:border-border [&_h2]:pt-4 [&>h2:first-child]:border-t-0 [&>h2:first-child]:pt-0 [&_h2]:text-lg [&_h2]:font-semibold [&_h3]:text-base [&_h3]:font-semibold [&_li+li]:mt-2 [&_ol]:list-decimal [&_ol]:pl-5 [&_pre]:overflow-x-auto [&_ul]:list-disc [&_ul]:pl-5 [&_li>ul]:mt-2">
          <ReactMarkdown skipHtml disallowedElements={["img"]}>
            {notes.data.markdown}
          </ReactMarkdown>
        </article>
      ) : (
        <p className="text-sm text-fg-muted">
          {t("No release notes are available for this version.")}
        </p>
      )}
    </Drawer>
  );
}

export function AutomaticWhatsNew({ version }: { version: string }) {
  return <UpdateNotesDrawer version={version} automatic />;
}
