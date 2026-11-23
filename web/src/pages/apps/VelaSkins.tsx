import {
  Alert,
  AlertDescription,
  AlertTitle,
  Badge,
  Button,
  Input,
  SearchSelect,
  SettingsSection,
  Switch,
  confirmDialog,
  toast,
} from "@braedonsaunders/appkit-ui";
import { FileUp, Plus, Trash2, X } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Field } from "../../components/fields";
import { SettingsRow } from "../../components/SettingsRow";
import { t } from "../../i18n";
import { ApiError, apiFetch, errorMessage } from "../../lib/api";
import { useAsync } from "../../lib/hooks";

export type VelaSkin = {
  id: string;
  name: string;
  version: string;
  kind: string;
  builtin: boolean;
  preview?: string;
  swatches: string[];
  installedAt?: number;
};

const MAX_SKIN_ARCHIVE_BYTES = 8 * 1024 * 1024;

function builtinSkinLabel(skin: VelaSkin): string {
  if (!skin.builtin) return skin.name;
  switch (skin.id) {
    case "cinematic-doc":
      return t("Cinematic");
    case "mochi":
      return t("Mochi");
    case "marquee":
      return t("Marquee");
    case "jellytwin":
      return t("Jelly Twin");
    case "index":
      return t("Index");
    case "lounge":
      return t("Lounge");
    case "latenight":
      return t("Late Night");
    case "kids":
      return t("Kids");
    case "broadcast":
      return t("Broadcast");
    default:
      return skin.name;
  }
}

function SkinPreviewCard({ skin }: { skin: VelaSkin }) {
  const [imgFailed, setImgFailed] = useState(false);
  useEffect(() => {
    setImgFailed(false);
  }, [skin.preview]);

  if (skin.preview && !imgFailed) {
    return (
      <div className="aspect-[16/9] w-full overflow-hidden border-b border-border bg-surface-muted/40">
        <img
          src={skin.preview}
          alt=""
          loading="lazy"
          onError={() => setImgFailed(true)}
          className="h-full w-full object-cover"
        />
      </div>
    );
  }

  const canvas = skin.swatches[0] ?? "#0b0d12";
  const surface = skin.swatches[1] ?? "#151923";
  const accent = skin.swatches[2] ?? "#e5a00d";
  const ink = skin.swatches[3] ?? "#f4f6fb";

  return (
    <div
      aria-hidden
      className="aspect-[16/9] w-full overflow-hidden border-b border-border p-3"
      style={{ backgroundColor: canvas }}
    >
      <div className="flex h-full flex-col justify-between">
        <div
          className="flex flex-1 flex-col justify-between rounded-lg p-2.5"
          style={{ backgroundColor: surface }}
        >
          <div className="space-y-1.5">
            <div className="h-2 w-20 rounded-sm opacity-90" style={{ backgroundColor: ink }} />
            <div className="h-1.5 w-28 rounded-sm opacity-45" style={{ backgroundColor: ink }} />
          </div>
          <div className="h-2.5 w-11 rounded-full" style={{ backgroundColor: accent }} />
        </div>
        <div className="mt-2 grid grid-cols-4 gap-1.5">
          {[0, 1, 2, 3].map((slot) => (
            <div
              key={slot}
              className="h-6 rounded-md"
              style={{
                backgroundColor: surface,
                boxShadow: slot === 0 ? `inset 0 2px 0 0 ${accent}` : undefined,
              }}
            />
          ))}
        </div>
      </div>
    </div>
  );
}

