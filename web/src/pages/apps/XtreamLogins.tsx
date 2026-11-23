import { Eye, EyeOff } from "lucide-react";
import { useEffect, useState } from "react";
import { Button, SettingsSection } from "@braedonsaunders/appkit-ui";
import { CopyButton } from "../../components/CopyButton";
import { loadViewers, viewerName } from "../../components/pickers";
import { apiFetch, errorMessage } from "../../lib/api";
import { t } from "../../i18n";

type Viewer = { id: number; title: string; username: string };
type Connection = { url: string; username: string; password: string };
type Grant = {
  hash: string;
  surface: string;
  lastUsed: number;
  xtreamUsername?: string;
  xtreamStreamLimit?: number;
  revealAvailable?: boolean;
};

function XtreamLoginRow({
  grant,
  viewer,
  index,
  busy,
  revoke,
}: {
  grant: Grant;
  viewer: string;
  index: number;
  busy: boolean;
  revoke: () => void;
}) {
  const [secret, setSecret] = useState<Connection>();
  const [limit, setLimit] = useState(String(grant.xtreamStreamLimit || ""));
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  async function reveal() {
    if (secret) {
      setSecret(undefined);
      return;
    }
    setSaving(true);
    setError("");
    try {
      const result = await apiFetch<Connection & { available: boolean }>(
        `/users/${viewer}/devices/xtream/reveal`,
        { method: "POST", body: JSON.stringify({ hash: grant.hash }) },
      );
      if (result.available) setSecret(result);
      else setError(t("Not available — create a new short login"));
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  }
  async function saveLimit() {
    const value = limit === "" ? 0 : Number(limit);
    if (!Number.isInteger(value) || value < 0 || value > 1000) {
      setError(t("Enter a whole number from 1 to 1000, or leave empty."));
      return;
    }
    setSaving(true);
    setError("");
    try {
      await apiFetch(`/users/${viewer}/devices/xtream/limit`, {
        method: "POST",
        body: JSON.stringify({ hash: grant.hash, limit: value }),
      });
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setSaving(false);
    }
  }
  return (
    <div className="space-y-3 border-t border-border pt-3">
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm">
          {grant.xtreamUsername || t("Login {number}", { number: index + 1 })}
        </span>
        <Button variant="outline" disabled={busy || saving} onClick={revoke}>
          {t("Revoke")}
        </Button>
      </div>
      {grant.xtreamUsername && (
        <div className="flex items-center justify-between gap-3">
          <code>{grant.xtreamUsername}</code>
          <CopyButton label={t("Username")} value={grant.xtreamUsername} />
        </div>
      )}
      {grant.revealAvailable ? (
        <div className="flex items-center justify-between gap-3">
          <code className="font-mono text-base tracking-wide">
            {secret?.password ?? "••••-••••-••"}
          </code>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={saving}
              onClick={() => void reveal()}
              aria-label={secret ? t("Hide password") : t("Show password")}
            >
              {secret ? <EyeOff size={14} /> : <Eye size={14} />}
            </Button>
            {secret && <CopyButton label={t("Password")} value={secret.password} />}
          </div>
        </div>
      ) : (
        <p className="text-sm text-fg-muted">{t("Not available — create a new short login")}</p>
      )}
      <label className="block space-y-1 text-sm">
        <span>{t("Streams on this device")}</span>
        <input
          type="number"
          min={1}
          max={1000}
          step={1}
          value={limit}
          onChange={(e) => setLimit(e.target.value)}
          onBlur={() => void saveLimit()}
          disabled={busy || saving}
          className="block w-full rounded-lg border border-border bg-surface p-2"
        />
        <span className="block text-fg-muted">
          {t("Leave empty to use the viewer’s stream limit.")}
        </span>
      </label>
      {error && (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      )}
    </div>
  );
}

export function XtreamLogins() {
  const [viewers, setViewers] = useState<Viewer[]>([]);
  const [viewer, setViewer] = useState("");
  const [grants, setGrants] = useState<Grant[]>([]);
  const [connection, setConnection] = useState<Connection>();
  const [serverUrl, setServerUrl] = useState("");
  const [showCreated, setShowCreated] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let live = true;
    void loadViewers()
      .then((v) => {
        if (live) setViewers(v);
      })
      .catch((e) => {
        if (live) setError(errorMessage(e));
      });
    return () => {
      live = false;
    };
  }, []);
  useEffect(() => {
    let live = true;
    setConnection(undefined);
    setShowCreated(false);
    setGrants([]);
    setServerUrl("");
    setError("");
    if (viewer)
      void apiFetch<{ devices: Grant[]; xtreamUrl?: string }>(`/users/${viewer}/devices`)
        .then((v) => {
          if (!live) return;
          setGrants(v.devices.filter((g) => g.surface === "xtream"));
          setServerUrl(v.xtreamUrl ?? "");
        })
        .catch((e) => {
          if (live) setError(errorMessage(e));
        });
    return () => {
      live = false;
    };
  }, [viewer]);
  async function create() {
    setBusy(true);
    setError("");
    setConnection(undefined);
    setShowCreated(false);
    try {
      const result = await apiFetch<Connection>(`/viewers/${viewer}/apps/xtream`, {
        method: "POST",
      });
      setConnection(result);
      const list = await apiFetch<{ devices: Grant[]; xtreamUrl?: string }>(
        `/users/${viewer}/devices`,
      );
      setGrants(list.devices.filter((g) => g.surface === "xtream"));
      setServerUrl(list.xtreamUrl || result.url);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  async function revoke(hash: string) {
    setBusy(true);
    setError("");
    try {
      await apiFetch(`/users/${viewer}/devices/revoke`, {
        method: "POST",
        body: JSON.stringify({ hash }),
      });
      setGrants((old) => old.filter((g) => g.hash !== hash));
      setConnection(undefined);
      setShowCreated(false);
    } catch (e) {
      setError(errorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <SettingsSection
      title={t("IPTV apps (Xtream)")}
      description={t("Live TV, movies and series in IPTV player apps.")}
    >
      <div className="space-y-4 px-5 py-4">
        <p className="text-sm text-fg-muted">
          {t(
            "Create a separate login for each device. All logins share the viewer’s stream limit.",
          )}
        </p>
        <label className="block space-y-2 text-sm">
          <span>{t("Viewer")}</span>
          <select
            className="block w-full rounded-lg border border-border bg-surface p-2"
            value={viewer}
            disabled={busy}
            onChange={(e) => setViewer(e.target.value)}
          >
            <option value="">{t("Choose a viewer")}</option>
            {viewers.map((v) => (
              <option key={v.id} value={v.id}>
                {viewerName(v)}
              </option>
            ))}
          </select>
        </label>
        <Button disabled={!viewer || busy} onClick={() => void create()}>
          {t("Create connection")}
        </Button>
        {connection && (
          <div className="space-y-3 rounded-lg border border-border p-4">
            <p className="text-sm text-fg-muted">
              {t("Keep this login for your device. You can reveal it here later.")}
            </p>
            <p className="text-sm text-fg-muted">
              {t("You can enter the password with or without hyphens.")}
            </p>
            {(
              [
                [t("Server address"), connection.url],
                [t("Username"), connection.username],
                [t("Password"), connection.password],
              ] as const
            ).map(([label, value], field) => (
              <div key={label} className="space-y-1">
                <div className="text-sm font-medium">{label}</div>
                <div className="flex items-center justify-between gap-3">
                  <code className="min-w-0 break-all font-mono text-base tracking-wide select-all">
                    {field === 2 && !showCreated ? "••••-••••-••" : value}
                  </code>
                  <div className="flex gap-2">
                    {field === 2 && (
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => setShowCreated(!showCreated)}
                        aria-label={showCreated ? t("Hide password") : t("Show password")}
                      >
                        {showCreated ? <EyeOff size={14} /> : <Eye size={14} />}
                      </Button>
                    )}
                    <CopyButton label={label} value={value} />
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
        {!connection && grants.length > 0 && serverUrl && (
          <div className="space-y-1">
            <div className="text-sm font-medium">{t("Server address")}</div>
            <div className="flex items-center justify-between gap-3">
              <code className="min-w-0 break-all font-mono text-base tracking-wide select-all">
                {serverUrl}
              </code>
              <CopyButton label={t("Server address")} value={serverUrl} />
            </div>
          </div>
        )}
        {grants.map((g, index) => (
          <XtreamLoginRow
            key={`${viewer}:${g.hash}`}
            grant={g}
            viewer={viewer}
            index={index}
            busy={busy}
            revoke={() => void revoke(g.hash)}
          />
        ))}
        {error && (
          <p role="alert" className="text-sm text-danger">
            {error}
          </p>
        )}
      </div>
    </SettingsSection>
  );
}
