import { Button, SettingsSection, Spinner, toast } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { SettingsRow } from "../components/SettingsRow";
import { getLocale } from "../i18n";
import { apiFetch, errorMessage } from "../lib/api";
import { useAsync } from "../lib/hooks";
import {
  DEFAULT_STREMIO_OPTIONS,
  StremioLinkChoice,
  chosenStremioLink,
  type StremioLinks,
  type StremioOptions,
} from "./StremioLinkChoice";

/**
 * Carte Nuvio d'un spectateur (Connexion et appareils), sous Trakt : liaison à
 * un profil Nuvio, reprise et historique synchronisés dans les deux sens, et
 * installation du lien Stremio du spectateur dans Nuvio
 * (internal/pse/nuvio). Voir docs/pse/FORK.fr.md.
 */

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

type Login = { code: string; webUrl: string; expiresAt: string; interval: number };
type Profile = { id: string; profileIndex: number; name: string };
type NuvioLink = {
  profileIndex: number;
  profileName: string;
  chosen: boolean;
  pull: boolean;
  push: boolean;
  lastCycle: number;
  imported: number;
  sent: number;
  unknown: number;
  lastError?: string;
};
export type NuvioStatus = {
  linked: boolean;
  link?: NuvioLink;
  profiles?: Profile[];
  login?: Login;
  status?: string;
  error?: string;
};
type Connection = StremioLinks & { url: string };

const api = (id: number, tail = "") => `/pse/viewers/${id}/nuvio${tail}`;
const JSON_HEADERS = { "Content-Type": "application/json" };

/** Seul un lien nuvio.tv en https est proposé à l'ouverture. */
export function safeNuvioUrl(raw?: string): string | null {
  if (!raw) return null;
  try {
    const u = new URL(raw);
    const host = u.hostname.toLowerCase();
    if (u.protocol === "https:" && (host === "nuvio.tv" || host.endsWith(".nuvio.tv"))) {
      return u.href;
    }
  } catch {
    // lien refusé
  }
  return null;
}

function when(ts: number): string {
  if (!ts) return L("jamais", "never");
  return new Date(ts * 1000).toLocaleString(getLocale());
}

