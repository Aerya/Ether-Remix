import type { Descriptor } from "../lib/extend";
import { REMIX_NAV, remixHref } from "./navigation";
const text = (fr: string, en = fr) => ({ fr, en });
const demoField = (key: string, fr: string, en: string, kind = "toggle", extra = {}) => ({
  key,
  kind,
  label: text(fr, en),
  help: text(""),
  default: kind === "toggle" ? "off" : "",
  value: kind === "toggle" ? "off" : "",
  source: "console",
  ...extra,
});
const demoFeatures = [
  {
    id: "predb",
    title: text(
      "Pre (bêta) : annonces de releases FR / MULTi / VOSTFR",
      "Pre (beta): FR / MULTi / VOSTFR release announcements",
    ),
    description: text("Fonction en bêta."),
    status: {
      active: true,
      import: {
        state: "ok",
        last: 1795240000,
        added: 12,
        pending: 3,
        feeds: [
          {
            feed: "https://api.predb.net/feed/",
            seen: 4,
            indexed: 3,
            pending: 1,
            unmatched: 0,
            state: "ok",
            fetched: 20,
            kept: 1,
            readAt: 1795240000,
          },
          {
            feed: "https://api.predb.net/?type=section&q=ANiME",
            seen: 2,
            indexed: 2,
            pending: 0,
            unmatched: 0,
            state: "ok",
            fetched: 20,
            kept: 0,
          },
          {
            feed: "https://predb.me/?cats=movies&rss=1",
            seen: 0,
            indexed: 0,
            pending: 0,
            unmatched: 0,
            state: "error",
            error: "flux refusé (403)",
          },
          {
            feed: "perso:1",
            label: "Mon tracker",
            seen: 6,
            indexed: 7,
            pending: 2,
            unmatched: 0,
            state: "ok",
            fetched: 50,
            kept: 6,
          },
        ],
      },
    },
    fields: [
      demoField("PSE_PREDB", "Activer", "Enable", "toggle", { value: "on" }),
      demoField("PSE_PREDB_PUBLIC", "Flux publics", "Public feeds", "toggle", {
        value: "on",
        default: "on",
      }),
      demoField(
        "PSE_PREDB_FEEDS",
        "Flux privés (RSS, API...)",
        "Private feeds (RSS, API...)",
        "feeds",
        {
          help: text(
            "Ajoutez autant de sites que vous voulez : un nom (facultatif), l'adresse d'un flux RSS ou d'une API JSON et, si le site en demande une, sa clé d'API.",
          ),
          value: '[{"name":"Mon tracker","url":"https://tracker.example/rss","key":"••••••"}]',
        },
      ),
      demoField(
        "PSE_PREDB_PROXY_TYPE",
        "Proxy pour les appels PRE",
        "Proxy for PRE calls",
        "select",
        {
          options: [
            { value: "", label: text("Aucun (lecture directe)", "None (direct)") },
            {
              value: "proxy",
              label: text("Proxy HTTP, HTTPS ou SOCKS5", "HTTP, HTTPS or SOCKS5 proxy"),
            },
            { value: "mediaflow", label: text("Relais MediaFlow", "MediaFlow relay") },
          ],
        },
      ),
      demoField(
        "PSE_PREDB_PROXY_URL",
        "Adresse du proxy ou du relais",
        "Proxy or relay address",
        "url",
      ),
      demoField(
        "PSE_PREDB_PROXY_AUTH",
        "Identifiants du proxy (utilisateur:mot de passe) ou mot de passe API MediaFlow",
        "Proxy credentials or MediaFlow API password",
        "secret",
      ),
    ],
  },
  {
    id: "music",
    title: text("Musique", "Music"),
    description: text(""),
    status: { active: false },
    fields: [
      demoField(
        "PSE_MUSIC_ECLIPSE_URL",
        "Manifest Eclipse personnel",
        "Personal Eclipse manifest",
        "url",
      ),
      demoField("PSE_MUSIC_TIDO_URL", "Tido", "Tido", "url", { community: true }),
      demoField("PSE_MUSIC_INTERNETARCHIVE_URL", "Internet Archive", "Internet Archive", "url", {
        community: true,
      }),
      demoField("PSE_MUSIC_ULTRAMAX_URL", "UltraMax", "UltraMax", "url", { community: true }),
      demoField("PSE_MUSIC_DEEZER_RICKY_URL", "Deezer · Ricky", "Deezer · Ricky", "url", {
        community: true,
      }),
      demoField("PSE_MUSIC_SOUNDCLOUD_URL", "SoundCloud · Eclipse", "SoundCloud · Eclipse", "url", {
        community: true,
      }),
      demoField(
        "PSE_MUSIC_SOUNDCLOUD_RICKY_URL",
        "SoundCloud · Ricky",
        "SoundCloud · Ricky",
        "url",
        { community: true },
      ),
      demoField(
        "PSE_MUSIC_ADDON_REGISTRY",
        "Actualiser les liens des fournisseurs",
        "Refresh provider setup links",
        "toggle",
        { advanced: true },
      ),
      demoField("PSE_MUSIC_DMM", "DMM · Real-Debrid", "DMM · Real-Debrid"),
      demoField(
        "PSE_MUSIC_REALDEBRID",
        "Real-Debrid par indexeurs",
        "Real-Debrid through indexers",
      ),
      demoField("PSE_MUSIC_ALLDEBRID", "AllDebrid", "AllDebrid"),
      demoField("PSE_MUSIC_TORBOX", "TorBox", "TorBox"),
      demoField("PSE_MUSIC_AUDIUS", "Audius", "Audius", "select", {
        options: [
          { value: "off", label: text("Désactivé", "Off") },
          { value: "fallback", label: text("En repli", "Fallback") },
        ],
      }),
      demoField("PSE_MUSIC_OPENDEEZER", "OpenDeezer", "OpenDeezer", "toggle"),
      demoField(
        "PSE_MUSIC_OPENDEEZER_ARL",
        "Session Deezer (ARL)",
        "Deezer session (ARL)",
        "secret",
      ),
      demoField("PSE_MUSIC_HIFI_URL", "HiFi API", "HiFi API", "url", { advanced: true }),
    ],
  },
  {
    id: "indexer",
    title: text("Indexation des releases", "Release indexing"),
    description: text(""),
    fields: [
      demoField(
        "PSE_INDEXER",
        "Rechercher automatiquement des releases",
        "Automatically search for releases",
        "toggle",
        { value: "off", default: "off" },
      ),
      demoField("PSE_INDEXER_SOURCES", "Sources importées", "Imported sources", "multi", {
        options: [],
      }),
    ],
  },
];
/** Réponses minimales du fork pour le serveur de démonstration Ether. */
export function remixMock(route: string): Response | undefined {
  if (route === "POST /pse/predb/test")
    return Response.json({
      ok: true,
      result: {
        runs: [
          {
            feed: "https://api.predb.net/feed/",
            state: "ok",
            fetched: 20,
            kept: 1,
            sample: ["Un.Film.2026.FRENCH.1080p.WEB.H264-GRP"],
          },
          {
            feed: "https://api.predb.net/?type=section&q=ANiME",
            state: "ok",
            fetched: 20,
            kept: 0,
          },
          {
            feed: "https://predb.me/?cats=movies&rss=1",
            state: "error",
            error: "flux refusé (403)",
          },
          {
            feed: "perso:1",
            label: "Mon tracker",
            state: "ok",
            fetched: 50,
            kept: 6,
            sample: [
              "Serie.S02E05.MULTi.1080p.WEB.H264-GRP",
              "Autre.2026.VOSTFR.1080p.WEB.H264-GRP",
            ],
          },
        ],
      },
    });
  if (route === "GET /pse/music/playlists")
    return Response.json({
      lastfmKey: false,
      hours: 6,
      viewers: [
        { id: 1, name: "Alex", accounts: {}, playlists: [], syncing: false, spotify: false },
      ],
    });
  if (route === "GET /pse/music/playlists/discover")
    return Response.json({
      items: [
        { name: "Jazz au calme", url: "https://www.deezer.com/playlist/1" },
        { name: "Voyage électronique", url: "https://www.deezer.com/playlist/2" },
        { name: "Classiques soul", url: "https://www.deezer.com/playlist/3" },
      ],
      more: false,
    });
  if (route === "POST /pse/music/playlists/preview")
    return Response.json({
      token: "demo-preview",
      name: "Jazz au calme",
      source: "deezer",
      total: 2,
      skipped: 0,
      tracks: [
        { title: "So What", artist: "Miles Davis" },
        { title: "Take Five", artist: "Dave Brubeck" },
      ],
    });
  if (route === "POST /pse/music/playlists/import")
    return Response.json({
      id: "demo-import",
      done: true,
      processed: 2,
      total: 2,
      matched: 2,
      missing: [],
    });

  if (route === "GET /pse" || route === "PUT /pse")
    return Response.json({ features: demoFeatures });
  if (route.startsWith("POST /debrid/providers/") && route.endsWith("/key"))
    return Response.json({ key: "demo-api-key" });
  if (route === "GET /pse/music/addons" || route === "POST /pse/music/addons")
    return Response.json({ addons: [], updatedAt: "" });
  if (route === "GET /pse/themes" || route === "GET /pse/themes/")
    return Response.json({ libraries: [], collections: [], presets: [], max: 99 });
  if (route === "GET /pse/themes/priorities" || route === "GET /pse/themes/priority")
    return Response.json({ priorities: [], total: 0, max: 99 });
  if (route === "GET /pse/themes/vostfr") return Response.json({ keys: [], max: 99 });
  return undefined;
}

export const remixDescriptors: Descriptor[] = [
  {
    kind: "page",
    id: "pse",
    owner: "builtin",
    label: { source: "Ether-Remix" },
    icon: "Languages",
    group: "remix",
    order: 10,
    meta: { route: "/remix", navGroup: "remix", inNav: true },
  },
  ...REMIX_NAV.map((e, i): Descriptor => ({
    kind: "navItem",
    id: e.id,
    owner: "builtin",
    label: { source: e.fr },
    icon: e.icon,
    group: "remix",
    order: 10 + i,
    meta: { route: remixHref(e.tab), page: "pse" },
  })),
];
