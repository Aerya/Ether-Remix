import type { CollectionSpec, Rule, TitleCard } from "../lib/contract";
import type {
  LibraryAvailableFacets,
  LibraryFacetOption,
  LibraryTab,
  LibraryTabKind,
  LibraryTabPreviewResponse,
  LibraryTabsResponse,
} from "../pages/libraries/api";
import { COLLECTIONS, KEYWORDS, NETWORKS, PEOPLE, STUDIOS, type FakeTitle } from "./data";

const SCOPE_NOTE =
  "Vela shows these as tabs. Plex, Jellyfin, Emby and Silo show the first rows tab as the library's Recommended page.";

const VALID_KINDS = new Set<LibraryTabKind>([
  "recommended",
  "library",
  "collections",
  "people",
  "view",
  "live",
]);

const CATALOGUE_SORTS = [
  "popularity",
  "added",
  "released",
  "year",
  "rating",
  "duration",
  "title",
  "lastViewed",
  "server",
];

const CATALOGUE_COLLECTION_FACETS = new Set([
  "",
  "genre",
  "studio",
  "network",
  "country",
  "language",
  "contentRating",
  "year",
  "decade",
  "franchise",
  "tag",
  "keyword",
  "director",
  "writer",
  "resolution",
  "hdr",
  "audioLanguage",
  "rating",
  "runtime",
  "addedMonth",
]);

const CATALOGUE_PEOPLE_FACETS = new Set(["cast", "director", "writer"]);

function decadeOf(year: number): string {
  return year ? `${Math.floor(year / 10) * 10}s` : "Unknown";
}

function validTabKey(key: string): boolean {
  return /^[a-z0-9][a-z0-9_-]{0,63}$/.test(key);
}