export function VelaSkins({
  defaultSkinId,
  force,
  onDefaultSkinChange,
  onForceChange,
}: {
  defaultSkinId: string;
  force: boolean;
  onDefaultSkinChange: (skinId: string) => void;
  onForceChange: (force: boolean) => void;
}) {
  const skinsQuery = useAsync(async (signal) => {
    const res = await apiFetch<{ skins?: VelaSkin[] }>("/vela/skins", { signal });
    return Array.isArray(res?.skins) ? res.skins : [];
  }, []);

  const [installOpen, setInstallOpen] = useState(false);
  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [urlValue, setUrlValue] = useState("");
  const [allowLan, setAllowLan] = useState(false);
  const [installing, setInstalling] = useState(false);
  const [installError, setInstallError] = useState<string>("");
  const [removeError, setRemoveError] = useState<unknown>();
  const [removingId, setRemovingId] = useState<string>("");

  const fileInputRef = useRef<HTMLInputElement>(null);
  const urlInputId = useId();

  useEffect(() => {
    if (!installOpen) {
      setSelectedFile(null);
      setUrlValue("");
      setAllowLan(false);
      setInstallError("");
      if (fileInputRef.current) fileInputRef.current.value = "";
    }
  }, [installOpen]);

  const skins = skinsQuery.data ?? [];

  const options: { value: string; label: string }[] = [
    {
      value: defaultSkinId === "cinematic-doc" ? "cinematic-doc" : "cinematic",
      label: t("Cinematic"),
    },
    ...skins
      .filter((skin) => skin.id !== "cinematic-doc" && skin.id !== "cinematic")
      .map((skin) => ({
        value: skin.id,
        label: builtinSkinLabel(skin),
      })),
  ];
  if (!options.some((opt) => opt.value === defaultSkinId)) {
    options.push({ value: defaultSkinId, label: defaultSkinId });
  }

  async function handleInstallSubmit() {
    setInstallError("");
    const trimmedUrl = urlValue.trim();

    if (!selectedFile && !trimmedUrl) {
      setInstallError(t("Choose a .vskin file or enter a package URL."));
      return;
    }
    if (selectedFile && selectedFile.size > MAX_SKIN_ARCHIVE_BYTES) {
      setInstallError(t("Skin archive must be 8 MB or smaller."));
      return;
    }

    setInstalling(true);
    try {
      let installed: VelaSkin;
      if (selectedFile) {
        const form = new FormData();
        form.append("file", selectedFile);
        installed = await apiFetch<VelaSkin>("/vela/skins", {
          method: "POST",
          body: form,
        });
      } else {
        installed = await apiFetch<VelaSkin>("/vela/skins", {
          method: "POST",
          body: JSON.stringify({ url: trimmedUrl, lan: allowLan }),
        });
      }
      await skinsQuery.reload();
      setInstallOpen(false);
      toast.success(t("{name} installed", { name: installed.name }));
    } catch (err) {
      if (err instanceof ApiError && err.code === "ssrf_blocked") {
        setInstallError(errorMessage(err));
      } else {
        setInstallError(errorMessage(err));
      }
    } finally {
      setInstalling(false);
    }
  }

  async function handleRemove(skin: VelaSkin) {
    if (skin.builtin) return;
    const displayName = builtinSkinLabel(skin);
    if (
      !(await confirmDialog({
        title: t("Remove {name}?", { name: displayName }),
        message: t("Viewers using this skin switch back to the default skin."),
        confirmLabel: t("Remove"),
        tone: "danger",
      }))
    ) {
      return;
    }
    setRemovingId(skin.id);
    setRemoveError(undefined);
    try {
      await apiFetch(`/vela/skins/${encodeURIComponent(skin.id)}`, {
        method: "DELETE",
      });
      if (defaultSkinId === skin.id) {
        onDefaultSkinChange("cinematic");
      }
      await skinsQuery.reload();
      toast.success(t("{name} removed", { name: displayName }));
    } catch (err) {
      setRemoveError(err);
    } finally {
      setRemovingId("");
    }
  }

  const isSkinDefault = (skin: VelaSkin) =>
    skin.id === defaultSkinId || (skin.id === "cinematic-doc" && defaultSkinId === "cinematic");

  return (
    <>
      <SettingsSection title={t("Skins")}>
        <SettingsRow
          title={t("Default skin")}
          description={t("The look viewers start with. They can choose their own in Vela.")}
          control={
            <SearchSelect
              ariaLabel={t("Default skin")}
              value={defaultSkinId}
              onChange={onDefaultSkinChange}
              options={options}
            />
          }
        />
        <SettingsRow
          title={t("Use this skin for everyone")}
          description={t(
            "Lock Vela to the default skin so viewers cannot switch skins on their own.",
          )}
          control={
            <Switch
              checked={force}
              aria-label={t("Use this skin for everyone")}
              onChange={(e) => onForceChange(e.currentTarget.checked)}
            />
          }
        />
        <div className="space-y-4 border-t border-border p-5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="text-sm text-fg-muted">
              {t("Built-in skins and custom .vskin packages installed on this server.")}
            </p>
            <Button type="button" variant="outline" size="sm" onClick={() => setInstallOpen(true)}>
              <Plus size={14} aria-hidden />
              {t("Install skin")}
            </Button>
          </div>

          {removeError ? <ErrorPanel error={removeError} /> : null}

          {skinsQuery.error && skins.length === 0 ? (
            <ErrorPanel error={skinsQuery.error} onRetry={() => void skinsQuery.reload()} />
          ) : skinsQuery.loading && skins.length === 0 ? (
            <LoadingRows rows={2} />
          ) : (
            <div data-testid="vela-skins-grid" className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              {skins.map((skin) => {
                const displayName = builtinSkinLabel(skin);
                return (
                  <article
                    key={skin.id}
                    data-skin-id={skin.id}
                    className="flex flex-col overflow-hidden rounded-xl border border-border bg-surface/60"
                  >
                    <SkinPreviewCard skin={skin} />
                    <div className="flex flex-1 flex-col justify-between gap-3 p-3.5">
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <div className="truncate text-sm font-medium text-fg">{displayName}</div>
                          <div className="font-mono text-xs text-fg-muted">
                            {t("v{version}", { version: skin.version })}
                          </div>
                        </div>
                        <div className="flex shrink-0 flex-wrap items-center gap-1.5">
                          {isSkinDefault(skin) ? (
                            <Badge variant="info">{t("Default")}</Badge>
                          ) : null}
                          {skin.builtin ? <Badge variant="secondary">{t("Built-in")}</Badge> : null}
                        </div>
                      </div>

                      <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-1.5" aria-label={t("Color swatches")}>
                          {skin.swatches.slice(0, 6).map((hex, idx) => (
                            <span
                              key={`${skin.id}-${idx}-${hex}`}
                              title={hex}
                              className="h-3.5 w-3.5 rounded-full border border-black/15 dark:border-white/15"
                              style={{ backgroundColor: hex }}
                            />
                          ))}
                        </div>
                        {!skin.builtin ? (
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            disabled={removingId === skin.id}
                            aria-label={t("Remove {name}", { name: displayName })}
                            onClick={() => void handleRemove(skin)}
                          >
                            <Trash2 size={14} aria-hidden />
                            {t("Remove")}
                          </Button>
                        ) : null}
                      </div>
                    </div>
                  </article>
                );
              })}
            </div>
          )}
        </div>
      </SettingsSection>

      <Drawer
        open={installOpen}
        onClose={() => {
          if (!installing) setInstallOpen(false);
        }}
        title={t("Install skin")}
        size="md"
        footer={
          <div className="flex w-full justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={installing}
              onClick={() => setInstallOpen(false)}
            >
              {t("Cancel")}
            </Button>
            <Button type="button" disabled={installing} onClick={() => void handleInstallSubmit()}>
              {installing ? t("Installing…") : t("Install")}
            </Button>
          </div>
        }
      >
        <div className="space-y-5">
          <p className="text-sm text-fg-muted">
            {t(
              "Upload a .vskin package from your computer or install one from a direct download link. Installing a newer version of the same skin ID replaces the existing version.",
            )}
          </p>

          <Field
            label={t("Skin package (.vskin)")}
            hint={t("Maximum 8 MB archive; fonts, images, and JSON tokens are served locally.")}
          >
            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={installing}
                onClick={() => fileInputRef.current?.click()}
              >
                <FileUp size={14} aria-hidden />
                {selectedFile ? t("Choose another file") : t("Choose .vskin file")}
              </Button>
              {selectedFile ? (
                <div className="flex items-center gap-1.5 rounded-md border border-border bg-surface px-2.5 py-1 text-xs text-fg">
                  <span className="max-w-52 truncate font-mono">{selectedFile.name}</span>
                  <button
                    type="button"
                    disabled={installing}
                    aria-label={t("Clear selected file")}
                    onClick={() => {
                      setSelectedFile(null);
                      setInstallError("");
                      if (fileInputRef.current) fileInputRef.current.value = "";
                    }}
                    className="text-fg-muted hover:text-fg"
                  >
                    <X size={14} aria-hidden />
                  </button>
                </div>
              ) : null}
              <input
                ref={fileInputRef}
                type="file"
                accept=".vskin,application/zip,application/octet-stream"
                aria-label={t("Upload .vskin package")}
                className="sr-only"
                onChange={(e) => {
                  const file = e.currentTarget.files?.[0] ?? null;
                  setSelectedFile(file);
                  if (file) setUrlValue("");
                  setInstallError("");
                }}
              />
            </div>
          </Field>

          <Field
            label={t("Package URL")}
            htmlFor={urlInputId}
            hint={t("Leave empty when uploading a local file.")}
          >
            <Input
              id={urlInputId}
              type="url"
              disabled={installing || selectedFile != null}
              placeholder="https://example.com/harbor.vskin"
              value={urlValue}
              onChange={(e) => {
                setUrlValue(e.currentTarget.value);
                setInstallError("");
              }}
            />
          </Field>

          <div className="flex items-start justify-between gap-3 rounded-lg border border-border bg-surface/60 p-3">
            <div className="space-y-0.5">
              <div className="text-sm font-medium text-fg">{t("Allow local network address")}</div>
              <p className="text-xs text-fg-muted">
                {t(
                  "Permit downloading from a private LAN host on your network. Link-local metadata addresses remain blocked.",
                )}
              </p>
            </div>
            <Switch
              checked={allowLan}
              disabled={installing || selectedFile != null}
              aria-label={t("Allow local network address")}
              onChange={(e) => {
                setAllowLan(e.currentTarget.checked);
                setInstallError("");
              }}
            />
          </div>

          {installError ? (
            <Alert variant="destructive" role="alert">
              <AlertTitle>{t("Could not install skin")}</AlertTitle>
              <AlertDescription>{installError}</AlertDescription>
            </Alert>
          ) : null}
        </div>
      </Drawer>
    </>
  );
}
