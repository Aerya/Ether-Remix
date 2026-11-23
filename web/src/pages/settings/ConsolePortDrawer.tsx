import { Button, Input, SettingsSection, Switch, toast } from "@braedonsaunders/appkit-ui";
import { AlertTriangle } from "lucide-react";
import { useEffect, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel } from "../../components/feedback";
import { SettingsRow } from "../../components/SettingsRow";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import type { ConsoleListenerStatus, EtherConfig } from "../../lib/types";

const DEFAULT_CONSOLE_PORT = 8099;

export function ConsolePortDrawer({
  open,
  onClose,
  consolePort,
  disableOnMainPort,
  serverPort,
  onSave,
}: {
  open: boolean;
  onClose: () => void;
  consolePort: number;
  disableOnMainPort: boolean;
  serverPort: number;
  onSave: (next: { consolePort: number; disableConsoleOnMainPort: boolean }) => Promise<void>;
}) {
  const suggestedPort = serverPort === DEFAULT_CONSOLE_PORT ? 8100 : DEFAULT_CONSOLE_PORT;
  const [dedicated, setDedicated] = useState(consolePort > 0);
  const [portText, setPortText] = useState(String(consolePort > 0 ? consolePort : suggestedPort));
  const [serveMain, setServeMain] = useState(!disableOnMainPort);
  const [fieldError, setFieldError] = useState<string>();
  const [saveError, setSaveError] = useState<unknown>();
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    const hasPort = consolePort > 0;
    setDedicated(hasPort);
    setPortText(String(hasPort ? consolePort : suggestedPort));
    setServeMain(hasPort ? !disableOnMainPort : true);
    setFieldError(undefined);
    setSaveError(undefined);
  }, [open, consolePort, disableOnMainPort, suggestedPort]);

  const initialDedicated = consolePort > 0;
  const initialPortText = String(initialDedicated ? consolePort : suggestedPort);
  const initialServeMain = initialDedicated ? !disableOnMainPort : true;
  const dirty =
    dedicated !== initialDedicated ||
    (dedicated && (portText.trim() !== initialPortText || serveMain !== initialServeMain));

  async function handleSave() {
    setFieldError(undefined);
    setSaveError(undefined);
    let nextPort = 0;
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
        consolePort: nextPort,
        disableConsoleOnMainPort: dedicated ? !serveMain : false,
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
      title={t("Owner console port")}
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
            "Use this if your reverse proxy gives the owner console (/ether) its own address. Home-network protection and your outside-access switch still apply on the dedicated port.",
          )}
        </p>

        <div className="flex items-start justify-between gap-4 rounded-lg border border-border bg-bg-subtle/60 px-4 py-3.5">
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium text-fg">{t("Dedicated console port")}</p>
            <p className="mt-0.5 text-xs text-fg-muted">
              {t("Serve /ether and its API on a separate port.")}
            </p>
          </div>
          <Switch
            aria-label={t("Dedicated console port")}
            checked={dedicated}
            disabled={saving}
            onChange={(e) => {
              const checked = e.currentTarget.checked;
              setDedicated(checked);
              if (checked && !portText.trim()) setPortText(String(suggestedPort));
              if (!checked) setServeMain(true);
              setFieldError(undefined);
            }}
          />
        </div>

        {dedicated ? (
          <>
            <div className="space-y-2">
              <label htmlFor="console-dedicated-port" className="block text-sm font-medium text-fg">
                {t("Port")}
              </label>
              <Input
                id="console-dedicated-port"
                aria-label={t("Port")}
                type="number"
                min={1}
                max={65535}
                className="w-36"
                placeholder={String(suggestedPort)}
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
                  {t("Serve /ether on Ether's main port")}
                </p>
                <p className="mt-0.5 text-xs text-fg-muted">
                  {t(
                    "Turn this off so the owner console is no longer reachable through Ether's Plex port ({port}). If the dedicated port ever fails to bind, Ether falls back to the main port automatically so you are never locked out.",
                    { port: String(serverPort) },
                  )}
                </p>
              </div>
              <Switch
                aria-label={t("Serve /ether on Ether's main port")}
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

export function ConsolePortSection() {
  const [drawerOpen, setDrawerOpen] = useState(false);
  const state = useAsync(async () => {
    const [cfgRes, status] = await Promise.all([
      apiFetch<{ config?: EtherConfig } & Partial<EtherConfig>>("/config"),
      apiFetch<ConsoleListenerStatus>("/server/console-listener").catch(() => null),
    ]);
    const cfg = cfgRes.config ?? (cfgRes as EtherConfig);
    return { server: cfg.server, status };
  }, []);

  const serverPort = state.data?.status?.mainPort || state.data?.server?.port || 32400;
  const consolePort = state.data?.server?.consolePort ?? state.data?.status?.configuredPort ?? 0;
  const disableOnMain = Boolean(state.data?.server?.disableConsoleOnMainPort);
  const status = state.data?.status;
  const fallback = Boolean(status?.fallback);
  const activePort = status?.activePort || 0;

  let description: string;
  if (fallback && consolePort > 0) {
    description = t(
      "Dedicated port {port} could not bind, so the console stayed on Ether's main port ({mainPort}).",
      { port: String(consolePort), mainPort: String(serverPort) },
    );
  } else if (consolePort > 0 && disableOnMain) {
    description = t(
      "Listening on dedicated port {port}. /ether is turned off on Ether's main port.",
      { port: String(activePort || consolePort) },
    );
  } else if (consolePort > 0) {
    description = t("Listening on dedicated port {port} and Ether's main port ({mainPort}).", {
      port: String(activePort || consolePort),
      mainPort: String(serverPort),
    });
  } else {
    description = t(
      "Shared on Ether's main port. Use a dedicated port if your reverse proxy gives the owner console its own address.",
    );
  }

  async function save(next: { consolePort: number; disableConsoleOnMainPort: boolean }) {
    await apiFetch("/config/server", {
      method: "PUT",
      body: JSON.stringify(next),
    });
    const currentPort = typeof window !== "undefined" ? Number(window.location.port || 0) : 0;
    if (
      next.consolePort > 0 &&
      next.disableConsoleOnMainPort &&
      currentPort === serverPort &&
      typeof window !== "undefined"
    ) {
      const target = `${window.location.protocol}//${window.location.hostname}:${next.consolePort}/ether/`;
      toast.success(t("Owner console moved to port {port}", { port: String(next.consolePort) }), {
        description: t("Open {url} to keep managing Ether.", { url: target }),
      });
      return;
    }
    await state.reload();
  }

  return (
    <>
      <SettingsSection>
        {fallback ? (
          <div
            role="status"
            className="flex items-start gap-2.5 border-b border-warning/25 bg-warning/10 px-5 py-3 text-sm text-warning"
          >
            <AlertTriangle size={16} className="mt-0.5 shrink-0" aria-hidden />
            <span>
              {t(
                "Port {port} couldn't bind, so Ether kept the console on port {mainPort} to prevent lockout.",
                { port: String(consolePort), mainPort: String(serverPort) },
              )}
              {status?.error ? ` (${status.error})` : ""}
            </span>
          </div>
        ) : null}
        <SettingsRow
          setting="consolePort"
          title={t("Owner console port")}
          description={description}
          control={
            <div className="flex items-center gap-2">
              <span className="rounded-md border border-border bg-bg-subtle px-2.5 py-1 font-mono text-xs text-fg">
                {consolePort > 0 && !fallback ? activePort || consolePort : serverPort}
              </span>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={state.loading && !state.data}
                onClick={() => setDrawerOpen(true)}
              >
                {consolePort > 0 ? t("Edit") : t("Set port")}
              </Button>
            </div>
          }
        />
      </SettingsSection>
      <ConsolePortDrawer
        open={drawerOpen}
        onClose={() => setDrawerOpen(false)}
        consolePort={consolePort}
        disableOnMainPort={disableOnMain}
        serverPort={serverPort}
        onSave={save}
      />
    </>
  );
}
