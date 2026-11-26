/**
 * Plugin screens and surfaces (docs/plugin-screens.md): helpers for the
 * answers of `plugin` sources and actions. A plugin never puts a URL or an
 * Ether key in its data directly; it wraps them in envelopes, and Ether
 * rewrites each one for the viewer who opened the screen:
 *
 * - image(url): a picture on one of your permissions.network hosts, served
 *   to the client through Ether's signed image proxy (asset("icons/x.svg")
 *   for a file in your package);
 * - link(url): an https page the viewer may open on a phone or the web;
 * - record(type, id): a reference to one of your records (sent once,
 *   patched live by screens.putRecords);
 * - event(feed, id) and entity(feed, kind, id): your live events and
 *   entities, resolved and filtered by Ether.
 *
 * A string that is a bare URL is dropped by Ether, so use the helpers.
 */

export interface ImageEnvelope { $image: string }
export interface LinkEnvelope { $link: string }
export interface RecordEnvelope { $record: { type: string; id: string } }
export interface EventEnvelope { $event: { feed: string; id: string } }
export interface EntityEnvelope { $entity: { feed: string; kind: string; id: string } }

/** A picture on one of your permissions.network hosts. */
export function image(url: string): ImageEnvelope {
  return { $image: url };
}

/** A file in your plugin package (icons, artwork). */
export function asset(path: string): ImageEnvelope {
  return { $image: `asset:${path.replace(/^\/+/, "")}` };
}

/** An https page; Vela offers it on phones and the web, never on TV. */
export function link(url: string): LinkEnvelope {
  return { $link: url };
}

/** A reference to one of your records; send its value in `records`. */
export function record(type: string, id: string): RecordEnvelope {
  return { $record: { type, id } };
}

/** One of your live feed's events. */
export function event(feed: string, id: string): EventEnvelope {
  return { $event: { feed, id } };
}

/** One of your live feed's entities (a team, a driver). */
export function entity(feed: string, kind: string, id: string): EntityEnvelope {
  return { $entity: { feed, kind, id } };
}
