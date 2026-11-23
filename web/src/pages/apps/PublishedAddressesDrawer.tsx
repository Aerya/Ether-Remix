import { Button, Input } from "@braedonsaunders/appkit-ui";
import { Plus, X } from "lucide-react";
import { useEffect, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel } from "../../components/feedback";
import { t } from "../../i18n";
import { normalizePublishedAddress, validatePublishedAddress } from "./plex-account";

const MAX_PUBLISHED_ADDRESSES = 16;

function exampleSlug(appId: string): string {
  switch (appId) {
    case "jellyfin":
    case "emby":
    case "silo":
    case "vela":
    case "stremio":
    case "xtream":
    case "m3u":
    case "webdav":
      return appId;
    default:
      return "media";
  }
}

export function PublishedAddressesDrawer({
  open,
  onClose,
  appId,
  appName,
  addresses,
  observedAddresses,
  onSave,
}: {
  open: boolean;
  onClose: () => void;
  appId: string;
  appName: string;
  addresses: string[];
  observedAddresses?: string[];
  onSave: (next: string[]) => Promise<void>;
}) {
  const [draft, setDraft] = useState<string[]>(addresses);
  const [input, setInput] = useState("");
  const [fieldError, setFieldError] = useState<string>();
  const [saveError, setSaveError] = useState<unknown>();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setDraft(addresses);
    setInput("");
    setFieldError(undefined);
    setSaveError(undefined);
  }, [open, addresses]);

  const slug = exampleSlug(appId);
  const subExample = `https://${slug}.example.com`;
  const pathExample = `https://example.com/${slug}`;

  const suggestions = (observedAddresses ?? []).filter((raw) => {
    const norm = normalizePublishedAddress(raw);
    return norm != null && !draft.includes(norm);
  });

  const dirty =
    draft.length !== addresses.length ||
    draft.some((item, index) => item !== addresses[index]) ||
    input.trim().length > 0;

  function addAddress(raw: string): string[] | null {
    const trimmed = raw.trim();
    if (!trimmed) return draft;
    const err = validatePublishedAddress(trimmed);
    if (err) {
      setFieldError(err);
      return null;
    }
    const normalized = normalizePublishedAddress(trimmed)!;
    if (draft.includes(normalized)) {
      setFieldError(t("That address is already in the list."));
      return null;
    }
    if (draft.length >= MAX_PUBLISHED_ADDRESSES) {
      setFieldError(
        t("You can add up to {max} published addresses.", {
          max: String(MAX_PUBLISHED_ADDRESSES),
        }),
      );
      return null;
    }
    const next = [...draft, normalized];
    setDraft(next);
    setInput("");
    setFieldError(undefined);
    return next;
  }

  function removeAddress(target: string) {
    setDraft((prev) => prev.filter((item) => item !== target));
    setFieldError(undefined);
  }

  async function handleSave() {
    let next = draft;
    if (input.trim()) {
      const added = addAddress(input);
      if (!added) return;
      next = added;
    }
    setSaving(true);
    setSaveError(undefined);
    try {
      await onSave(next);
      onClose();
    } catch (err) {
      setSaveError(err);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Drawer
      open={open}
      onClose={() => {
        if (!saving) onClose();
      }}
      title={t("Published addresses")}
      description={appName}
      size="md"
      footer={
        <div className="flex w-full justify-end gap-2">
          <Button type="button" variant="outline" disabled={saving} onClick={onClose}>
            {t("Cancel")}
          </Button>
          <Button type="button" disabled={saving || !dirty} onClick={() => void handleSave()}>
            {saving ? t("Saving…") : t("Save")}
          </Button>
        </div>
      }
    >
      <div className="space-y-5">
        <p className="text-sm text-fg-muted">
          {t(
            "Optional. Ether already answers requests using the address each app connects on. Add addresses here only for links Ether shares ahead of time — QR codes, invites, and share links.",
          )}
        </p>

        {suggestions.length > 0 ? (
          <div className="space-y-2 rounded-lg border border-border bg-bg-subtle/60 p-3.5">
            <div>
              <p className="text-xs font-medium text-fg">{t("Seen recently")}</p>
              <p className="mt-0.5 text-xs text-fg-muted">
                {t("Click an address to add it to your published list.")}
              </p>
            </div>
            <div className="flex flex-wrap gap-2 pt-0.5">
              {suggestions.map((candidate) => (
                <button
                  key={candidate}
                  type="button"
                  disabled={saving}
                  onClick={() => {
                    addAddress(candidate);
                  }}
                  className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 py-1.5 text-left font-mono text-xs text-fg transition-colors hover:border-primary/50 hover:bg-primary/5 hover:text-primary disabled:opacity-50"
                >
                  <Plus size={13} className="shrink-0 text-primary" aria-hidden />
                  <span className="truncate">{candidate}</span>
                </button>
              ))}
            </div>
          </div>
        ) : null}

        <div className="space-y-2">
          <label htmlFor={`${appId}-published-input`} className="block text-sm font-medium text-fg">
            {t("Add an address")}
          </label>
          <div className="flex items-center gap-2">
            <Input
              id={`${appId}-published-input`}
              aria-label={t("Add an address")}
              type="url"
              inputMode="url"
              autoComplete="off"
              spellCheck={false}
              className="flex-1 font-mono text-sm"
              placeholder={subExample}
              value={input}
              disabled={saving}
              onChange={(e) => {
                setInput(e.currentTarget.value);
                if (fieldError) setFieldError(undefined);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault();
                  addAddress(input);
                }
              }}
            />
            <Button
              type="button"
              variant="outline"
              disabled={saving || !input.trim()}
              onClick={() => {
                addAddress(input);
              }}
            >
              {t("Add")}
            </Button>
          </div>
          {fieldError ? (
            <p role="alert" className="text-xs text-danger">
              {fieldError}
            </p>
          ) : (
            <p className="text-xs text-fg-muted">
              {t("Works with subdomains ({sub}) or paths ({path}).", {
                sub: subExample,
                path: pathExample,
              })}
            </p>
          )}
        </div>

        <div className="space-y-2">
          <p className="text-sm font-medium text-fg">{t("Addresses to share")}</p>
          {draft.length > 0 ? (
            <ul className="divide-y divide-border rounded-lg border border-border bg-surface">
              {draft.map((address, index) => (
                <li key={address} className="flex items-center justify-between gap-2 px-3.5 py-2.5">
                  <div className="min-w-0 flex-1">
                    <p className="break-all font-mono text-sm text-fg">{address}</p>
                    {index === 0 && draft.length > 1 ? (
                      <span className="text-xs text-fg-muted">
                        {t("Used first for QR codes and share links")}
                      </span>
                    ) : null}
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    disabled={saving}
                    aria-label={t("Remove {address}", { address })}
                    onClick={() => removeAddress(address)}
                  >
                    <X size={15} />
                  </Button>
                </li>
              ))}
            </ul>
          ) : (
            <div className="rounded-lg border border-dashed border-border px-3.5 py-3 text-sm text-fg-muted">
              {t(
                "No published addresses yet. Ether will use your local address or the current request.",
              )}
            </div>
          )}
        </div>

        {saveError != null ? <ErrorPanel error={saveError} /> : null}
      </div>
    </Drawer>
  );
}
