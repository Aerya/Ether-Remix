import { useEffect, useRef, useState } from "react";
import { Button, SettingsSection } from "@braedonsaunders/appkit-ui";
import { ErrorPanel } from "../../components/feedback";
import { apiFetch } from "../../lib/api";
import { t } from "../../i18n";

export type VelaDistributionStatus = {
  installed: boolean;
  installedVersion: string;
  availableVersion: string;
  rollbackAvailable: boolean;
  busy: boolean;
  phase: string;
  downloaded: number;
  total: number;
  error?: string;
};

export function VelaDistribution() {
  const [status, setStatus] = useState<VelaDistributionStatus>();
  const [error, setError] = useState<unknown>();
  const [pending, setPending] = useState(false);
  const request = useRef<AbortController | null>(null);
  async function action(name: "check" | "install" | "rollback") {
    const controller = new AbortController();
    request.current = controller;
    setPending(true);
    setError(undefined);
    try {
      setStatus(
        await apiFetch<VelaDistributionStatus>(`/vela/${name}`, {
          method: "POST",
          body: "{}",
          signal: controller.signal,
        }),
      );
    } catch (err) {
      if (!controller.signal.aborted) setError(err);
    } finally {
      if (request.current === controller) request.current = null;
      setPending(false);
    }
  }
  useEffect(() => {
    const controller = new AbortController();
    void apiFetch<VelaDistributionStatus>("/vela/status", { signal: controller.signal })
      .then(setStatus)
      .catch((err) => {
        if (!controller.signal.aborted) setError(err);
      });
    void action("check");
    return () => {
      controller.abort();
      request.current?.abort();
    };
  }, []);
  useEffect(() => {
    if (!pending && !status?.busy) return;
    const controller = new AbortController();
    const timer = setInterval(() => {
      void apiFetch<VelaDistributionStatus>("/vela/status", { signal: controller.signal })
        .then(setStatus)
        .catch(() => {});
    }, 1000);
    return () => {
      clearInterval(timer);
      controller.abort();
    };
  }, [pending, status?.busy]);
  const busy = pending || status?.busy;
  const progress = status?.total
    ? Math.min(100, Math.round((status.downloaded * 100) / status.total))
    : undefined;
  return (
    <SettingsSection title={status?.installed ? t("Vela") : t("Install Vela")}>
      <div className="space-y-3 p-5">
        <p>
          {status?.installed
            ? t("Installed version: {version}", {
                version: status.installedVersion || t("Unknown"),
              })
            : t("Install Vela to watch Ether in your browser.")}
        </p>
        {status?.availableVersion ? (
          <p>{t("Available version: {version}", { version: status.availableVersion })}</p>
        ) : null}
        {busy ? (
          <p role="status">
            {status?.phase === "checking"
              ? t("Checking for updates…")
              : status?.phase === "rollback"
                ? t("Restoring previous version…")
                : t("Installing Vela…")}
            {progress === undefined ? "" : ` ${progress}%`}
          </p>
        ) : null}
        <div className="flex flex-wrap gap-2">
          {status?.availableVersion ? (
            <Button disabled={busy} onClick={() => void action("install")}>
              {status.installed ? t("Update") : t("Install Vela")}
            </Button>
          ) : null}
          <Button disabled={busy} onClick={() => void action("check")}>
            {t("Check for updates")}
          </Button>
          {status?.rollbackAvailable ? (
            <Button disabled={busy} onClick={() => void action("rollback")}>
              {t("Roll back")}
            </Button>
          ) : null}
          {pending ? <Button onClick={() => request.current?.abort()}>{t("Cancel")}</Button> : null}
        </div>
        {error ? (
          <ErrorPanel error={error} />
        ) : status?.error ? (
          <ErrorPanel error={new Error(status.error)} />
        ) : null}
      </div>
    </SettingsSection>
  );
}
