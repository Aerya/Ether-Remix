import { Button } from "@braedonsaunders/appkit-ui";
import { FlaskConical } from "lucide-react";
import { useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { preFeedLabel, type PreFeedStat } from "./PreStats";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const num = (n: number) => n.toLocaleString(getLocale());

interface Run extends Omit<PreFeedStat, "seen" | "indexed" | "pending" | "unmatched"> {
  sample?: string[];
}
interface Result {
  proxy?: string;
  runs: Run[];
}

/** Réglages que le test lit tels que saisis, sans attendre l’enregistrement. */
export const PRE_TEST_KEYS = [
  "PSE_PREDB_PUBLIC",
  "PSE_PREDB_LANGS",
  "PSE_PREDB_FEEDS",
  "PSE_PREDB_PROXY_TYPE",
  "PSE_PREDB_PROXY_URL",
  "PSE_PREDB_PROXY_AUTH",
] as const;

/**
 * Bouton « Tester les flux » : lit chaque flux maintenant avec les réglages affichés (même
 * non enregistrés) et montre, pour chacun, le résultat, les annonces reçues et retenues et
 * quelques exemples, ou l’erreur rencontrée. Rien n’est enregistré.
 */
export function PreTest({ values }: { values: Record<string, string> }) {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<Result | null>(null);
  const [message, setMessage] = useState("");
  async function run() {
    setBusy(true);
    setMessage("");
    setResult(null);
    const body: Record<string, string> = {};
    for (const key of PRE_TEST_KEYS) if (values[key] !== undefined) body[key] = values[key]!;
    try {
      const res = await apiFetch<{ ok: boolean; message?: string; result?: Result }>(
        "/pse/predb/test",
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ values: body }),
        },
      );
      if (res.ok && res.result) setResult(res.result);
      else setMessage(res.message || L("Test impossible.", "Test failed."));
    } catch (e) {
      setMessage(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }
  const okCount = result?.runs.filter((r) => r.state === "ok").length ?? 0;
  return (
    <section
      className="space-y-3 rounded-xl border border-border p-4 text-sm"
      aria-label={L("Test des flux", "Feed test")}
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="font-semibold">{L("Tester les flux", "Test the feeds")}</h2>
          <p className="text-fg-muted">
            {L(
              "Lit chaque flux maintenant avec les réglages affichés, même non enregistrés, et montre ce que Remix en retient. Rien n’est enregistré.",
              "Reads each feed now with the settings shown, even unsaved, and shows what Remix keeps. Nothing is saved.",
            )}
          </p>
        </div>
        <Button onClick={() => void run()} disabled={busy}>
          <FlaskConical
            className={`mr-1.5 h-4 w-4 ${busy ? "animate-pulse" : ""}`}
            aria-hidden="true"
          />
          {busy ? L("Test en cours…", "Testing…") : L("Tester les flux", "Test the feeds")}
        </Button>
      </div>
      {message ? (
        <p role="alert" className="text-warning">
          {message}
        </p>
      ) : null}
      {result ? (
        <>
          {result.proxy ? (
            <p role="alert" className="text-warning">
              {L("Proxy : ", "Proxy: ")}
              {result.proxy}
            </p>
          ) : null}
          <p role="status">
            {result.runs.length === 0
              ? L(
                  "Aucun flux à lire : activez les flux publics ou ajoutez un site.",
                  "No feed to read: enable public feeds or add a site.",
                )
              : L(
                  `${okCount} flux sur ${result.runs.length} lisible(s).`,
                  `${okCount} of ${result.runs.length} feed(s) readable.`,
                )}
          </p>
          {result.runs.length ? (
            <div className="overflow-x-auto rounded-xl border border-border">
              <table className="w-full min-w-[640px]">
                <caption className="sr-only">{L("Résultat du test", "Test result")}</caption>
                <thead>
                  <tr className="border-b border-border text-xs text-fg-muted">
                    <th scope="col" className="px-4 py-3 text-left font-medium">
                      {L("Flux", "Feed")}
                    </th>
                    <th scope="col" className="px-4 py-3 text-left font-medium">
                      {L("Résultat", "Result")}
                    </th>
                    <th scope="col" className="px-4 py-3 text-right font-medium">
                      {L("Annonces", "Announcements")}
                    </th>
                    <th scope="col" className="px-4 py-3 text-right font-medium">
                      {L("Retenues", "Kept")}
                    </th>
                    <th scope="col" className="px-4 py-3 text-left font-medium">
                      {L("Exemples", "Examples")}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {result.runs.map((r) => (
                    <tr key={r.feed} className="border-b border-border last:border-0 align-top">
                      <th scope="row" className="px-4 py-3 text-left font-medium">
                        {preFeedLabel(r.feed, r.label)}
                      </th>
                      <td
                        className={`px-4 py-3 ${r.state === "ok" ? "text-success" : "text-warning"}`}
                      >
                        {r.state === "ok" ? "OK" : `${L("Erreur", "Error")} · ${r.error ?? ""}`}
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        {r.state === "ok" ? num(r.fetched ?? 0) : "—"}
                      </td>
                      <td className="px-4 py-3 text-right tabular-nums">
                        {r.state === "ok" ? num(r.kept ?? 0) : "—"}
                      </td>
                      <td className="px-4 py-3 text-xs text-fg-muted">
                        {r.sample?.length
                          ? r.sample.join(" · ")
                          : r.state === "ok"
                            ? L(
                                "Aucune annonce retenue à cet instant",
                                "No announcement kept right now",
                              )
                            : ""}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
