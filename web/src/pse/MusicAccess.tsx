import { useEffect, useState } from "react";
import { Button, Input } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
interface Access {
  id: number;
  name: string;
  username: string;
  enabled: boolean;
}
const endpoint = "/pse/music/access";

export function MusicAccess() {
  const [access, setAccess] = useState<Access[]>([]);
  const [secrets, setSecrets] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const refresh = () => apiFetch<Access[]>(endpoint).then(setAccess);
  useEffect(() => {
    void refresh().catch(() =>
      setError(L("Accès musicaux indisponibles", "Music access unavailable")),
    );
  }, []);
  async function change(id: number, method: "POST" | "DELETE") {
    setBusy(true);
    setError("");
    try {
      const result = await apiFetch<{ password?: string }>(endpoint, {
        method,
        body: JSON.stringify({ viewer: id }),
      });
      setSecrets((previous) => {
        const next = { ...previous };
        delete next[id];
        if (result.password) next[id] = result.password;
        return next;
      });
      await refresh();
    } catch {
      setError(L("Impossible de modifier cet accès musical", "Could not change music access"));
    } finally {
      setBusy(false);
    }
  }
  return (
    <details className="border-t border-border px-4 py-3">
      <summary className="cursor-pointer text-sm font-medium">
        {L("Clients musicaux · Arpeggi / Subsonic", "Music clients · Arpeggi / Subsonic")}
      </summary>
      <div className="mt-3 space-y-3 text-sm">
        <p className="text-fg-muted">
          {L(
            "Activez la musique, puis générez un accès pour le spectateur concerné. Dans Arpeggi, choisissez Subsonic et recopiez l’adresse du serveur, l’identifiant musical et le mot de passe généré. Cet accès est réservé à l’API musicale et conserve les droits du spectateur.",
            "Enable music, then generate access for the relevant viewer. In Arpeggi, select Subsonic and copy the server address, music username and generated password. This access is limited to the music API and retains the viewer’s permissions.",
          )}
        </p>
        <label className="block">
          {L("Adresse du serveur", "Server address")}
          <Input readOnly value={window.location.origin} />
        </label>
        <p className="text-fg-muted">
          {L(
            "Un accès par spectateur, utilisable sur plusieurs clients avec authentification par jeton. Remplacer ou révoquer l’accès déconnecte ces clients. Le mot de passe est affiché uniquement après sa création ; il ne remplace pas celui de la console ou de Plex.",
            "One access per viewer, usable on multiple token-authenticated clients. Replacing or revoking it disconnects those clients. The password is shown only after creation; it does not replace the console or Plex password.",
          )}
        </p>
        {error && (
          <p role="alert" className="text-danger">
            {error}
          </p>
        )}
        {access.map((v) => (
          <div key={v.id} className="space-y-2 rounded-lg border border-border p-3">
            <div className="font-medium">
              {v.name} · {v.enabled ? L("Actif", "Active") : L("Non configuré", "Not configured")}
            </div>
            <label className="block">
              {L("Identifiant musical", "Music username")}
              <Input readOnly value={v.username} />
            </label>
            {secrets[v.id] && (
              <label className="block">
                {L("Mot de passe musical — à conserver maintenant", "Music password — save it now")}
                <Input
                  readOnly
                  type="password"
                  autoComplete="off"
                  value={secrets[v.id]}
                  onFocus={(e) => e.currentTarget.select()}
                />
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => {
                    void navigator.clipboard
                      .writeText(secrets[v.id]!)
                      .catch(() =>
                        setError(
                          L(
                            "Copie impossible : sélectionnez le champ et copiez-le manuellement",
                            "Copy failed: select the field and copy it manually",
                          ),
                        ),
                      );
                  }}
                >
                  {L("Copier le mot de passe", "Copy password")}
                </Button>
              </label>
            )}
            <div className="flex flex-wrap gap-2">
              <Button disabled={busy} size="sm" onClick={() => void change(v.id, "POST")}>
                {v.enabled
                  ? L("Remplacer l’accès", "Replace access")
                  : L("Créer l’accès", "Create access")}
              </Button>
              {v.enabled && (
                <Button
                  disabled={busy}
                  variant="outline"
                  size="sm"
                  onClick={() => void change(v.id, "DELETE")}
                >
                  {L("Révoquer", "Revoke")}
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
    </details>
  );
}
