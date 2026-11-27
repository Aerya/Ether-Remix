import { t } from "../i18n";
import { api, apiFetch } from "./api";

export interface MetadataSource {
  id: string;
  name: string;
  configured: boolean;
  enabled: boolean;
  keySource: "owner" | "built-in" | "none";
  movies: boolean;
  shows: boolean;
}

export interface EpisodeOrder {
  source: string;
  kind: string;
  label?: string;
}

export interface EpisodeOrderProvenance {
  effective: EpisodeOrder;
  override?: EpisodeOrder | null;
  libraryDefault: EpisodeOrder;
  libraryName?: string;
}

export interface EpisodeOrderDetails extends EpisodeOrderProvenance {
  options: EpisodeOrder[];
  gap?: { order: EpisodeOrder; count: number };
}

export const builtinMetadataSources = {
  tmdb: {
    name: "TMDB",
    key: "tmdbKey",
    label: () => t("TMDB API key"),
    description: () => t("Movie and TV details, artwork and people."),
    keyURL: "https://www.themoviedb.org/settings/api",
    test: api.testTMDB,
  },
  tvdb: {
    name: "TheTVDB",
    key: "tvdbKey",
    label: () => t("TheTVDB API key"),
    description: () => t("Movie and TV details, artwork and people."),
    keyURL: "https://www.thetvdb.com/api-information/signup",
    test: api.testTVDB,
  },
} as const;

export type BuiltinMetadataSource = keyof typeof builtinMetadataSources;

export const metadataSources = (signal?: AbortSignal) =>
  apiFetch<{ sources: MetadataSource[] }>("/metadata/sources", { signal }).then((r) => r.sources);

export function supportsMetadata(source: MetadataSource, mediaType?: string) {
  if (mediaType === "movie") return source.movies;
  if (mediaType === "show") return source.shows;
  return source.movies && source.shows;
}
