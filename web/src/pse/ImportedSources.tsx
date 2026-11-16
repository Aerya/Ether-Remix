import { indexerOrigin } from "./indexerOrigin";
import { Button, Checkbox, Select } from "@braedonsaunders/appkit-ui";
import { useState } from "react";
import { api } from "../lib/api";
import { useAsync } from "../lib/hooks";
import { getLocale } from "../i18n";
const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);
type Option = { value: string; label: { fr: string; en: string } };
export function ImportedSources({
  value,
  options,
  onChange,
}: {
  value: string;
  options: Option[];
  onChange: (value: string) => void;
}) {
  const cfg = useAsync(() => api.getConfig(), []);
  const [filter, setFilter] = useState("all");
  const [sort, setSort] = useState<"name" | "category" | "content">("category");
  const [content, setContent] = useState("all");
  const automatic = value.trim() === "";
  const selected = value.split(",").filter((id) => id && id !== "__none__");
  const choices: Option[] = cfg.data?.indexers
    ? [
        ...cfg.data.indexers.map((ix) => ({
          value: ix.id,
          label: { fr: ix.name || ix.id, en: ix.name || ix.id },
        })),
        ...selected
          .filter((id) => !cfg.data!.indexers!.some((ix) => ix.id === id))
          .map(
            (id) => options.find((o) => o.value === id) ?? { value: id, label: { fr: id, en: id } },
          ),
      ]
    : options;
  const allRows = choices.map((o) => {
    const ix = cfg.data?.indexers?.find((ix) => ix.id === o.value);
    const name = ix?.name || (getLocale().startsWith("fr") ? o.label.fr : o.label.en);
    const category =
      ix?.kind === "stremio" || /stremio/i.test(name)
        ? "Stremio"
        : ix?.protocol === "torrent"
          ? "BitTorrent"
          : ix
            ? "Usenet"
            : L("Autres indexeurs", "Other indexers");
    const contents = ["movie", "show", "anime", ...(category === "BitTorrent" ? ["music"] : [])];
    const contentLabel =
      L("Films · Séries · Animes", "Movies · Shows · Anime") +
      (contents.includes("music") ? L(" · Albums via debrid", " · Albums via debrid") : "");
    return {
      ...o,
      name,
      category,
      contents,
      contentLabel,
      origin: ix ? indexerOrigin(ix).label : "",
      enabled: ix?.enabled ?? true,
    };
  });
  const effective = automatic
    ? allRows.filter((r) => r.enabled && r.category === "Stremio").map((r) => r.value)
    : selected;
  const saveSelection = (ids: string[]) => onChange(ids.length ? ids.join(",") : "__none__");
  const rows = allRows
    .filter(
      (r) =>
        (filter === "all" || r.category === filter) &&
        (content === "all" || r.contents.includes(content)),
    )
    .sort(
      (a, b) =>
        (sort === "category"
          ? a.category.localeCompare(b.category)
          : sort === "content"
            ? a.contentLabel.localeCompare(b.contentLabel)
            : 0) || a.name.localeCompare(b.name),
    );
  return (
    <div className="w-full space-y-3">
      <Select
        aria-label={L("Mode de sélection", "Selection mode")}
        value={automatic ? "auto" : "custom"}
        onChange={(e) => (e.target.value === "auto" ? onChange("") : saveSelection(effective))}
      >
        <option value="auto">
          {L("Automatique — addons Stremio activés", "Automatic — enabled Stremio addons")}
        </option>
        <option value="custom">
          {L("Personnalisé — choisir mes sources", "Custom — choose my sources")}
        </option>
      </Select>
      <p className="text-sm">
        {L(
          `${allRows.filter((r) => r.enabled && effective.includes(r.value)).length} source(s) active(s) utilisée(s) pour l’indexation`,
          `${allRows.filter((r) => r.enabled && effective.includes(r.value)).length} enabled source(s) used for indexing`,
        )}
      </p>
      {automatic ? (
        <p className="text-xs text-fg-muted">
          {L(
            "Les cases montrent les addons utilisés automatiquement, y compris les futurs addons activés. Passez en mode Personnalisé pour sélectionner aussi Prowlarr, Jackett ou d’autres indexeurs.",
            "The checkboxes show automatically used addons, including future enabled addons. Switch to Custom to also select Prowlarr, Jackett or other indexers.",
          )}
        </p>
      ) : (
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            variant="outline"
            onClick={() => saveSelection(allRows.filter((r) => r.enabled).map((r) => r.value))}
          >
            {L("Toutes les sources actuelles", "All current sources")}
          </Button>
          <Button size="sm" variant="outline" onClick={() => saveSelection([])}>
            {L("Aucune source", "No sources")}
          </Button>
        </div>
      )}
      <div className="flex flex-wrap gap-2">
        <Select
          aria-label={L("Catégorie de source", "Source category")}
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        >
          <option value="all">{L("Toutes les catégories", "All categories")}</option>
          {[...new Set(allRows.map((r) => r.category))].map((c) => (
            <option key={c}>{c}</option>
          ))}
        </Select>
        <Select
          aria-label={L("Trier les sources", "Sort sources")}
          value={sort}
          onChange={(e) => setSort(e.target.value as "name" | "category" | "content")}
        >
          <option value="category">{L("Par catégorie puis nom", "Category then name")}</option>
          <option value="name">{L("Par nom", "Name")}</option>
          <option value="content">
            {L("Par contenus concernés puis nom", "Content then name")}
          </option>
        </Select>
        <Select
          aria-label={L("Contenus concernés", "Source content")}
          value={content}
          onChange={(e) => setContent(e.target.value)}
        >
          <option value="all">{L("Tous les contenus", "All content")}</option>
          <option value="movie">{L("Films", "Movies")}</option>
          <option value="show">{L("Séries", "Shows")}</option>
          <option value="anime">{L("Animes", "Anime")}</option>
          <option value="music">{L("Albums via debrid", "Albums via debrid")}</option>
        </Select>
      </div>
      {choices.length === 0 ? (
        <p className="text-sm text-fg-muted">
          {L(
            "Aucun indexeur configuré. Ajoutez vos addons ou indexeurs dans les autres catégories de Sources.",
            "No indexers configured. Add addons or indexers in the other Sources categories.",
          )}
        </p>
      ) : null}
      <div className="overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="border-b border-border">
              <th className="p-2">{L("Utiliser pour l’indexation", "Use for indexing")}</th>
              <th className="p-2">{L("Catégorie", "Category")}</th>
              <th className="p-2">{L("Contenus concernés", "Content")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.value} className="border-b border-border">
                <td className="p-2">
                  <label className="flex items-center gap-2">
                    <Checkbox
                      checked={effective.includes(row.value)}
                      disabled={automatic || !row.enabled}
                      onChange={(event) => {
                        const next = event.currentTarget.checked
                          ? [...selected, row.value]
                          : selected.filter((id) => id !== row.value);
                        saveSelection(
                          choices.map((o) => o.value).filter((id) => next.includes(id)),
                        );
                      }}
                    />
                    {row.name}
                  </label>
                  <span className="text-xs text-fg-muted">{row.origin}</span>
                  {!row.enabled ? (
                    <span className="text-xs text-fg-muted">
                      {L("Désactivée dans les sources", "Disabled in sources")}
                    </span>
                  ) : null}
                </td>
                <td className="p-2">{row.category}</td>
                <td className="p-2 text-xs text-fg-muted">{row.contentLabel}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-xs text-fg-muted">
        {L(
          "En mode personnalisé, aucune case cochée signifie aucune source interrogée. Les sources non choisies restent disponibles à la lecture. Pensez à enregistrer vos modifications.",
          "In Custom mode, no checked boxes means no source is queried. Unselected sources remain available for playback. Remember to save your changes.",
        )}
      </p>
    </div>
  );
}
