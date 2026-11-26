import {
  Alert,
  AlertDescription,
  AlertTitle,
  Button,
  SettingsSection,
  Spinner,
  toast,
} from "@braedonsaunders/appkit-ui";
import { useMemo, useState } from "react";
import { MoreOptions } from "../../components/ConfigSection";
import { ErrorPanel } from "../../components/feedback";
import { SaveBar, useDraft } from "../../components/SaveBar";
import { t } from "../../i18n";
import { ApiError, api, apiFetch } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import { SchemaField, type FieldDef } from "../settings/SchemaForm";
import * as schema from "../settings/schema";
import { translatePlexFieldError, validateCustomUrls } from "./plex-account";

type Obj = Record<string, unknown>;
type AwayDraft = { plex: Obj; server: Obj };

/** A home-network peer forwarding visitor addresses that Ether does not trust yet. */
interface UntrustedProxy {
  address: string;
  /** Host or container name that resolves back to the address, when there is one. */
  name?: string;
  requests: number;
  lastSeen: string;
}

function group(id: string) {
  const found = schema.AWAY.find((item) => item.id === id);
  if (!found) throw new Error(`missing away settings group ${id}`);
  return found;
}

/**
 * Public address, port, extra URLs, trusted reverse proxies and certificate
 * every app uses to reach this server from outside.
 */
export function AwayFromHome() {
  const config = useAsync(() => api.getConfig(), []);
  const address = group("address");
  const proxy = group("proxy");
  const certificate = group("certificate");
  const plexKeys = useMemo(
    () => [...(address.more ?? []), ...(certificate.more ?? [])].map((f) => f.key),
    [address, certificate],
  );
  const serverKeys = useMemo(() => (proxy.more ?? []).map((f) => f.key), [proxy]);
  const saved = useMemo<AwayDraft | null>(() => {
    const plex = config.data?.plex as Obj | undefined;
    if (!plex) return null;
    const server = (config.data?.server as Obj | undefined) ?? {};
    return {
      plex: Object.fromEntries(plexKeys.map((key) => [key, plex[key]])),
      server: Object.fromEntries(serverKeys.map((key) => [key, server[key] ?? []])),
    };
  }, [config.data, plexKeys, serverKeys]);
  const { draft, setDraft, dirty, reset } = useDraft(saved);
  const hint = useAsync(
    (signal) =>
      config.data
        ? apiFetch<{ proxies: UntrustedProxy[] }>("/server/proxy-hint", { signal })
        : Promise.resolve({ proxies: [] }),
    [config.data],
  );
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [error, setError] = useState<unknown>();
  const [saving, setSaving] = useState(false);
  const urlError = validateCustomUrls(draft?.plex.customUrls);
  const shown = urlError ? { ...errors, "plex.customUrls": urlError } : errors;

  async function save() {
    if (!draft || !saved) return;
    if (urlError) {
      const fields = [{ field: "plex.customUrls", message: urlError }];
      setErrors({ "plex.customUrls": urlError });
      setError(new ApiError(400, "validation", t("Please fix the highlighted fields"), fields));
      return;
    }
    const serverDirty = JSON.stringify(draft.server) !== JSON.stringify(saved.server);
    const plexDirty = JSON.stringify(draft.plex) !== JSON.stringify(saved.plex);
    setSaving(true);
    setError(undefined);
    setErrors({});
    try {
      if (serverDirty) await api.putSection("server", draft.server);
      if (plexDirty) await api.putSection("plex", draft.plex);
      toast.success(t("Settings saved"), { description: t("Away from home") });
      await config.reload();
    } catch (err) {
      if (err instanceof ApiError && err.fields) {
        const fields = err.fields.map((f) => ({
          field: f.field,
          message: translatePlexFieldError(f),
        }));
        setErrors(Object.fromEntries(fields.map((f) => [f.field, f.message])));
        setError(new ApiError(err.status, err.code, err.message, fields));
      } else setError(err);
    } finally {
      setSaving(false);
    }
  }

  if (config.error) return <ErrorPanel error={config.error} onRetry={() => void config.reload()} />;
  if (!draft)
    return (
      <div className="py-6 text-center">
        <Spinner size={18} label={t("Loading settings")} />
      </div>
    );
  const fields = (list: FieldDef[] | undefined, section: "plex" | "server") =>
    (list ?? []).map((field) => (
      <SchemaField
        key={`${section}.${field.key}`}
        def={field}
        value={draft[section][field.key]}
        path={`${section}.${field.key}`}
        error={shown[`${section}.${field.key}`]}
        errors={shown}
        onChange={(next) =>
          setDraft({ ...draft, [section]: { ...draft[section], [field.key]: next } })
        }
      />
    ));
  const hasError = (list: FieldDef[] | undefined, section: "plex" | "server") =>
    (list ?? []).some((f) => Boolean(shown[`${section}.${f.key}`]));
  const trustedList = Array.isArray(draft.server.trustedProxyCIDRs)
    ? (draft.server.trustedProxyCIDRs as string[])
    : [];
  // A proxy the draft already trusts (by name or address) needs no hint.
  const untrusted = (hint.data?.proxies ?? []).filter(
    (p) => !trustedList.includes(p.address) && !(p.name && trustedList.includes(p.name)),
  );
  return (
    <SettingsSection title={address.title} description={address.description}>
      <MoreOptions
        setting={address.id}
        label={t("More options: {title}", { title: address.title ?? "" })}
        text={t("Address and port")}
        hasError={hasError(address.more, "plex")}
      >
        {fields(address.more, "plex")}
      </MoreOptions>
      {untrusted.map((p) => {
        const entry = p.name || p.address;
        return (
          <Alert key={p.address} variant="warning" role="status">
            <AlertTitle>{t("A reverse proxy is not trusted yet")}</AlertTitle>
            <AlertDescription>
              <p>
                {p.name
                  ? t(
                      "{name} ({address}) is forwarding visitor addresses, but Ether does not trust it. Every viewer behind it looks like they are at home, so limits for viewers away from home do not apply. Trust it only if it is your own reverse proxy.",
                      { name: p.name, address: p.address },
                    )
                  : t(
                      "{address} is forwarding visitor addresses, but Ether does not trust it. Every viewer behind it looks like they are at home, so limits for viewers away from home do not apply. Trust it only if it is your own reverse proxy.",
                      { address: p.address },
                    )}
              </p>
              <Button
                type="button"
                size="sm"
                className="mt-2"
                onClick={() =>
                  setDraft({
                    ...draft,
                    server: { ...draft.server, trustedProxyCIDRs: [...trustedList, entry] },
                  })
                }
              >
                {t("Trust {entry}", { entry })}
              </Button>
            </AlertDescription>
          </Alert>
        );
      })}
      <MoreOptions
        setting={proxy.id}
        label={t("Reverse proxy")}
        text={t("Reverse proxy")}
        hasError={hasError(proxy.more, "server")}
      >
        {fields(proxy.more, "server")}
      </MoreOptions>
      <MoreOptions
        setting={certificate.id}
        label={t("Use your own certificate")}
        text={t("Use your own certificate")}
        hasError={hasError(certificate.more, "plex")}
      >
        {fields(certificate.more, "plex")}
      </MoreOptions>
      <SaveBar
        dirty={dirty}
        saving={saving}
        error={error}
        onSave={save}
        onDiscard={() => {
          reset();
          setErrors({});
          setError(undefined);
        }}
      />
    </SettingsSection>
  );
}
