/**
 * Ether Live Events & Screens contract re-exports for the Sports plugin,
 * sourced from the real TypeScript SDK (`sdk/typescript`) via type-only
 * imports so `wasm.Compile` (Studio VFS) and `kit.Pack` strip them cleanly
 * with zero runtime external module dependencies, plus the hosted QuickJS
 * `globalThis.ether` request/host shapes.
 */

import type {
  HostFetchError,
  HostFetchManyOptions,
  HostFetchManyResult,
  HostFetchRequest,
  HostFetchResponse,
  LiveEvent,
  LiveEventBroadcast,
  LiveEventCompetitor,
  LiveEventDetail,
  LiveEventEntitiesResponse,
  LiveEventEntity,
  LiveEventLeague,
  LiveEventLink,
  LiveEventMatchHints,
  LiveEventPlay,
  LiveEventProbability,
  LiveEventProbabilityPoint,
  LiveEventRankRow,
  LiveEventsResponse,
  LiveEventStandingsResponse,
  LiveEventState,
  LiveEventStatus,
  LiveEventTable,
  LiveFeedMeta,
  ScreenEventQuery,
  ScreenMeta,
  ScreenSectionMeta,
} from "../../../typescript/src/index.ts";

export type {
  HostFetchError,
  HostFetchManyOptions,
  HostFetchRequest,
  HostFetchResponse,
  LiveEvent,
  LiveEventBroadcast,
  LiveEventCompetitor,
  LiveEventDetail,
  LiveEventEntitiesResponse,
  LiveEventEntity,
  LiveEventLeague,
  LiveEventLink,
  LiveEventMatchHints,
  LiveEventPlay,
  LiveEventProbability,
  LiveEventProbabilityPoint,
  LiveEventRankRow,
  LiveEventsResponse,
  LiveEventStandingsResponse,
  LiveEventState,
  LiveEventStatus,
  LiveEventTable,
  LiveFeedMeta,
  ScreenEventQuery,
  ScreenMeta,
  ScreenSectionMeta,
};

// Short aliases used throughout the Sports plugin sources.
export type LeagueRef = LiveEventLeague;
export type Competitor = LiveEventCompetitor;
export type RankingRow = LiveEventRankRow;
export type Probability = LiveEventProbability;
export type ProbabilityPoint = LiveEventProbabilityPoint;
export type Broadcast = LiveEventBroadcast;
export type MatchHints = LiveEventMatchHints;
export type GenericTable = LiveEventTable;
export type EventPlay = LiveEventPlay;
export type EventLink = LiveEventLink;
export type LiveEntity = LiveEventEntity;
export type EventsResponse = LiveEventsResponse;
export type EntitiesResponse = LiveEventEntitiesResponse;
export type StandingsResponse = LiveEventStandingsResponse;
export type LiveFeedContribution = LiveFeedMeta;
export type ScreenSectionQuery = ScreenEventQuery;
export type ScreenSection = ScreenSectionMeta;
export type ScreenContribution = ScreenMeta;
export type HostFetchBatchResult = HostFetchManyResult;

// Hosted QuickJS request / response / globalThis.ether host bindings.
export interface HostedRequest {
  method?: string;
  path: string;
  query?: Record<string, string | string[]>;
  headers?: Record<string, string>;
  body?: unknown;
}

export interface HostedResponse {
  status: number;
  headers?: Record<string, string>;
  body: unknown;
}

export interface HostedEtherGlobal {
  settings: {
    get(): Record<string, unknown>;
  };
  kv: {
    get(key: string): string | null;
    put(key: string, value: string): void;
    delete(key: string): void;
    list?(prefix?: string): string[];
  };
  http: {
    fetch(req: HostFetchRequest): HostFetchResponse;
    fetchMany?(
      reqs: HostFetchRequest[],
      opts?: HostFetchManyOptions,
    ): HostFetchBatchResult[];
  };
  log?(level: string, message: string, fields?: Record<string, unknown>): void;
}
