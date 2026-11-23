import { Badge, Button, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { useCallback, useEffect, useState } from "react";
import { ErrorPanel } from "../../../components/feedback";
import { SettingsRow } from "../../../components/SettingsRow";
import { t } from "../../../i18n";
import { ApiError, api, errorMessage, type SignInLockout } from "../../../lib/api";
import { unit } from "../../../lib/format";

function formatRemaining(remainingMs: number): string {
  const totalSec = Math.max(1, Math.ceil(remainingMs / 1000));
  if (totalSec < 60) return unit(totalSec, "second");
  const mins = Math.floor(totalSec / 60);
  const secs = totalSec % 60;
  return secs > 0 ? `${unit(mins, "minute")} ${unit(secs, "second")}` : unit(mins, "minute");
}

function namespaceLabel(namespace: string): string {
  if (namespace === "login") return t("Sign-in");
  if (namespace === "poll") return t("Polling");
  return namespace;
}

/** Owner-only list of active sign-in rate-limit lockouts with Clear and Clear all controls. */
export function SignInLockouts() {
  const [lockouts, setLockouts] = useState<SignInLockout[] | null>(null);
  const [forbidden, setForbidden] = useState(false);
  const [error, setError] = useState<unknown>();
  const [refreshing, setRefreshing] = useState(false);
  const [clearingKey, setClearingKey] = useState<string>();
  const [clearingAll, setClearingAll] = useState(false);

  const load = useCallback(async (initial = false) => {
    if (!initial) setRefreshing(true);
    setError(undefined);
    try {
      const data = await api.listLockouts();
      setLockouts(Array.isArray(data) ? data : []);
      setForbidden(false);
    } catch (err) {
      if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
        setForbidden(true);
        return;
      }
      setLockouts([]);
      setError(err);
    } finally {
      if (!initial) setRefreshing(false);
    }
  }, []);

  useEffect(() => {
    void load(true);
  }, [load]);

  if (forbidden) return null;

  async function clearOne(item: SignInLockout) {
    setClearingKey(item.key);
    setError(undefined);
    try {
      await api.clearLockout(item.key);
      setLockouts((old) => (old ? old.filter((x) => x.key !== item.key) : old));
      toast.success(t("Lockout cleared"));
    } catch (err) {
      toast.error(errorMessage(err));
      setError(err);
    } finally {
      setClearingKey(undefined);
    }
  }

  async function clearAll() {
    setClearingAll(true);
    setError(undefined);
    try {
      await api.clearAllLockouts();
      setLockouts([]);
      toast.success(t("All sign-in lockouts cleared"));
    } catch (err) {
      toast.error(errorMessage(err));
      setError(err);
    } finally {
      setClearingAll(false);
    }
  }

  const footer =
    lockouts !== null && lockouts.length > 0 ? (
      <>
        <Button
          variant="outline"
          size="sm"
          disabled={refreshing || clearingAll}
          onClick={() => void load()}
        >
          {t("Refresh")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          disabled={clearingAll || clearingKey !== undefined}
          onClick={() => void clearAll()}
        >
          {t("Clear all")}
        </Button>
      </>
    ) : undefined;

  return (
    <SettingsSection
      title={t("Sign-in lockouts")}
      description={t(
        "Devices and accounts temporarily blocked after repeated failed sign-in attempts.",
      )}
      footer={footer}
    >
      {error != null ? (
        <div className="px-5 py-4">
          <ErrorPanel error={error} />
        </div>
      ) : null}
      {lockouts === null ? null : lockouts.length === 0 ? (
        <div className="flex flex-wrap items-center justify-between gap-3 px-5 py-4">
          <p className="text-sm text-fg-muted">{t("No active sign-in lockouts.")}</p>
          <Button variant="outline" size="sm" disabled={refreshing} onClick={() => void load()}>
            {t("Refresh")}
          </Button>
        </div>
      ) : (
        lockouts.map((item) => {
          const remaining = t("{time} remaining", {
            time: formatRemaining(item.remainingMs),
          });
          const description = item.app ? `${item.app} · ${remaining}` : remaining;
          const title = (
            <span className="inline-flex flex-wrap items-center gap-2">
              {item.identity ? (
                <>
                  <span>{item.identity}</span>
                  {item.remoteIp ? (
                    <code className="font-mono text-xs text-fg-muted">{item.remoteIp}</code>
                  ) : null}
                </>
              ) : (
                <code className="font-mono text-sm">{item.remoteIp || item.key.slice(0, 12)}</code>
              )}
              <Badge variant="outline">{namespaceLabel(item.namespace)}</Badge>
            </span>
          );
          return (
            <SettingsRow
              key={item.key}
              title={title}
              description={description}
              control={
                <Button
                  variant="outline"
                  size="sm"
                  disabled={clearingKey === item.key || clearingAll}
                  onClick={() => void clearOne(item)}
                >
                  {t("Clear")}
                </Button>
              }
            />
          );
        })
      )}
    </SettingsSection>
  );
}
