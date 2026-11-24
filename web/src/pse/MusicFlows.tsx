import { Button, Input, toast } from "@braedonsaunders/appkit-ui";
import { useEffect, useState } from "react";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
const ENDPOINT = "/pse/music/playlists/flows";
interface Flow {
  ref: string;
  viewer: number;
  name: string;
  mode: string;
  hours: number;
  size: number;
  enabled: boolean;
  url?: string;
  seed?: number;
  revision: number;
  succeeded?: number;
  attempted?: number;
  matched?: number;
  missing?: number;
  error?: string;
}
interface Data {
  flows: Flow[];
  audioMuse: boolean;
  busy: boolean;
}
const fresh = (viewer: number): Flow => ({
  ref: "",
  viewer,
  name: "",
  mode: "mix",
  hours: 24,
  size: 30,
  enabled: true,
  revision: 0,
});

export function MusicFlows({ viewers }: { viewers: { id: number; name: string }[] }) {
  const [viewer, setViewer] = useState(viewers[0]?.id ?? 0);
  const [data, setData] = useState<Data | null>(null);
  const [draft, setDraft] = useState<Flow>(() => fresh(viewer));
  const [seeds, setSeeds] = useState<{ id: number; name: string }[]>([]);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    setData(null);
    setDraft(fresh(viewer));
    setSeeds([]);
    setError("");
    if (!viewer) return;
    const load = () =>
      apiFetch<Data>(`${ENDPOINT}?viewer=${viewer}`)
        .then((v) => {
          if (active) {
            setData(v);
            setError("");
          }
        })
        .catch(() => {
          if (active) setError(L("Chargement impossible", "Could not load playlists"));
        });
    void load();
    const timer = window.setInterval(() => void load(), 3000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [viewer]);
  useEffect(() => {
    let active = true;
    if (draft.mode === "audiomuse" && viewer)
      void apiFetch<{ id: number; name: string }[]>(`${ENDPOINT}/seeds?viewer=${viewer}`)
        .then((v) => {
          if (active) setSeeds(v);
        })
        .catch(() => {
          if (active) setError(L("Titres locaux indisponibles", "Local tracks unavailable"));
        });
    return () => {
      active = false;
    };
  }, [draft.mode, viewer]);
  const send = async (method: string, flow: Flow, path = "") => {
    setPending(true);
    try {
      await apiFetch(ENDPOINT + path, {
        method,
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(flow),
      });
      const next = await apiFetch<Data>(`${ENDPOINT}?viewer=${viewer}`);
      setData(next);
      if (method === "PUT" || method === "DELETE") setDraft(fresh(viewer));
      toast.success(L("Demande enregistrée", "Request saved"));
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setPending(false);
    }
  };
  const modeName = (mode: string) =>
    mode === "mix"
      ? L("Mix personnel", "Personal mix")
      : mode === "playlist"
        ? L("Playlist liée", "Linked playlist")
        : "AudioMuse";
  return (
    <section className="space-y-3" aria-label={L("Playlists programmées", "Scheduled playlists")}>
      <p className="text-sm text-fg-muted">
        {L(
          "Un mix renouvelé de vos favoris et écoutes, ou une playlist qui suit une source Deezer, Spotify ou ListenBrainz. Le dernier contenu est conservé en cas d’échec. Aucun téléchargement audio n’est lancé.",
          "A rotating mix of your favourites and listening history, or a playlist following a Deezer, Spotify or ListenBrainz source. The last contents are preserved on failure. No audio downloads are started.",
        )}
      </p>
      <label className="block text-sm">
        {L("Spectateur", "Viewer")}{" "}
        <select
          className="rounded border border-border bg-bg p-2"
          value={viewer}
          disabled={pending}
          onChange={(e) => setViewer(Number(e.target.value))}
        >
          {viewers.map((v) => (
            <option key={v.id} value={v.id}>
              {v.name}
            </option>
          ))}
        </select>
      </label>
      {error && <p role="alert">{error}</p>}
      {!viewer ? (
        <p>{L("Ajoutez d’abord un spectateur.", "Add a viewer first.")}</p>
      ) : (
        <>
          {data?.flows.map((f) => (
            <div key={f.ref} className="space-y-2 rounded border border-border p-3">
              <p className="font-medium">
                {f.name} · {modeName(f.mode)} ·{" "}
                {f.enabled ? `${f.hours} h` : L("En pause", "Paused")}
              </p>
              <p className="text-xs text-fg-muted">
                {L(
                  `${f.matched ?? 0} titre(s) retenu(s), ${f.missing ?? 0} introuvable(s)`,
                  `${f.matched ?? 0} matched, ${f.missing ?? 0} missing`,
                )}
                {f.succeeded
                  ? ` · ${new Date(f.succeeded * 1000).toLocaleString(getLocale())}`
                  : ` · ${L("Pas encore actualisée", "Not refreshed yet")}`}
              </p>
              {f.error && (
                <p role="alert" className="text-sm text-warning">
                  {L(
                    f.error,
                    "Refresh failed; previous contents retained. Check the source and accessible tracks.",
                  )}
                </p>
              )}
              <div className="flex flex-wrap gap-2">
                <Button size="sm" variant="outline" disabled={pending} onClick={() => setDraft(f)}>
                  {L("Modifier", "Edit")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={pending || data.busy}
                  onClick={() => void send("POST", f, "/run")}
                >
                  {data.busy
                    ? L("Actualisation en cours…", "Refreshing…")
                    : L("Actualiser", "Refresh")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={pending}
                  onClick={() => void send("PUT", { ...f, enabled: !f.enabled })}
                >
                  {f.enabled ? L("Suspendre", "Pause") : L("Reprendre", "Resume")}
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={pending}
                  onClick={() => {
                    if (
                      window.confirm(
                        L(
                          "Supprimer cette playlist programmée et sa liste générée ? Les fichiers audio sont conservés.",
                          "Delete this schedule and its generated playlist? Audio files are preserved.",
                        ),
                      )
                    )
                      void send("DELETE", f);
                  }}
                >
                  {L("Supprimer", "Delete")}
                </Button>
              </div>
            </div>
          ))}
          <form
            className="space-y-3 rounded border border-border p-3"
            onSubmit={(e) => {
              e.preventDefault();
              void send("PUT", draft);
            }}
          >
            <p className="font-medium">
              {draft.ref
                ? L("Modifier la programmation", "Edit schedule")
                : L("Nouvelle playlist programmée", "New scheduled playlist")}
            </p>
            <label className="block text-sm">
              {L("Nom", "Name")}
              <Input
                required
                maxLength={120}
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
            </label>
            <label className="block text-sm">
              {L("Type", "Type")}{" "}
              <select
                className="rounded border border-border bg-bg p-2"
                value={draft.mode}
                onChange={(e) => setDraft({ ...draft, mode: e.target.value })}
              >
                <option value="mix">{modeName("mix")}</option>
                <option value="playlist">{modeName("playlist")}</option>
                <option value="audiomuse" disabled={!data?.audioMuse}>
                  AudioMuse
                </option>
              </select>
            </label>
            {draft.mode === "mix" && (
              <p className="text-xs text-fg-muted">
                {L(
                  "Alterne favoris, écoutes récentes et favoris peu écoutés, avec une nouvelle sélection à chaque période. Les Découvertes et Nouveautés restent disponibles dans vos playlists automatiques.",
                  "Interleaves favourites, recent listens and seldom-played favourites, rotating each period. Discoveries and New releases remain available in your automatic playlists.",
                )}
              </p>
            )}
            {draft.mode === "playlist" && (
              <label className="block text-sm">
                {L("Lien de la playlist", "Playlist URL")}
                <Input
                  required
                  type="url"
                  maxLength={2048}
                  value={draft.url ?? ""}
                  onChange={(e) => setDraft({ ...draft, url: e.target.value })}
                />
                <span className="text-xs text-fg-muted">
                  {L(
                    "Remplace la liste générée à chaque succès, dans l’ordre de la source et dans la limite choisie. Spotify nécessite un compte lié.",
                    "Replaces generated contents on success, in source order up to the selected size. Spotify requires a linked account.",
                  )}
                </span>
              </label>
            )}
            {draft.mode === "audiomuse" && (
              <label className="block text-sm">
                {L("Titre local de départ", "Local seed track")}{" "}
                <select
                  required
                  className="max-w-full rounded border border-border bg-bg p-2"
                  value={draft.seed ?? ""}
                  onChange={(e) => setDraft({ ...draft, seed: Number(e.target.value) })}
                >
                  <option value="">{L("Choisir un titre", "Choose a track")}</option>
                  {seeds.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </select>
                <p className="text-xs text-fg-muted">
                  {L(
                    "AudioMuse doit avoir analysé cette bibliothèque Remix. Seules les recommandations locales accessibles sont retenues. Le connecteur ne déclenche aucune analyse.",
                    "AudioMuse must have analysed this Remix library. Only accessible local recommendations are kept. The connector never starts an analysis.",
                  )}
                </p>
              </label>
            )}
            <div className="flex flex-wrap gap-3">
              <label className="text-sm">
                {L("Cadence (heures)", "Interval (hours)")}
                <Input
                  required
                  type="number"
                  min={1}
                  max={168}
                  value={draft.hours}
                  onChange={(e) => setDraft({ ...draft, hours: Number(e.target.value) })}
                />
              </label>
              <label className="text-sm">
                {L("Nombre maximal de titres", "Maximum tracks")}
                <Input
                  required
                  type="number"
                  min={1}
                  max={200}
                  value={draft.size}
                  onChange={(e) => setDraft({ ...draft, size: Number(e.target.value) })}
                />
              </label>
            </div>
            <label className="block text-sm">
              <input
                type="checkbox"
                checked={draft.enabled}
                onChange={(e) => setDraft({ ...draft, enabled: e.target.checked })}
              />{" "}
              {L("Actualisation automatique", "Automatic refresh")}
            </label>
            <p className="text-xs text-fg-muted">
              {L(
                "Vérification toutes les dix minutes lorsque la musique est active. Après enregistrement, utilisez Actualiser pour lancer immédiatement la première sélection.",
                "Schedules are checked every ten minutes while music is enabled. After saving, use Refresh to generate the first selection immediately.",
              )}
            </p>
            <div className="flex gap-2">
              <Button size="sm" disabled={pending || !data} type="submit">
                {L("Enregistrer la programmation", "Save schedule")}
              </Button>
              {draft.ref && (
                <Button
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => setDraft(fresh(viewer))}
                >
                  {L("Annuler", "Cancel")}
                </Button>
              )}
            </div>
          </form>
          {!data?.audioMuse && (
            <p className="text-xs text-fg-muted">
              {L(
                "AudioMuse est facultatif. Sa connexion se configure sur le serveur ; consultez le guide des playlists programmées.",
                "AudioMuse is optional. Configure its connection on the server; see the scheduled playlists guide.",
              )}
            </p>
          )}
        </>
      )}
    </section>
  );
}