export function createLibraryTabsMock({
  catalog,
  getCollections = () => [],
  matchesRule = () => true,
  toCard,
}: {
  catalog: FakeTitle[];
  getCollections?: () => CollectionSpec[];
  matchesRule?: (title: FakeTitle, rule: Rule) => boolean;
  toCard?: (title: FakeTitle) => TitleCard;
}) {
  // Initial owner overrides so customized libraries can be inspected right away.
  const ownerTabs = new Map<number, LibraryTab[]>([
    [
      4,
      [
        {
          key: "library",
          label: "Weekend Picks",
          kind: "library",
          sort: "rating",
          direction: "desc",
          search: true,
        },
      ],
    ],
    [
      7,
      [
        { key: "recommended", label: "Recommendations", kind: "recommended" },
        {
          key: "library",
          label: "All Anime",
          kind: "library",
          sort: "rating",
          direction: "desc",
          search: true,
          searchPlaceholder: "Search anime",
        },
        {
          key: "studios",
          label: "Studios",
          kind: "collections",
          facet: "studio",
          order: "count",
          sort: "rating",
          direction: "desc",
          search: true,
          searchPlaceholder: "Find a studio",
        },
        {
          key: "cast",
          label: "Voice Cast",
          kind: "people",
          facet: "cast",
          order: "name",
          search: true,
          searchPlaceholder: "Find a voice actor",
        },
        {
          key: "unwatched",
          label: "Unwatched",
          kind: "view",
          sort: "added",
          direction: "desc",
          search: true,
          filters: {
            match: "all",
            rules: [
              { field: "viewer", op: "is", value: { userId: 0, unwatched: true } },
              { field: "rating", op: "gte", value: 7.5 },
            ],
          },
        },
        {
          key: "classics",
          label: "90s Classics",
          kind: "view",
          sort: "year",
          direction: "asc",
          search: true,
          hidden: true,
          filters: {
            match: "all",
            rules: [{ field: "year", op: "between", value: [1990, 1999] }],
          },
        },
      ],
    ],
  ]);

  function availableFacetsFor(lib: Record<string, unknown>): LibraryAvailableFacets {
    if (lib.source === "plugin") {
      const kind = lib.kind as
        | {
            fields?: { key: string; label: string; type: string; indexed?: boolean }[];
            browse?: { facets?: string[] };
          }
        | undefined;
      const collections: LibraryFacetOption[] = [];
      const people: LibraryFacetOption[] = [];
      const seenCol = new Set<string>();
      const seenPeople = new Set<string>();
      const fieldsByKey = new Map((kind?.fields ?? []).map((f) => [f.key, f]));

      const addCol = (id: string, label: string, category: "essentials" | "plugin" = "plugin") => {
        if (!seenCol.has(id)) {
          seenCol.add(id);
          collections.push({ id, label, category });
        }
      };
      const addPerson = (id: string, label: string) => {
        if (!seenPeople.has(id)) {
          seenPeople.add(id);
          people.push({ id, label, category: "people" });
        }
      };
      const addField = (f: { key: string; label: string; type: string }) => {
        const isPeople = f.type === "people" || f.key === "author" || f.key === "narrator";
        if (isPeople) {
          addPerson(f.key, f.label);
          return;
        }
        if (
          f.type === "string" ||
          f.type === "strings" ||
          f.type === "enum" ||
          f.type === "int" ||
          f.type === "float" ||
          f.type === "date" ||
          f.type === "bool" ||
          f.type === "duration"
        ) {
          addCol(f.key, f.label, "plugin");
          if (f.type === "date") {
            addCol(`${f.key}-decade`, `${f.label} (decade)`, "plugin");
          }
        }
      };

      for (const fk of kind?.browse?.facets ?? []) {
        if (fk === "year") addCol("year", "Year", "essentials");
        else if (fk === "decade") addCol("decade", "Decade", "essentials");
        else if (fk === "released") addCol("released", "Released", "essentials");
        else if (fk === "rating") addCol("rating", "Rating", "essentials");
        else {
          const fd = fieldsByKey.get(fk);
          if (fd) addField(fd);
        }
      }
      for (const f of kind?.fields ?? []) {
        addField(f);
      }
      addCol("year", "Year", "essentials");
      addCol("decade", "Decade", "essentials");
      addCol("released", "Released", "essentials");
      addCol("rating", "Rating", "essentials");
      return { collections, people };
    }

    const mediaType = String(lib.mediaType ?? "");
    const hasStudioData = catalog.some(
      (x) => (!mediaType || mediaType === "mixed" || x.type === mediaType) && Boolean(x.studio),
    );
    const collections: LibraryFacetOption[] = [
      { id: "", label: "Your collections", category: "essentials" },
      { id: "genre", label: "Genres", category: "essentials" },
    ];
    if (mediaType === "show") {
      collections.push({ id: "network", label: "Networks", category: "essentials" });
      if (hasStudioData) {
        collections.push({ id: "studio", label: "Studios", category: "essentials" });
      }
    } else if (mediaType === "movie") {
      collections.push({ id: "studio", label: "Studios", category: "essentials" });
      if (hasStudioData) {
        collections.push({ id: "network", label: "Networks", category: "essentials" });
      }
    } else {
      collections.push(
        { id: "studio", label: "Studios", category: "essentials" },
        { id: "network", label: "Networks", category: "essentials" },
      );
    }
    collections.push(
      { id: "country", label: "Countries", category: "essentials" },
      { id: "language", label: "Languages", category: "essentials" },
      { id: "contentRating", label: "Content ratings", category: "essentials" },
      { id: "year", label: "Years", category: "essentials" },
      { id: "decade", label: "Decades", category: "essentials" },
    );
    if (mediaType !== "show") {
      collections.push({ id: "franchise", label: "Franchises", category: "essentials" });
    }
    collections.push(
      { id: "tag", label: "Tags", category: "essentials" },
      { id: "keyword", label: "Keywords", category: "essentials" },
      { id: "rating", label: "Rating bands", category: "essentials" },
      { id: "runtime", label: "Runtime bands", category: "essentials" },
      { id: "addedMonth", label: "Added month", category: "essentials" },
      { id: "director", label: "Directors", category: "people" },
      { id: "writer", label: "Writers", category: "people" },
      { id: "resolution", label: "Resolutions", category: "technical" },
      { id: "hdr", label: "HDR", category: "technical" },
      { id: "audioLanguage", label: "Audio languages", category: "technical" },
    );
    return {
      collections,
      people: [
        { id: "cast", label: "Actors", category: "people" },
        { id: "director", label: "Directors", category: "people" },
        { id: "writer", label: "Writers", category: "people" },
      ],
    };
  }

  function availableSortsFor(lib: Record<string, unknown>): string[] {
    if (lib.source === "plugin") {
      const kind = lib.kind as { browse?: { sorts?: string[] } } | undefined;
      const base = kind?.browse?.sorts?.length ? [...kind.browse.sorts] : ["server"];
      if (!base.includes("lastViewed")) base.push("lastViewed");
      return base;
    }
    return [...CATALOGUE_SORTS];
  }

  function defaultTabsFor(lib: Record<string, unknown>): LibraryTab[] {
    if (lib.source === "plugin") {
      const name = String(lib.name ?? "Items");
      return [
        { key: "recommended", label: "Recommendations", kind: "recommended", search: false },
        {
          key: "library",
          label: name,
          kind: "library",
          sort: "title",
          direction: "asc",
          search: true,
          searchPlaceholder: `Search ${name.toLowerCase()}`,
        },
        {
          key: "subjects",
          label: "Subjects",
          kind: "collections",
          facet: "subject",
          order: "count",
          sort: "title",
          direction: "asc",
          search: true,
          searchPlaceholder: "Find a subject",
        },
        {
          key: "authors",
          label: "Authors",
          kind: "people",
          facet: "author",
          order: "name",
          search: true,
          searchPlaceholder: "Find an author",
        },
      ];
    }
    return [
      { key: "recommended", label: "Recommendations", kind: "recommended", search: false },
      {
        key: "library",
        label: "Library",
        kind: "library",
        search: true,
      },
    ];
  }

  function withEffectiveSearch(tabs: LibraryTab[]): LibraryTab[] {
    return tabs.map((t) => ({
      ...structuredClone(t),
      search: t.kind === "recommended" ? false : t.search !== false,
    }));
  }

  function getTabs(lib: Record<string, unknown>): LibraryTabsResponse {
    const id = Number(lib.id);
    const defaults = defaultTabsFor(lib);
    const customized = ownerTabs.has(id);
    const stored = customized ? structuredClone(ownerTabs.get(id) ?? []) : [];
    const effectiveTabs = customized ? withEffectiveSearch(stored) : structuredClone(defaults);
    return {
      libraryId: id,
      customized,
      tabs: stored,
      effectiveTabs,
      defaults,
      scopeNote: SCOPE_NOTE,
      availableFacets: availableFacetsFor(lib),
      availableSorts: availableSortsFor(lib),
    };
  }

  function validateTabList(
    lib: Record<string, unknown>,
    rawTabs: unknown,
  ): { ok: true; tabs: LibraryTab[] } | { ok: false; message: string } {
    if (!Array.isArray(rawTabs)) {
      return { ok: false, message: "Invalid tabs payload" };
    }
    if (rawTabs.length === 0) {
      return { ok: false, message: "invalid: library must have at least one tab" };
    }
    if (rawTabs.length > 50) {
      return { ok: false, message: "invalid: library may have at most 50 tabs" };
    }

    const isPlugin = lib.source === "plugin";
    const facets = availableFacetsFor(lib);
    const sorts = new Set(availableSortsFor(lib));
    const seen = new Set<string>();
    const normalized: LibraryTab[] = [];
    let visibleCount = 0;

    for (let i = 0; i < rawTabs.length; i++) {
      const raw = rawTabs[i] as Partial<LibraryTab> | null;
      if (!raw || typeof raw !== "object") {
        return { ok: false, message: `tab ${i + 1}: invalid tab` };
      }
      const label = String(raw.label ?? "").trim();
      if (!label || label.length > 80) {
        return { ok: false, message: `invalid: tab ${i + 1} needs a label (1-80 characters)` };
      }
      const key = String(raw.key ?? "")
        .trim()
        .toLowerCase();
      if (!validTabKey(key)) {
        return { ok: false, message: `invalid: invalid tab key "${key}"` };
      }
      if (seen.has(key)) {
        return { ok: false, message: `invalid: duplicate tab key "${key}"` };
      }
      seen.add(key);

      const kind = String(raw.kind ?? "")
        .trim()
        .toLowerCase() as LibraryTabKind;
      if (!VALID_KINDS.has(kind)) {
        return { ok: false, message: `invalid: tab "${key}" has unknown kind "${kind}"` };
      }
      if (kind === "live" && !isPlugin) {
        return {
          ok: false,
          message: `invalid: tab "${key}" of kind live is only supported on plugin libraries`,
        };
      }

      const facet = String(raw.facet ?? "").trim();
      if (kind === "collections") {
        if (isPlugin) {
          if (!facet || !facets.collections.some((f) => f.id === facet)) {
            return {
              ok: false,
              message: `invalid: plugin collections tab "${key}" requires a declared facet`,
            };
          }
        } else if (!CATALOGUE_COLLECTION_FACETS.has(facet)) {
          return {
            ok: false,
            message: `invalid: tab "${key}" has unsupported collections facet "${facet}"`,
          };
        }
      } else if (kind === "people") {
        if (isPlugin) {
          if (!facet || !facets.people.some((f) => f.id === facet)) {
            return {
              ok: false,
              message: `invalid: plugin people tab "${key}" requires a declared people facet`,
            };
          }
        } else if (!CATALOGUE_PEOPLE_FACETS.has(facet)) {
          return {
            ok: false,
            message: `invalid: tab "${key}" has unsupported people facet "${facet}"`,
          };
        }
      } else if (facet !== "") {
        return { ok: false, message: `invalid: tab "${key}" of kind ${kind} cannot set facet` };
      }

      const order = raw.order ? String(raw.order).trim().toLowerCase() : "";
      const allowsOrder = kind === "people" || (kind === "collections" && facet !== "");
      if (order) {
        if (!allowsOrder) {
          return { ok: false, message: `invalid: tab "${key}" cannot set order` };
        }
        if (order !== "count" && order !== "name") {
          return { ok: false, message: `invalid: tab "${key}" order must be count or name` };
        }
      }

      const sort = raw.sort ? String(raw.sort).trim() : "";
      const direction = raw.direction ? String(raw.direction).trim().toLowerCase() : "";
      const allowsSort =
        kind === "library" || kind === "view" || (kind === "collections" && facet !== "");
      if (!allowsSort) {
        if (sort || direction) {
          return { ok: false, message: `invalid: tab "${key}" cannot set sort or direction` };
        }
      } else {
        if (sort && !sorts.has(sort)) {
          return { ok: false, message: `invalid: tab "${key}" has unsupported sort "${sort}"` };
        }
        if (direction && direction !== "asc" && direction !== "desc") {
          return { ok: false, message: `invalid: tab "${key}" direction must be asc or desc` };
        }
      }

      if (kind === "recommended" && raw.search === true) {
        return {
          ok: false,
          message: `invalid: recommended tab "${key}" cannot enable in-tab search`,
        };
      }

      if (raw.filters && raw.filters.rules?.length && kind !== "view" && kind !== "library") {
        return { ok: false, message: `invalid: tab "${key}" of kind ${kind} cannot set filters` };
      }

      const tab: LibraryTab = { key, label, kind };
      if (kind === "collections" && facet) tab.facet = facet;
      if (kind === "people") tab.facet = facet;
      if (order === "count" || order === "name") tab.order = order;
      if (sort) tab.sort = sort;
      if (direction === "asc" || direction === "desc") tab.direction = direction;
      if (kind !== "recommended") {
        if (raw.search !== undefined) tab.search = Boolean(raw.search);
        const ph = String(raw.searchPlaceholder ?? "").trim();
        if (ph) tab.searchPlaceholder = ph;
      }
      if (kind === "live") {
        tab.card = raw.card === "poster" || raw.card === "square" ? raw.card : "landscape";
      }
      if (kind === "view") {
        tab.layout = raw.layout === "list" || raw.layout === "timeline" ? raw.layout : "grid";
      }
      if (raw.filters && raw.filters.rules?.length) {
        tab.filters = structuredClone(raw.filters);
      }
      if (raw.hidden) {
        tab.hidden = true;
      } else {
        visibleCount++;
      }
      normalized.push(tab);
    }

    if (visibleCount === 0) {
      return { ok: false, message: "invalid: at least one tab must be visible" };
    }
    return { ok: true, tabs: normalized };
  }

  function putTabs(
    lib: Record<string, unknown>,
    body: unknown,
  ): { ok: true; data: LibraryTabsResponse } | { ok: false; message: string } {
    const rawList = Array.isArray(body)
      ? body
      : body && typeof body === "object"
        ? (body as { tabs?: unknown }).tabs
        : undefined;
    const res = validateTabList(lib, rawList);
    if (!res.ok) return res;
    ownerTabs.set(Number(lib.id), res.tabs);
    return { ok: true, data: getTabs(lib) };
  }

  function resetTabs(lib: Record<string, unknown>): LibraryTabsResponse {
    ownerTabs.delete(Number(lib.id));
    return getTabs(lib);
  }

  function foldSearch(s: string): string {
    return s
      .normalize("NFD")
      .replace(/[\u0300-\u036f]/g, "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, " ")
      .trim();
  }

  function matchesFoldedPrefix(candidate: string, query: string): boolean {
    const qFold = foldSearch(query);
    if (!qFold) return candidate.toLowerCase().includes(query.toLowerCase());
    const cFold = foldSearch(candidate);
    const words = cFold.split(" ").filter(Boolean);
    const qTokens = qFold.split(" ").filter(Boolean);
    return qTokens.every((qt) => words.some((w) => w.startsWith(qt)));
  }

  function facetValuesFor(
    lib: Record<string, unknown>,
    field: string,
    opts?: {
      q?: string;
      offset?: number;
      limit?: number;
      order?: "count" | "name";
      declared?: boolean;
    },
  ): {
    field: string;
    values: { value: string; title?: string; count: number }[];
    truncated?: boolean;
    more?: boolean;
  } {
    if (lib.source === "plugin") {
      const extraAuthors: { value: string; count: number }[] = [
        "Octavia E. Butler",
        "N. K. Jemisin",
        "Iain M. Banks",
        "Dan Simmons",
        "Arthur C. Clarke",
        "Philip K. Dick",
        "Stanislaw Lem",
        "Ted Chiang",
        "Adrian Tchaikovsky",
        "Becky Chambers",
        "Arkady Martine",
        "Ann Leckie",
        "Martha Wells",
        "Liu Cixin",
        "Neal Stephenson",
        "Kim Stanley Robinson",
        "Gene Wolfe",
        "Lois McMaster Bujold",
        "Alastair Reynolds",
        "Peter F. Hamilton",
        "Vernor Vinge",
        "Greg Egan",
        "C. J. Cherryh",
        "Samuel R. Delany",
        "J. G. Ballard",
        "Ray Bradbury",
        "Robert A. Heinlein",
        "Larry Niven",
        "Joe Haldeman",
        "John Scalzi",
        "Andy Weir",
        "James S. A. Corey",
        "Jeff VanderMeer",
        "China Miéville",
        "Margaret Atwood",
        "Kazuo Ishiguro",
        "Kurt Vonnegut",
        "Douglas Adams",
        "Terry Pratchett",
        "Susanna Clarke",
        "Robin Hobb",
        "Brandon Sanderson",
        "Patrick Rothfuss",
        "José Saramago",
      ].map((name, idx) => ({ value: name, count: Math.max(1, 24 - Math.floor(idx / 2)) }));

      const pluginBuckets: Record<string, { value: string; title?: string; count: number }[]> = {
        author: [
          { value: "Ursula K. Le Guin", count: 28 },
          { value: "Isaac Asimov", count: 25 },
          { value: "William Gibson", count: 19 },
          { value: "Frank Herbert", count: 16 },
          { value: "Herbert", count: 1 },
          ...extraAuthors,
        ],
        narrator: [
          { value: "Simon Vance", count: 14 },
          { value: "Scott Brick", count: 11 },
          { value: "Robin Miles", count: 8 },
          { value: "Jefferson Mays", count: 7 },
          { value: "Ray Porter", count: 6 },
        ],
        subject: [
          { value: "Science Fiction", count: 42 },
          { value: "Space Opera", count: 27 },
          { value: "Cyberpunk", count: 18 },
          { value: "Classic Literature", count: 14 },
          { value: "First Contact", count: 11 },
          { value: "Speculative Fiction", count: 9 },
          { value: "Dystopian", count: 7 },
          { value: "Short Stories", count: 5 },
        ],
        series: [
          { value: "Dune Chronicles", count: 6 },
          { value: "Hainish Cycle", count: 7 },
          { value: "Foundation", count: 7 },
          { value: "Culture", count: 10 },
          { value: "The Expanse", count: 9 },
        ],
        publisher: [
          { value: "Ace Books", count: 15 },
          { value: "Chilton Books", count: 6 },
          { value: "Spectra", count: 12 },
          { value: "Tor Books", count: 19 },
          { value: "Gollancz", count: 14 },
        ],
        format: [
          { value: "Hardcover", count: 31 },
          { value: "EPUB", count: 24 },
          { value: "Audiobook", count: 17 },
        ],
        pages: [
          { value: "412", count: 3 },
          { value: "304", count: 4 },
          { value: "688", count: 2 },
        ],
        published: [
          { value: "1965", count: 2 },
          { value: "1969", count: 3 },
          { value: "1984", count: 2 },
        ],
        "published-decade": [
          { value: "1960", title: "1960s", count: 5 },
          { value: "1980", title: "1980s", count: 3 },
        ],
        abridged: [
          { value: "false", title: "No", count: 10 },
          { value: "true", title: "Yes", count: 2 },
        ],
        language: [
          { value: "en", title: "English", count: 64 },
          { value: "fr", title: "French", count: 8 },
          { value: "ja", title: "Japanese", count: 5 },
          { value: "de", title: "German", count: 3 },
        ],
        year: [
          { value: "1965", count: 2 },
          { value: "1969", count: 3 },
          { value: "1984", count: 2 },
        ],
        decade: [
          { value: "1960", title: "1960s", count: 5 },
          { value: "1980", title: "1980s", count: 3 },
        ],
        released: [
          { value: "1965", count: 2 },
          { value: "1969", count: 3 },
        ],
        rating: [
          { value: "9", title: "9+", count: 4 },
          { value: "8", title: "8+", count: 6 },
        ],
      };

      const rows = [...(pluginBuckets[field] ?? [{ value: "Herbert", count: 1 }])];
      const kind = lib.kind as
        { fields?: { key: string; enum?: string[]; values?: string[] }[] } | undefined;
      const fd = kind?.fields?.find((f) => f.key === field);
      if (opts?.declared && fd) {
        const declared = [...(fd.enum ?? []), ...(fd.values ?? [])];
        const present = new Set(rows.map((r) => r.value.toLowerCase()));
        for (const dv of declared) {
          if (!present.has(dv.toLowerCase())) {
            present.add(dv.toLowerCase());
            rows.push({ value: dv, count: 0 });
          }
        }
      }

      const q = (opts?.q ?? "").trim();
      const filtered = q ? rows.filter((r) => matchesFoldedPrefix(r.value, q)) : rows;
      const order = opts?.order === "name" ? "name" : "count";
      filtered.sort((a, b) =>
        order === "name"
          ? a.value.localeCompare(b.value, undefined, { sensitivity: "base" })
          : b.count - a.count || a.value.localeCompare(b.value, undefined, { sensitivity: "base" }),
      );

      if (
        opts &&
        (opts.q !== undefined ||
          opts.offset !== undefined ||
          opts.limit !== undefined ||
          opts.declared)
      ) {
        const offset = Math.max(0, opts.offset ?? 0);
        const limit = Math.max(1, Math.min(200, opts.limit ?? 40));
        const page = filtered.slice(offset, offset + limit);
        const more = offset + limit < filtered.length;
        return { field, values: page, truncated: more, more };
      }

      return { field, values: filtered };
    }

    const mediaType = String(lib.mediaType ?? "");
    const pool = catalog.filter((x) => !mediaType || mediaType === "mixed" || x.type === mediaType);
    const counts = new Map<string, { title: string; count: number }>();
    const bump = (key: string, title = key) => {
      if (!key) return;
      const prev = counts.get(key);
      counts.set(key, { title: prev?.title ?? title, count: (prev?.count ?? 0) + 1 });
    };

    for (const item of pool) {
      if (field === "genre") {
        for (const g of item.genres ?? []) bump(g);
      } else if (field === "studio") {
        const st = STUDIOS.find((s) => s.id === item.studio);
        bump(st?.name ?? String(item.studio));
      } else if (field === "network") {
        const nw = NETWORKS[item.studio % NETWORKS.length];
        if (nw) bump(nw.name);
      } else if (field === "franchise") {
        if (item.year !== undefined) {
          const col = COLLECTIONS[item.year % COLLECTIONS.length];
          if (col) bump(col.name);
        }
      } else if (field === "country") {
        bump(item.country);
      } else if (field === "decade") {
        if (item.year !== undefined) bump(decadeOf(item.year));
      } else if (field === "year") {
        if (item.year !== undefined) bump(String(item.year));
      } else if (field === "cast") {
        for (const pid of item.people?.slice(0, 3) ?? []) {
          const p = PEOPLE.find((person) => person.id === pid);
          if (p) bump(p.name);
        }
      } else if (field === "director") {
        const pid = item.people?.[0];
        const p = PEOPLE.find((person) => person.id === pid);
        if (p) bump(p.name);
      } else if (field === "writer") {
        const pid = item.people?.[1] ?? item.people?.[0];
        const p = PEOPLE.find((person) => person.id === pid);
        if (p) bump(p.name);
      } else if (field === "language" || field === "audioLanguage") {
        const code = (item.language || "en").toLowerCase();
        const label =
          new Intl.DisplayNames(["en"], { type: "language" }).of(code) ?? code.toUpperCase();
        bump(code, label);
      } else if (field === "contentRating") {
        bump(item.contentRating || (item.type === "show" ? "TV-14" : "PG-13"));
      } else if (field === "tag") {
        bump(Number(item.key) % 2 === 0 ? "favorites" : "criterion");
      } else if (field === "keyword") {
        for (const kwId of item.keywords ?? []) {
          const kw = KEYWORDS.find((k) => k.id === kwId);
          if (kw) bump(kw.name);
        }
      } else if (field === "rating") {
        const r = item.rating ?? 7.5;
        if (r >= 9) bump("9-10", "9+");
        else if (r >= 8) bump("8-8.9", "8–8.9");
        else if (r >= 7) bump("7-7.9", "7–7.9");
        else if (r >= 6) bump("6-6.9", "6–6.9");
        else if (r > 0) bump("under-6", "Under 6");
      } else if (field === "runtime") {
        const m = item.runtimeMinutes ?? 110;
        if (m < 30) bump("under-30m", "Under 30m");
        else if (m < 60) bump("30-60m", "30–60m");
        else if (m < 90) bump("60-90m", "1–1.5h");
        else if (m < 120) bump("90-120m", "1.5–2h");
        else if (m < 180) bump("120-180m", "2–3h");
        else bump("180m-plus", "3h+");
      } else if (field === "addedMonth") {
        const ym = (item.addedAt ?? "2026-01-15").slice(0, 7);
        bump(ym, ym);
      } else if (field === "resolution") {
        bump(
          item.year && item.year >= 2018 ? "4k" : "1080p",
          item.year && item.year >= 2018 ? "4K" : "1080p",
        );
      } else if (field === "hdr") {
        bump(
          item.year && item.year >= 2020 ? "dv" : "sdr",
          item.year && item.year >= 2020 ? "Dolby Vision" : "SDR",
        );
      }
    }

    if (counts.size === 0 && (field === "cast" || field === "director")) {
      for (const [idx, p] of PEOPLE.slice(0, 6).entries()) {
        bump(p.name, p.name);
        counts.set(p.name, { title: p.name, count: 8 - idx });
      }
    }

    const values = [...counts.entries()]
      .sort((a, b) => b[1].count - a[1].count || a[0].localeCompare(b[0]))
      .map(([value, info]) => ({ value, title: info.title, count: info.count }));
    return { field, values };
  }

  function previewTab(
    lib: Record<string, unknown>,
    body: unknown,
  ): { ok: true; data: LibraryTabPreviewResponse } | { ok: false; message: string } {
    const obj = (body && typeof body === "object" ? body : {}) as Record<string, unknown>;
    const rawTab = ("tab" in obj ? obj.tab : obj) as Partial<LibraryTab> | null;
    if (!rawTab || typeof rawTab !== "object") {
      return { ok: false, message: "Invalid tab preview body" };
    }
    const query = String(obj.query ?? "")
      .trim()
      .toLowerCase();
    const rawLimit = Number(obj.limit ?? 12);
    const limit = rawLimit > 0 ? Math.min(48, rawLimit) : 12;

    const candidate: Partial<LibraryTab> = {
      ...rawTab,
      key: String(rawTab.key ?? "").trim() || "preview",
      label: String(rawTab.label ?? "").trim() || "Preview",
      hidden: false,
    };
    const validated = validateTabList(lib, [candidate]);
    if (!validated.ok) return validated;
    const tab = validated.tabs[0]!;

    if (tab.kind === "recommended") {
      return {
        ok: true,
        data: { kind: "recommended", scopeNote: SCOPE_NOTE },
      };
    }

    if (tab.kind === "collections") {
      const facet = tab.facet ?? "";
      if (facet === "") {
        const mediaType = String(lib.mediaType ?? "");
        const matched = getCollections().filter((sp) => {
          if (mediaType && sp.mediaType && sp.mediaType !== "mixed" && sp.mediaType !== mediaType) {
            return false;
          }
          if (query && !sp.name.toLowerCase().includes(query)) return false;
          return true;
        });
        return {
          ok: true,
          data: {
            kind: "collections",
            facet: "",
            order: "server",
            collections: matched.slice(0, limit).map((sp) => ({
              id: String(sp.id ?? sp.name),
              name: sp.name,
              itemCount: sp.itemCount ?? 0,
            })),
            more: matched.length > limit,
          },
        };
      }

      const order = tab.order ?? "count";
      const buckets = [...facetValuesFor(lib, facet).values].filter((b) =>
        query ? (b.title ?? b.value).toLowerCase().includes(query) : true,
      );
      if (order === "name") {
        buckets.sort((a, b) =>
          (a.title ?? a.value).localeCompare(b.title ?? b.value, undefined, {
            sensitivity: "base",
          }),
        );
      } else {
        buckets.sort((a, b) => b.count - a.count);
      }
      return {
        ok: true,
        data: {
          kind: "collections",
          facet,
          order,
          collections: buckets.slice(0, limit).map((b) => ({
            id: `facet:${facet}:${b.value}`,
            bucketId: b.value,
            name: b.title ?? b.value,
            itemCount: b.count,
          })),
          more: buckets.length > limit,
        },
      };
    }

    if (tab.kind === "people") {
      const facet = tab.facet ?? "";
      const order = tab.order ?? "name";
      const buckets = [...facetValuesFor(lib, facet).values].filter((b) =>
        query ? (b.title ?? b.value).toLowerCase().includes(query) : true,
      );
      if (order === "name") {
        buckets.sort((a, b) =>
          (a.title ?? a.value).localeCompare(b.title ?? b.value, undefined, {
            sensitivity: "base",
          }),
        );
      } else {
        buckets.sort((a, b) => b.count - a.count);
      }
      return {
        ok: true,
        data: {
          kind: "people",
          facet,
          order,
          people: buckets.slice(0, limit).map((b, idx) => ({
            id: idx + 1,
            name: b.title ?? b.value,
            itemCount: b.count,
          })),
          more: buckets.length > limit,
        },
      };
    }

    if (tab.kind === "live") {
      return {
        ok: true,
        data: {
          kind: "live",
          card: tab.card ?? "landscape",
          scopeNote: SCOPE_NOTE,
        },
      };
    }

    // kind === "library" | "view"
    const defaultSort = lib.source === "plugin" ? "title" : "popularity";
    const sort = tab.sort || defaultSort;
    const direction =
      tab.direction ?? (sort === "server" || sort === "title" || sort === "index" ? "asc" : "desc");

    if (lib.source === "plugin") {
      const items = [
        { key: 1, title: "Dune", year: 1965 },
        { key: 2, title: "The Left Hand of Darkness", year: 1969 },
      ].filter((it) => (query ? it.title.toLowerCase().includes(query) : true));
      return {
        ok: true,
        data: {
          kind: tab.kind,
          sort,
          direction,
          total: items.length,
          items: items.slice(0, limit),
        },
      };
    }

    const mediaType = String(lib.mediaType ?? "");
    let pool = catalog.filter((x) => !mediaType || mediaType === "mixed" || x.type === mediaType);
    if (query) {
      pool = pool.filter((x) => x.title.toLowerCase().includes(query));
    }
    if (tab.filters?.rules?.length) {
      const matchAny = tab.filters.match === "any";
      pool = pool.filter((item) =>
        matchAny
          ? tab.filters!.rules.some((r) => matchesRule(item, r))
          : tab.filters!.rules.every((r) => matchesRule(item, r)),
      );
    }

    const dirMul = direction === "asc" ? 1 : -1;
    pool.sort((a, b) => {
      switch (sort) {
        case "title":
        case "server":
          return a.title.localeCompare(b.title) * dirMul;
        case "added":
          return a.addedAt.localeCompare(b.addedAt) * dirMul;
        case "released":
          return a.released.localeCompare(b.released) * dirMul;
        case "year":
          return ((a.year ?? 0) - (b.year ?? 0)) * dirMul;
        case "rating":
          return ((a.rating ?? 0) - (b.rating ?? 0)) * dirMul;
        case "duration":
          return ((a.runtimeMinutes ?? 0) - (b.runtimeMinutes ?? 0)) * dirMul;
        case "popularity":
        default:
          return (a.popularity - b.popularity) * dirMul;
      }
    });

    const mapped = pool.slice(0, limit).map((item) =>
      toCard
        ? toCard(item)
        : {
            key: item.key,
            title: item.title,
            year: item.year,
            poster: item.poster,
          },
    );

    return {
      ok: true,
      data: {
        kind: tab.kind,
        sort,
        direction,
        total: pool.length,
        items: mapped,
      },
    };
  }

  return {
    getTabs,
    putTabs,
    resetTabs,
    previewTab,
    facetValuesFor,
  };
}
