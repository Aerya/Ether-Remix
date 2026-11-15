import { Input, Switch } from "@braedonsaunders/appkit-ui";
import { useId, useState } from "react";
import { SecretInput } from "../components/fields";
import { getLocale } from "../i18n";
import { importProxyAddress } from "../lib/proxy-address";
import type { SourceProxy } from "../lib/types";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
export function SourceProxyFields({
  title,
  value,
  onChange,
}: {
  title: string;
  value?: SourceProxy;
  onChange: (p: SourceProxy) => void;
}) {
  const id = useId();
  const [error, setError] = useState("");
  const p = value ?? { enabled: false, url: "", username: "", password: "" };
  const patch = (v: Partial<SourceProxy>) => onChange({ ...p, ...v });
  return (
    <details className="rounded-lg border border-border p-3">
      <summary className="cursor-pointer font-medium">
        {title} · {p.enabled ? L("Personnalisé", "Custom") : L("Hérité", "Inherited")}
      </summary>
      <div className="mt-3 space-y-3">
        <label className="flex items-center justify-between gap-3">
          {L("Utiliser un proxy spécifique", "Use a dedicated proxy")}
          <Switch
            aria-label={title}
            checked={p.enabled}
            onChange={(e) => patch({ enabled: e.currentTarget.checked })}
          />
        </label>
        <p className="text-sm text-fg-muted">
          {L(
            "Désactivé : conserve les réglages globaux et MediaFlow. Activé : ce proxy prend priorité, sans repli direct ni exceptions de destination. HTTP, HTTPS ou SOCKS5 ; le VPN reste géré par votre service externe.",
            "Off: inherit global and MediaFlow settings. On: this proxy takes precedence, with no direct fallback or destination bypass. HTTP, HTTPS or SOCKS5; your external service manages the VPN.",
          )}
        </p>
        {p.enabled && (
          <>
            <label htmlFor={id + "url"}>{L("Adresse du proxy", "Proxy address")}</label>
            <Input
              id={id + "url"}
              value={p.url}
              placeholder="http://proxy:8888"
              onChange={(e) => {
                patch({ url: e.target.value });
                setError("");
              }}
              onPaste={(e) => {
                try {
                  const imported = importProxyAddress(e.clipboardData.getData("text"));
                  if (imported) {
                    e.preventDefault();
                    patch(imported);
                    setError("");
                  }
                } catch {
                  e.preventDefault();
                  setError(
                    L(
                      "URL invalide : vérifiez le protocole, le port et les identifiants.",
                      "Invalid URL: check scheme, port and credentials.",
                    ),
                  );
                }
              }}
            />
            {error && (
              <p role="alert" className="text-danger">
                {error}
              </p>
            )}
            <label htmlFor={id + "user"}>{L("Utilisateur du proxy", "Proxy username")}</label>
            <Input
              id={id + "user"}
              autoComplete="off"
              value={p.username ?? ""}
              onChange={(e) => patch({ username: e.target.value })}
            />
            <label htmlFor={id + "password"}>{L("Mot de passe du proxy", "Proxy password")}</label>
            <SecretInput
              id={id + "password"}
              value={p.password ?? ""}
              onChange={(password) => patch({ password })}
            />
          </>
        )}
      </div>
    </details>
  );
}
