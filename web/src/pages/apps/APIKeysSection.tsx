import { Button, SettingsSection, confirmDialog } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { ErrorPanel } from "../../components/feedback";
import { SettingsRow } from "../../components/SettingsRow";
import { t } from "../../i18n";
import { apiFetch } from "../../lib/api";
import { formatDateTime } from "../../lib/format";

export interface FrontendAPIKey {
  id: number;
  appName: string;
  createdAt: string;
  lastUsed?: string;
}

/** Minimal list and revoke controls for Jellyfin and Emby owner API keys. */
export function APIKeysSection({ id }: { id: "jellyfin" | "emby" }) {
  const [keys, setKeys] = useState<FrontendAPIKey[] | null>(null);
  const [error, setError] = useState<unknown>();
  const [revoking, setRevoking] = useState<number>();

  useEffect(() => {
    let live = true;
    setError(undefined);
    void apiFetch<FrontendAPIKey[]>(`/frontends/${id}/api-keys`)
      .then((data) => {
        if (live) setKeys(Array.isArray(data) ? data : []);
      })
      .catch((err) => {
        if (live) {
          setKeys([]);
          setError(err);
        }
      });
    return () => {
      live = false;
    };
  }, [id]);

  async function revoke(key: FrontendAPIKey) {
    if (
      !(await confirmDialog({
        title: t("Revoke this API key?"),
        message: t("Tools using {name} will lose access right away.", { name: key.appName }),
        confirmLabel: t("Revoke"),
        tone: "danger",
      }))
    ) {
      return;
    }
    setRevoking(key.id);
    setError(undefined);
    try {
      await apiFetch(`/frontends/${id}/api-keys/${key.id}`, { method: "DELETE" });
      setKeys((old) => (old ? old.filter((item) => item.id !== key.id) : old));
    } catch (err) {
      setError(err);
    } finally {
      setRevoking(undefined);
    }
  }

  return (
    <SettingsSection
      title={t("API keys")}
      description={t("Tools like Wizarr and Jellyseerr use API keys to act as the server owner.")}
    >
      {error != null ? (
        <div className="px-5 py-4">
          <ErrorPanel error={error} />
        </div>
      ) : null}
      {keys === null ? null : keys.length === 0 ? (
        <p className="px-5 py-4 text-sm text-fg-muted">{t("No API keys yet.")}</p>
      ) : (
        keys.map((key) => {
          const created = t("Created {time}", { time: formatDateTime(key.createdAt) });
          const lastUsed = t("Last used {time}", {
            time: key.lastUsed ? formatDateTime(key.lastUsed) : t("Never"),
          });
          return (
            <SettingsRow
              key={key.id}
              title={key.appName}
              description={`${created} · ${lastUsed}`}
              control={
                <Button
                  variant="outline"
                  size="sm"
                  disabled={revoking === key.id}
                  onClick={() => void revoke(key)}
                >
                  {t("Revoke")}
                </Button>
              }
            />
          );
        })
      )}
    </SettingsSection>
  );
}