export function ViewerNuvio({ viewerId }: { viewerId: number }) {
  const status = useAsync((signal) => apiFetch<NuvioStatus>(api(viewerId), { signal }), [viewerId]);
  const [login, setLogin] = useState<Login | null>(null);
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<string | null>(null);
  const [profile, setProfile] = useState<number | null>(null);
  const [pull, setPull] = useState<boolean | null>(null);
  const [push, setPush] = useState<boolean | null>(null);
  const [connection, setConnection] = useState<Connection | null>(null);
  const [filesOnly, setFilesOnly] = useState(false);
  const [options, setOptions] = useState<StremioOptions>(DEFAULT_STREMIO_OPTIONS);

  const st = status.data;
  const link = st?.link;
  const chosen = Boolean(link?.chosen);

  useEffect(() => {
    if (!login) return;
    let cancelled = false;
    let timer: number | undefined;
    const tick = async () => {
      try {
        const next = await apiFetch<NuvioStatus>(api(viewerId, "/poll"), { method: "POST" });
        if (cancelled) return;
        if (next.linked) {
          status.setData(next);
          setLogin(null);
          toast.success(L("Compte Nuvio relié.", "Nuvio account linked."));
          return;
        }
        if (next.status && next.status !== "pending") {
          setLogin(null);
          setOutcome(next.status);
          return;
        }
      } catch {
        if (cancelled) return;
      }
      timer = window.setTimeout(() => void tick(), Math.max(login.interval || 3, 2) * 1000);
    };
    timer = window.setTimeout(() => void tick(), Math.max(login.interval || 3, 2) * 1000);
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearTimeout(timer);
    };
  }, [login, viewerId]);

  const run = async (fn: () => Promise<void>, failure: string) => {
    setBusy(true);
    try {
      await fn();
    } catch (e) {
      toast.error(failure, { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  const start = () =>
    run(
      async () => {
        setOutcome(null);
        const next = await apiFetch<NuvioStatus>(api(viewerId, "/start"), { method: "POST" });
        setLogin(next.login ?? null);
      },
      L("Connexion à Nuvio impossible", "Could not start the Nuvio link"),
    );

  const save = (patch: { profile?: number; pull?: boolean; push?: boolean }) =>
    run(
      async () => {
        const index = patch.profile ?? profile ?? link?.profileIndex;
        if (index === undefined) return;
        const next = await apiFetch<NuvioStatus>(api(viewerId), {
          method: "PUT",
          headers: JSON_HEADERS,
          body: JSON.stringify({
            profileIndex: index,
            pull: patch.pull ?? pull ?? link?.pull ?? true,
            push: patch.push ?? push ?? link?.push ?? true,
          }),
        });
        status.setData(next);
        setProfile(null);
        setPull(null);
        setPush(null);
      },
      L("Nuvio n'a pas été mis à jour", "Nuvio was not updated"),
    );

  const sync = () =>
    run(
      async () => {
        const before = status.data?.link?.lastCycle ?? 0;
        const res = await apiFetch<{ running: boolean; status: NuvioStatus }>(
          api(viewerId, "/sync"),
          { method: "POST" },
        );
        status.setData(res.status);
        toast.success(
          L(
            "Récupération lancée en arrière-plan : les compteurs se mettront à jour à la fin.",
            "Fetch started in the background: the counters update when it ends.",
          ),
        );
        for (let i = 0; i < 180; i++) {
          await new Promise((r) => setTimeout(r, 5000));
          const st = await apiFetch<NuvioStatus>(api(viewerId));
          status.setData(st);
          if ((st.link?.lastCycle ?? 0) !== before) break;
        }
      },
      L("Synchronisation impossible", "Sync failed"),
    );

  const unlink = () =>
    run(
      async () => {
        await apiFetch(api(viewerId), { method: "DELETE" });
        setConnection(null);
        setLogin(null);
        status.setData({ linked: false });
        toast.success(L("Nuvio délié.", "Nuvio unlinked."));
      },
      L("Nuvio n'a pas été délié", "Nuvio was not unlinked"),
    );

  const prepareLink = () =>
    run(
      async () => {
        setConnection(
          await apiFetch<Connection>(`/viewers/${viewerId}/apps/stremio`, { method: "POST" }),
        );
      },
      L("Lien Stremio indisponible", "Stremio link unavailable"),
    );

  const shown = connection ? chosenStremioLink(connection, filesOnly, options) : null;

  const install = () =>
    run(
      async () => {
        if (!shown) return;
        await apiFetch(api(viewerId, "/install-addon"), {
          method: "POST",
          headers: JSON_HEADERS,
          body: JSON.stringify({ url: shown.url }),
        });
        toast.success(L("Ether installé dans Nuvio.", "Ether installed in Nuvio."));
      },
      L("Installation dans Nuvio impossible", "Could not install in Nuvio"),
    );

  if (status.error) {
    return (
      <SettingsSection title="Nuvio">
        <p role="alert" className="text-sm text-fg-muted">
          {L("Nuvio est indisponible.", "Nuvio is unavailable.")}
        </p>
      </SettingsSection>
    );
  }
  if (!st) {
    return (
      <div className="py-6 text-center">
        <Spinner size={18} label={L("Chargement de Nuvio", "Loading Nuvio")} />
      </div>
    );
  }

  const webUrl = safeNuvioUrl(login?.webUrl);
  const profiles = st.profiles ?? [];

  return (
    <SettingsSection
      title="Nuvio"
      description={L(
        "Synchronise la reprise et l'historique de ce spectateur avec un profil Nuvio.",
        "Syncs this viewer's resume points and history with a Nuvio profile.",
      )}
    >
      {!st.linked ? (
        <>
          <SettingsRow
            title={L("Connexion", "Connection")}
            description={L("Non relié", "Not linked")}
            control={
              <Button variant="outline" disabled={busy} onClick={() => void start()}>
                {L("Relier", "Link")}
              </Button>
            }
          />
          {login ? (
            <div className="space-y-3 rounded-xl border border-border bg-bg-subtle p-5">
              <p className="text-xs font-medium tracking-wide text-fg-muted uppercase">
                {L("Code de connexion", "Sign-in code")}
              </p>
              <code className="font-mono text-2xl font-semibold tracking-[0.2em] text-fg">
                {login.code}
              </code>
              <p className="text-sm text-fg">
                {L("Approuvez la connexion sur ", "Approve the sign-in on ")}
                {webUrl ? (
                  <a
                    href={webUrl}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="font-medium text-primary hover:underline"
                  >
                    {webUrl}
                  </a>
                ) : (
                  "nuvio.tv/tv-login"
                )}
              </p>
              <div className="flex items-center gap-2 text-sm text-fg-muted" role="status">
                <Spinner size={14} label={L("En attente de Nuvio…", "Waiting for Nuvio…")} />
                <span>
                  {L("En attente de l'approbation sur Nuvio…", "Waiting for approval on Nuvio…")}
                </span>
              </div>
            </div>
          ) : null}
          {outcome ? (
            <p role="alert" className="text-sm text-fg-muted">
              {L(
                "La connexion a expiré ou a été refusée. Relancez la liaison.",
                "The sign-in expired or was declined. Start the link again.",
              )}
            </p>
          ) : null}
        </>
      ) : (
        <>
          <SettingsRow
            title={L("Profil Nuvio", "Nuvio profile")}
            description={
              chosen
                ? L(`Relié à « ${link?.profileName} »`, `Linked to “${link?.profileName}”`)
                : L("Choisissez le profil à synchroniser.", "Choose the profile to sync.")
            }
            control={
              <select
                aria-label={L("Profil Nuvio", "Nuvio profile")}
                className="rounded-md border border-border bg-transparent px-2 py-1"
                disabled={busy || profiles.length === 0}
                value={profile ?? (chosen ? link?.profileIndex : "") ?? ""}
                onChange={(e) => {
                  const index = Number(e.target.value);
                  setProfile(index);
                  void save({ profile: index });
                }}
              >
                {!chosen ? <option value="">{L("— Choisir —", "— Choose —")}</option> : null}
                {profiles.map((p) => (
                  <option key={p.profileIndex} value={p.profileIndex}>
                    {p.name || `${L("Profil", "Profile")} ${p.profileIndex}`}
                  </option>
                ))}
              </select>
            }
          />
          {chosen && link ? (
            <>
              <SettingsRow
                title={L("Nuvio → Ether", "Nuvio → Ether")}
                description={L(
                  "Reprend la progression et les titres vus dans Nuvio.",
                  "Brings Nuvio's progress and watched titles into Ether.",
                )}
                control={
                  <input
                    type="checkbox"
                    aria-label="Nuvio → Ether"
                    checked={pull ?? link.pull}
                    disabled={busy}
                    onChange={(e) => {
                      setPull(e.target.checked);
                      void save({ pull: e.target.checked });
                    }}
                  />
                }
              />
              <SettingsRow
                title={L("Ether → Nuvio", "Ether → Nuvio")}
                description={L(
                  "Envoie à Nuvio les lectures arrêtées et les titres vus dans les autres lecteurs.",
                  "Sends stopped playbacks and watched titles from other players to Nuvio.",
                )}
                control={
                  <input
                    type="checkbox"
                    aria-label="Ether → Nuvio"
                    checked={push ?? link.push}
                    disabled={busy}
                    onChange={(e) => {
                      setPush(e.target.checked);
                      void save({ push: e.target.checked });
                    }}
                  />
                }
              />
              <SettingsRow
                title={L("État", "Status")}
                description={
                  <span className="block space-y-1">
                    <span className="block">
                      {L("Dernier cycle", "Last cycle")} : {when(link.lastCycle)}
                    </span>
                    <span className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
                      <span>
                        <strong className="text-fg">{link.imported}</strong>{" "}
                        {L("importés", "imported")}
                      </span>
                      <span>
                        <strong className="text-fg">{link.sent}</strong> {L("envoyés", "sent")}
                      </span>
                      <span>
                        <strong className="text-fg">{link.unknown}</strong>{" "}
                        {L("non reconnus", "not recognised")}
                      </span>
                    </span>
                    {!link.pull ? (
                      <span className="block text-xs">
                        {L(
                          "Activez « Nuvio → Ether » pour récupérer l'historique.",
                          "Turn on “Nuvio → Ether” to fetch the history.",
                        )}
                      </span>
                    ) : null}
                    {link.lastError ? (
                      <span role="alert" className="block text-danger">
                        {link.lastError}
                      </span>
                    ) : null}
                  </span>
                }
                control={
                  <Button
                    variant="outline"
                    disabled={busy || !link.pull}
                    onClick={() => void sync()}
                  >
                    {L("Récupérer depuis Nuvio", "Fetch from Nuvio")}
                  </Button>
                }
              />
              <SettingsRow
                title={L("Lien Stremio", "Stremio link")}
                description={L(
                  "Installe Ether dans ce profil Nuvio avec le lien Stremio de ce spectateur.",
                  "Installs Ether in this Nuvio profile using this viewer's Stremio link.",
                )}
                control={
                  <Button variant="outline" disabled={busy} onClick={() => void prepareLink()}>
                    {L("Préparer le lien", "Prepare the link")}
                  </Button>
                }
              />
              {shown ? (
                <div className="space-y-4">
                  <StremioLinkChoice
                    connection={shown}
                    filesOnly={filesOnly}
                    onChange={setFilesOnly}
                    options={options}
                    onOptionsChange={setOptions}
                  />
                  {shown.qr ? (
                    <img
                      src={shown.qr}
                      alt={L("QR code du lien Stremio", "Stremio link QR code")}
                      width={160}
                      height={160}
                      className="rounded-md bg-white p-2"
                    />
                  ) : null}
                  <Button disabled={busy} onClick={() => void install()}>
                    {L("Installer dans Nuvio", "Install in Nuvio")}
                  </Button>
                </div>
              ) : null}
            </>
          ) : null}
          <SettingsRow
            title={L("Délier", "Unlink")}
            description={L(
              "Efface les jetons Nuvio de ce spectateur ; l'historique déjà importé reste.",
              "Deletes this viewer's Nuvio tokens; history already imported stays.",
            )}
            control={
              <Button variant="outline" disabled={busy} onClick={() => void unlink()}>
                {L("Délier", "Unlink")}
              </Button>
            }
          />
        </>
      )}
    </SettingsSection>
  );
}
