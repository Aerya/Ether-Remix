import { getLocale } from "../i18n";
import type { SourceProxy } from "../lib/types";
import { SourceProxyFields } from "./SourceProxyFields";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const off: SourceProxy = { enabled: false, url: "", username: "", password: "" };
const norm = (p?: SourceProxy) => {
  const v = p ?? off;
  return v.enabled ? [true, v.url, v.username ?? "", v.password ?? ""].join("\u0000") : "off";
};
export const sameProxy = (a?: SourceProxy, b?: SourceProxy) => norm(a) === norm(b);
// Un bloc unique remplit le proxy des requêtes et celui de la lecture d'un addon Stremio.
export function AddonProxyFields({
  requestProxy,
  streamProxy,
  onChange,
}: {
  requestProxy?: SourceProxy;
  streamProxy?: SourceProxy;
  onChange: (v: { requestProxy?: SourceProxy; streamProxy?: SourceProxy }) => void;
}) {
  const same = sameProxy(requestProxy, streamProxy);
  return (
    <>
      <SourceProxyFields
        title={L("Proxy de l’addon · requêtes et lecture", "Addon proxy · requests and playback")}
        value={same ? requestProxy : undefined}
        onChange={(p) => onChange({ requestProxy: p, streamProxy: { ...p } })}
      />
      {!same && (
        <p role="status" className="text-sm text-warning">
          {L(
            "Les proxys des requêtes et de la lecture diffèrent : voir les réglages séparés.",
            "Request and playback proxies differ: see the separate settings.",
          )}
        </p>
      )}
      <p className="text-sm text-fg-muted">
        {L(
          "Le proxy de lecture concerne les flux récupérés par Remix. Pour un torrent résolu par un compte débrideur de Remix, le proxy du compte s’applique. Les appels effectués par un addon distant restent sous son contrôle.",
          "The playback proxy applies to streams fetched by Remix. Torrents resolved through a Remix debrid account use that account’s proxy. Calls made by a remote addon remain under its control.",
        )}
      </p>
      <details className="rounded-lg border border-border p-3">
        <summary className="cursor-pointer font-medium">
          {L("Réglages séparés (avancé)", "Separate settings (advanced)")}
        </summary>
        <div className="mt-3 space-y-3">
          <SourceProxyFields
            title={L(
              "Proxy des requêtes · manifeste et recherches",
              "Request proxy · manifest and searches",
            )}
            value={requestProxy}
            onChange={(p) => onChange({ requestProxy: p })}
          />
          <SourceProxyFields
            title={L(
              "Proxy de lecture · flux HTTP de cet addon",
              "Playback proxy · this addon’s HTTP streams",
            )}
            value={streamProxy}
            onChange={(p) => onChange({ streamProxy: p })}
          />
        </div>
      </details>
    </>
  );
}
