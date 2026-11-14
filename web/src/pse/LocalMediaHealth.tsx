import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Button } from "@braedonsaunders/appkit-ui";
import { apiFetch } from "../lib/api";
import { getLocale } from "../i18n";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
type FileHealth = {
  id: number;
  path: string;
  name: string;
  state: string;
  size: number;
  unidentified: boolean;
  duplicate: boolean;
};
type RootHealth = {
  id: string;
  name: string;
  kind: string;
  state: string;
  bytes: number;
  knownBytes: number;
  files: FileHealth[];
};
type Health = { checked: number; roots: RootHealth[] };
const label = (state: string) =>
  ({
    available: L("Disponible", "Available"),
    missing: L("Absent", "Missing"),
    permission: L("Permission refusée", "Permission denied"),
    inaccessible: L("Inaccessible", "Inaccessible"),
    unavailable: L("Non vérifiable : dossier indisponible", "Cannot check: folder unavailable"),
    changed: L("Modifié : analyse nécessaire", "Changed: scan required"),
  })[state] ?? state;
const bytes = (n: number) => {
  const unit = n > 0 ? Math.min(4, Math.floor(Math.log(n) / Math.log(1024))) : 0;
  return (
    new Intl.NumberFormat(getLocale(), { maximumFractionDigits: 2 }).format(n / 1024 ** unit) +
    " " +
    ["B", "KiB", "MiB", "GiB", "TiB"][unit]
  );
};
export function LocalMediaHealth() {
  const [data, setData] = useState<Health>();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [filter, setFilter] = useState("all");
  const [query, setQuery] = useState("");
  const [rootID, setRootID] = useState("");
  const [page, setPage] = useState(0);
  async function refresh() {
    setBusy(true);
    setError("");
    try {
      setData(await apiFetch<Health>("/pse/owned/local/health"));
      setPage(0);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  }
  useEffect(() => {
    void refresh();
  }, []);
  const roots = data?.roots ?? [];
  const files = roots
    .flatMap((root) => root.files.map((file) => ({ ...file, root })))
    .filter(
      (f) =>
        (!rootID || f.root.id === rootID) &&
        (f.name + " " + f.path).toLowerCase().includes(query.toLowerCase()) &&
        (filter === "all" ||
          (filter === "unidentified"
            ? f.unidentified
            : filter === "duplicate"
              ? f.duplicate
              : f.state === filter)),
    )
    .sort((a, b) => b.size - a.size || a.path.localeCompare(b.path));
  return (
    <section className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold">
          {L("Santé des médias locaux", "Local media health")}
        </h2>
        <Button disabled={busy} onClick={() => void refresh()}>
          {busy
            ? L("Vérification…", "Checking…")
            : L("Actualiser le diagnostic", "Refresh diagnostics")}
        </Button>
      </div>
      <p>
        {L(
          "Les entrées absentes restent au catalogue, même si un montage devient vide. Aucun fichier n’est supprimé. Les vidéos sans correspondance peuvent être des vidéos personnelles ; les doublons sont seulement des candidats.",
          "Missing entries stay in the catalogue, even when a mount becomes empty. No files are deleted. Unidentified videos may be home videos; duplicates are only candidates.",
        )}
      </p>
      <Link className="underline" to="/remix/sources?source=local">
        {L("Configurer les dossiers et lancer une analyse", "Configure folders and run a scan")}
      </Link>
      {error && <p role="alert">{error}</p>}
      {data && (
        <>
          <p className="text-sm text-fg-muted">
            {L("Diagnostic du", "Checked at")}{" "}
            {new Date(data.checked * 1000).toLocaleString(getLocale())} ·{" "}
            {L("Taille des médias accessibles", "Accessible media size")} :{" "}
            {bytes(roots.reduce((s, r) => s + r.bytes, 0))}
          </p>
          <p className="text-sm text-fg-muted">
            {L(
              "Tailles logiques par dossier source : les liens physiques peuvent être comptés plusieurs fois. Ce n’est pas l’espace libre du disque. Les tailles connues incluent les fichiers absents ; seuls les fichiers déjà indexés sont listés.",
              "Logical sizes per source folder: hardlinks may be counted more than once. This is not free disk space. Known sizes include missing files; only previously indexed files are listed.",
            )}
          </p>
          {!roots.length && (
            <p>{L("Aucun dossier local configuré.", "No local folders configured.")}</p>
          )}
          <div className="grid gap-3 sm:grid-cols-2">
            {roots.map((r) => (
              <div key={r.id} className="rounded border border-border p-3">
                <strong>{r.name}</strong> · {label(r.state)}
                <p>
                  {r.files.filter((f) => f.state !== "available").length}{" "}
                  {L("fichiers à vérifier", "files to check")}
                </p>
                <p>
                  {r.files.length} {L("fichiers indexés", "indexed files")} · {bytes(r.bytes)}{" "}
                  {L("accessibles", "accessible")} / {bytes(r.knownBytes)} {L("connus", "known")}
                </p>
                {r.state !== "available" && (
                  <p role="status">
                    {L(
                      "Vérifiez le volume Docker, le montage réseau et les permissions. Catalogue conservé.",
                      "Check the Docker volume, network mount and permissions. Catalogue preserved.",
                    )}
                  </p>
                )}
              </div>
            ))}
          </div>
          <div className="flex flex-wrap gap-3">
            <label>
              {L("Dossier", "Folder")}{" "}
              <select
                className="rounded border border-border bg-surface p-2"
                value={rootID}
                onChange={(e) => {
                  setRootID(e.target.value);
                  setPage(0);
                }}
              >
                <option value="">{L("Tous", "All")}</option>
                {roots.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              {L("État", "State")}{" "}
              <select
                className="rounded border border-border bg-surface p-2"
                value={filter}
                onChange={(e) => {
                  setFilter(e.target.value);
                  setPage(0);
                }}
              >
                <option value="all">{L("Tous", "All")}</option>
                {[
                  "available",
                  "missing",
                  "permission",
                  "inaccessible",
                  "unavailable",
                  "changed",
                ].map((s) => (
                  <option key={s} value={s}>
                    {label(s)}
                  </option>
                ))}
                <option value="unidentified">
                  {L("Non identifié (vidéo)", "Unidentified (video)")}
                </option>
                <option value="duplicate">{L("Doublon potentiel", "Potential duplicate")}</option>
              </select>
            </label>
            <label>
              {L("Rechercher", "Search")}{" "}
              <input
                value={query}
                onChange={(e) => {
                  setQuery(e.target.value);
                  setPage(0);
                }}
                className="rounded border border-border bg-surface p-1"
              />
            </label>
          </div>
          <p>
            {files.length} {L("résultats, du plus lourd au plus léger", "results, largest first")}
          </p>
          <div className="overflow-auto">
            <table className="w-full text-sm">
              <thead>
                <tr>
                  {[
                    L("Fichier", "File"),
                    L("Dossier", "Folder"),
                    L("État", "State"),
                    L("Taille", "Size"),
                  ].map((s) => (
                    <th key={s} className="p-2 text-left">
                      {s}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {files.slice(page * 50, page * 50 + 50).map((f) => (
                  <tr key={f.id} className="border-t border-border">
                    <td className="p-2">
                      <strong>{f.name}</strong>
                      <p className="break-all text-fg-muted">{f.path}</p>
                    </td>
                    <td className="p-2">{f.root.name}</td>
                    <td className="p-2">
                      {label(f.state)}
                      {f.unidentified && (
                        <p>{L("Non identifié (vidéo)", "Unidentified (video)")}</p>
                      )}
                      {f.duplicate && <p>{L("Doublon potentiel", "Potential duplicate")}</p>}
                    </td>
                    <td className="p-2">{bytes(f.size)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="flex gap-3">
            <Button disabled={page === 0} onClick={() => setPage(page - 1)}>
              {L("Précédent", "Previous")}
            </Button>
            <Button disabled={(page + 1) * 50 >= files.length} onClick={() => setPage(page + 1)}>
              {L("Suivant", "Next")}
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
