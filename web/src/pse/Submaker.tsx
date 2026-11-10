import { useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { getLocale } from "../i18n";
import type { ThemeData } from "./ThemeLibraries";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
type Rule = { scope: string; target: string; enabled: boolean };
type Scopes = { global: boolean; rules: Rule[] };
type Data = {
  scopes: Scopes;
  jobs: { id: string; release: string; state: string; error?: string }[];
};
export function Submaker() {
  const data = useAsync(
    () => Promise.all([apiFetch<Data>("/pse/submaker/"), apiFetch<ThemeData>("/pse/themes/")]),
    [],
  );
  const [draft, setDraft] = useState<Scopes | null>(null),
    [message, setMessage] = useState(""),
    [busy, setBusy] = useState(false);
  if (data.error) return <p role="alert">{String(data.error)}</p>;
  if (!data.data) return <p>{L("Chargement…", "Loading…")}</p>;
  const [saved, themes] = data.data,
    c = draft ?? { ...saved.scopes, rules: saved.scopes.rules ?? [] };
  async function act(test = false) {
    setBusy(true);
    setMessage("");
    try {
      if (test) {
        const r = await apiFetch<{ message: string }>("/pse/submaker/test", { method: "POST" });
        setMessage(r.message);
      } else {
        await apiFetch("/pse/submaker/", { method: "PUT", body: JSON.stringify(c) });
        setMessage(L("Périmètre enregistré.", "Scope saved."));
      }
    } catch (e) {
      setMessage(String(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-4 p-4">
      <p className="text-sm text-fg-muted">
        {L(
          "Configurez SubMaker sur l’instance de votre choix, puis collez son manifeste personnel ci-dessus. Les réglages du fournisseur se sauvegardent avec le bouton Enregistrer de cette page.",
          "Configure SubMaker on your chosen instance, then paste its personal manifest above. Save provider settings with this page’s Save button.",
        )}
      </p>
      <div className="flex flex-wrap gap-4 text-sm underline">
        <a href="https://submaker.elfhosted.com/configure" target="_blank" rel="noreferrer">
          {L("Instance publique", "Public instance")}
        </a>
        <a href="https://github.com/xtremexq/StremioSubMaker" target="_blank" rel="noreferrer">
          {L("GitHub / auto-hébergement", "GitHub / self-hosting")}
        </a>
      </div>
      <label className="flex items-center gap-2">
        <input
          type="checkbox"
          checked={c.global}
          onChange={(e) => setDraft({ ...c, global: e.target.checked })}
        />
        {L(
          "Autoriser globalement (si SubMaker est activé)",
          "Allow globally (when SubMaker is enabled)",
        )}
      </label>
      <p className="text-sm text-fg-muted">
        {L(
          "Exceptions : collection > bibliothèque > global. À priorité égale, la première règle gagne. Un nouveau titre SeerrCatalog sans bibliothèque connue suit le réglage global.",
          "Overrides: collection > library > global. At equal priority, the first rule wins. A new SeerrCatalog title without known membership uses the global setting.",
        )}
      </p>
      {(c.rules ?? []).map((r, i) => (
        <div className="flex flex-wrap items-center gap-2" key={i}>
          <select
            className="rounded border border-border bg-surface p-2"
            aria-label={L("Périmètre", "Scope")}
            value={r.scope}
            onChange={(e) =>
              setDraft({
                ...c,
                rules: c.rules.map((x, n) =>
                  n === i ? { ...x, scope: e.target.value, target: "" } : x,
                ),
              })
            }
          >
            <option value="library">{L("Bibliothèque", "Library")}</option>
            <option value="collection">Collection</option>
          </select>
          <select
            className="min-w-48 max-w-full rounded border border-border bg-surface p-2"
            aria-label={L("Cible", "Target")}
            value={r.target}
            onChange={(e) =>
              setDraft({
                ...c,
                rules: c.rules.map((x, n) => (n === i ? { ...x, target: e.target.value } : x)),
              })
            }
          >
            <option value="">{L("Choisir…", "Choose…")}</option>
            {(r.scope === "library" ? themes.libraries : themes.collections).map((x) => (
              <option key={x.id} value={String(x.id)}>
                {x.name}
              </option>
            ))}
          </select>
          <select
            className="rounded border border-border bg-surface p-2"
            aria-label={L("Autorisation", "Permission")}
            value={r.enabled ? "on" : "off"}
            onChange={(e) =>
              setDraft({
                ...c,
                rules: c.rules.map((x, n) =>
                  n === i ? { ...x, enabled: e.target.value === "on" } : x,
                ),
              })
            }
          >
            <option value="on">{L("Activer", "Enable")}</option>
            <option value="off">{L("Désactiver", "Disable")}</option>
          </select>
          <Button onClick={() => setDraft({ ...c, rules: c.rules.filter((_, n) => n !== i) })}>
            {L("Retirer", "Remove")}
          </Button>
        </div>
      ))}
      <div className="flex flex-wrap gap-2">
        <Button
          onClick={() =>
            setDraft({ ...c, rules: [...c.rules, { scope: "library", target: "", enabled: true }] })
          }
        >
          {L("Ajouter une exception", "Add override")}
        </Button>
        <Button disabled={busy} onClick={() => act()}>
          {L("Enregistrer le périmètre", "Save scope")}
        </Button>
        <Button disabled={busy} onClick={() => act(true)}>
          {L("Tester le manifeste enregistré", "Test saved manifest")}
        </Button>
      </div>
      <p role="status" className="text-sm">
        {message}
      </p>
      <p className="text-sm text-fg-muted">
        {L(
          "Une tâche à la fois, deux releases maximum par recherche. Correspondance exacte du nom de release exigée. Les traductions ne sont disponibles qu’après téléchargement et contrôle des timecodes. Une lecture déjà ouverte doit être relancée.",
          "One job at a time, at most two releases per search. Exact release filename match required. Translations become available after download and timestamp checks. Restart an already open playback.",
        )}
      </p>
      <details>
        <summary>
          {L("Dernières recherches / traductions", "Recent searches / translations")}
        </summary>
        <ul className="mt-2 space-y-2 text-sm">
          {saved.jobs.map((j) => (
            <li key={j.id}>
              <strong>{j.state}</strong> — {j.release}
              {j.error && <span className="block text-fg-muted">{j.error}</span>}
            </li>
          ))}
        </ul>
        <p className="text-sm">
          {L(
            "Rouvrez cette section pour actualiser les tâches.",
            "Reopen this section to refresh jobs.",
          )}
        </p>
      </details>
    </div>
  );
}
