import { Checkbox, Select } from "@braedonsaunders/appkit-ui";
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
  const selected = value.split(",").filter(Boolean);
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
    return { ...o, name, category, contents, contentLabel, enabled: ix?.enabled ?? true };
  });
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
              <th className="p-2">{L("Sélection explicite", "Explicit selection")}</th>
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
                      checked={selected.includes(row.value)}
                      onChange={(event) => {
                        const next = event.currentTarget.checked
                          ? [...selected, row.value]
                          : selected.filter((id) => id !== row.value);
                        onChange(
                          choices
                            .map((o) => o.value)
                            .filter((id) => next.includes(id))
                            .join(","),
                        );
                      }}
                    />
                    {row.name}
                  </label>
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
          "Sans sélection explicite : tous les addons Stremio activés. Les indexeurs BitTorrent/Usenet nécessitent une sélection. Pour arrêter toutes les recherches, désactivez l’indexation. Les contenus disponibles dépendent de chaque source.",
          "Without an explicit selection: every enabled Stremio addon. BitTorrent/Usenet indexers must be selected. Disable indexing to stop all searches. Available content depends on each source.",
        )}
      </p>
    </div>
  );
}
