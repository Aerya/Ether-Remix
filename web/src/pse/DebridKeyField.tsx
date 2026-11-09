import { Button, Input } from "@braedonsaunders/appkit-ui";
import { Eye, EyeOff } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { SecretInput } from "../components/fields";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export function DebridKeyField({
  providerId,
  kind,
  value,
  onChange,
  ariaLabel,
  invalid,
}: {
  providerId: string;
  kind: string;
  value: string;
  onChange: (value: string) => void;
  ariaLabel: string;
  invalid?: boolean;
}) {
  const [revealed, setRevealed] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  const request = useRef<AbortController | null>(null);
  useEffect(() => {
    setRevealed(null);
    setError(false);
    setBusy(false);
    return () => {
      request.current?.abort();
    };
  }, [providerId, kind, value]);
  const hide = () => {
    request.current?.abort();
    setRevealed(null);
    setBusy(false);
  };
  const reveal = async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setBusy(true);
    setError(false);
    try {
      const result = await apiFetch<{ key: string }>(
        `/debrid/providers/${encodeURIComponent(providerId)}/key`,
        {
          method: "POST",
          body: JSON.stringify({ kind }),
          signal: controller.signal,
        },
      );
      if (!controller.signal.aborted) setRevealed(result.key);
    } catch {
      if (!controller.signal.aborted) setError(true);
    } finally {
      if (!controller.signal.aborted) setBusy(false);
    }
  };
  return (
    <div className="w-full space-y-2">
      {revealed !== null ? (
        <Input
          aria-label={ariaLabel}
          type="text"
          value={revealed}
          readOnly
          autoComplete="off"
          spellCheck={false}
        />
      ) : (
        <SecretInput ariaLabel={ariaLabel} value={value} invalid={invalid} onChange={onChange} />
      )}
      {value === "••••" ? (
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={busy}
          aria-pressed={revealed !== null}
          onClick={revealed === null ? () => void reveal() : hide}
        >
          {revealed === null ? <Eye size={14} /> : <EyeOff size={14} />}
          {revealed === null
            ? L("Afficher la clé API", "Show API key")
            : L("Masquer la clé API", "Hide API key")}
        </Button>
      ) : null}
      {error ? (
        <p role="alert" className="text-sm text-danger">
          {L(
            "Impossible d’afficher la clé. Vérifiez le compte et votre connexion propriétaire.",
            "Could not show the key. Check the account and your owner session.",
          )}
        </p>
      ) : null}
    </div>
  );
}
