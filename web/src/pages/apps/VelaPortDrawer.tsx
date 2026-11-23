import { Button, Input, Switch } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel } from "../../components/feedback";
import { t } from "../../i18n";

export function VelaPortDrawer({
  open,
  onClose,
  port,
  defaultPort,
  serverPort,
  serveOnMainPort,
  onSave,
}: {
  open: boolean;
  onClose: () => void;
  port?: number;
  defaultPort: number;
  serverPort: number;
  serveOnMainPort: boolean;
  onSave: (next: { port?: number; serveOnMainPort: boolean }) => Promise<void>;
}) {
  const [dedicated, setDedicated] = useState(Boolean(port && port > 0));
  const [portText, setPortText] = useState(String(port || defaultPort));
  const [serveMain, setServeMain] = useState(serveOnMainPort);
  const [fieldError, setFieldError] = useState<string>();
  const [saveError, setSaveError] = useState<unknown>();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    const hasPort = Boolean(port && port > 0);
    setDedicated(hasPort);
    setPortText(String(hasPort ? port : defaultPort));
    setServeMain(hasPort ? serveOnMainPort : true);
    setFieldError(undefined);
    setSaveError(undefined);
  }, [open, port, defaultPort, serveOnMainPort]);

  const initialDedicated = Boolean(port && port > 0);
  const initialPortText = String(initialDedicated ? port : defaultPort);
  const initialServeMain = initialDedicated ? serveOnMainPort : true;
  const dirty =
    dedicated !== initialDedicated ||
    (dedicated && (portText.trim() !== initialPortText || serveMain !== initialServeMain));

  async function handleSave() {
    setFieldError(undefined);
    setSaveError(undefined);
    let nextPort: number | undefined;
    if (dedicated) {
      const parsed = Number(portText.trim());
      if (!Number.isInteger(parsed) || parsed < 1 || parsed > 65535) {
        setFieldError(t("Enter a port between 1 and 65535."));
        return;
      }
      if (parsed === serverPort) {
        setFieldError(
          t("Choose a port different from Ether's main port ({port}).", {
            port: String(serverPort),
          }),
        );
        return;
      }
      nextPort = parsed;
    }
    setSaving(true);
    try {
      await onSave({
        port: nextPort,
        serveOnMainPort: dedicated ? serveMain : true,
      });
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
      title={t("Vela port")}
      description={t("Dedicated listener")}
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
            "Use this if your reverse proxy gives Vela its own address. Saving switches the listener right away.",
          )}
        </p>

        <div className="flex items-start justify-between gap-4 rounded-lg border border-border bg-bg-subtle/60 px-4 py-3.5">
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-fg">{t("Dedicated port")}</p>
            <p className="mt-0.5 text-xs text-fg-muted">
              {t("Listen for Vela on its own port (default {port}).", {
                port: String(defaultPort),
              })}
            </p>
          </div>
          <Switch
            aria-label={t("Dedicated port")}
            checked={dedicated}
            disabled={saving}
            onChange={(e) => {
              const checked = e.currentTarget.checked;
              setDedicated(checked);
              if (checked && !portText.trim()) setPortText(String(defaultPort));
              if (!checked) setServeMain(true);
              setFieldError(undefined);
            }}
          />
        </div>

        {dedicated ? (
          <>
            <div className="space-y-2">
              <label htmlFor="vela-dedicated-port" className="block text-sm font-medium text-fg">
                {t("Port")}
              </label>
              <Input
                id="vela-dedicated-port"
                aria-label={t("Port")}
                type="number"
                min={1}
                max={65535}
                className="w-36"
                placeholder={String(defaultPort)}
                value={portText}
                disabled={saving}
                onChange={(e) => {
                  setPortText(e.currentTarget.value);
                  setFieldError(undefined);
                }}
              />
              {fieldError ? (
                <p role="alert" className="text-xs text-danger">
                  {fieldError}
                </p>
              ) : null}
            </div>

            <div className="flex items-start justify-between gap-4 rounded-lg border border-border bg-bg-subtle/60 px-4 py-3.5">
              <div className="min-w-0 flex-1">
                <p className="text-sm font-medium text-fg">
                  {t("Serve /vela on Ether's main port")}
                </p>
                <p className="mt-0.5 text-xs text-fg-muted">
                  {t(
                    "Turn this off so Vela is no longer reachable through Ether's Plex port ({port}).",
                    { port: String(serverPort) },
                  )}
                </p>
              </div>
              <Switch
                aria-label={t("Serve /vela on Ether's main port")}
                checked={serveMain}
                disabled={saving}
                onChange={(e) => setServeMain(e.currentTarget.checked)}
              />
            </div>
          </>
        ) : null}

        {saveError != null ? <ErrorPanel error={saveError} /> : null}
      </div>
    </Drawer>
  );
}
