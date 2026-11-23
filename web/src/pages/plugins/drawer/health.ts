import { t, tn } from "../../../i18n";
import type { HealthLevel } from "../../../lib/health";
import { formatNumber, parseTime, relativeTime, unit } from "../../../lib/format";
import { pluginHealthLevel, type PluginDetail, type PluginFeedOverview } from "../types";

/**
 * One reading of a plugin's health for every place the drawer shows it:
 * the header word, the Responding tile and the problem card. They all come
 * from here, so they can never disagree. Plain words only; the raw error
 * lives in Activity.
 */
export interface HealthSummary {
  level: HealthLevel;
  /** One or two words for the header and the tile. */
  word: string;
  /** The tile's second line. */
  tile: string;
  /** Present only when the owner should look. */
  card?: { title: string; line: string; meta?: string; retry: boolean };
}

/** Compact poll interval ("15s", "2m", "1h") for feed health and metadata. */
export function formatPollInterval(seconds?: number): string {
  if (!seconds || !Number.isFinite(seconds) || seconds <= 0) return "";
  const s = Math.round(seconds);
  if (s < 60) return unit(s, "second");
  if (s < 3600) return unit(Math.round(s / 60), "minute");
  return unit(Math.round(s / 3600), "hour");
}

function cleanFeedError(raw?: string): string {
  if (!raw) return "";
  return raw.replace(/^liveevents:\s*/i, "").trim();
}

/**
 * The level and the word for a plugin's health, from its status and the
 * server's plain reason only, so a card in the Installed list and the
 * plugin's drawer always say the same thing.
 */
export function healthHeadline(p: {
  health: string;
  healthReason?: string;
  healthSidecar?: string;
  enabled: boolean;
}): {
  level: HealthLevel;
  word: string;
  slow: boolean;
} {
  const level = pluginHealthLevel(p.health, p.enabled);
  const slow = level === "warn" && /slow/i.test(p.healthReason ?? "");
  const waiting = level === "warn" && p.healthSidecar === "waiting";
  const word =
    level === "off"
      ? t("Off")
      : level === "unknown"
        ? t("Unknown")
        : level === "ok"
          ? t("Healthy")
          : waiting
            ? t("Waiting for its sidecar")
            : slow
              ? t("Slow")
              : level === "warn"
                ? t("Needs attention")
                : t("Not answering");
  return { level, word, slow };
}

export function healthSummary(
  detail: PluginDetail,
  liveFeeds?: PluginFeedOverview[],
): HealthSummary {
  const h = detail.healthDetail;
  const im = detail.impact;
  const { level, word, slow } = healthHeadline({
    health: h.status,
    healthReason: h.reason,
    healthSidecar: h.sidecar,
    enabled: detail.enabled,
  });
  const calls = im.calls;
  const seconds = Math.round(im.p50Ms / 1000);
  const failures = t("{errors} of {calls} recent calls failed", {
    errors: formatNumber(im.errors, 0),
    calls: formatNumber(calls, 0),
  });
  if (level === "off") {
    return { level, word, tile: t("Turned off") };
  }
  const failingFeeds = (liveFeeds ?? [])
    .filter((f) => f.health.consecutiveErrors > 0 || Boolean(f.health.lastError))
    .sort((a, b) => b.health.consecutiveErrors - a.health.consecutiveErrors);
  if (failingFeeds.length > 0) {
    const worst = failingFeeds[0]!;
    const fh = worst.health;
    const feedLevel: HealthLevel =
      level === "error" || fh.consecutiveErrors >= 3 ? "error" : "warn";
    const metaParts: string[] = [];
    if (fh.lastPollAt && parseTime(fh.lastPollAt)) {
      metaParts.push(t("Last poll {when}", { when: relativeTime(fh.lastPollAt) }));
    }
    if (fh.consecutiveErrors > 0) {
      metaParts.push(tn(fh.consecutiveErrors, "{count} error", "{count} errors"));
    }
    if (fh.nextPollSec && fh.nextPollSec > 0) {
      metaParts.push(
        t("Next poll in {interval}", { interval: formatPollInterval(fh.nextPollSec) }),
      );
    }
    const errLine = cleanFeedError(fh.lastError) || t("Recent polls failed.");
    return {
      level: feedLevel,
      word: feedLevel === "error" ? t("Not answering") : t("Needs attention"),
      tile:
        fh.consecutiveErrors > 0
          ? tn(fh.consecutiveErrors, "{count} feed error", "{count} feed errors")
          : t("Feed failing"),
      card: {
        title:
          failingFeeds.length === 1
            ? t("Live event feed “{feed}” is failing", { feed: worst.title || worst.id })
            : tn(
                failingFeeds.length,
                "{count} live event feed is failing",
                "{count} live event feeds are failing",
              ),
        line: errLine,
        meta: metaParts.length > 0 ? metaParts.join(" · ") : undefined,
        retry: true,
      },
    };
  }
  if (level === "ok" || level === "unknown") {
    return {
      level,
      word,
      tile: calls ? tn(calls, "{count} recent call", "{count} recent calls") : t("Not used yet"),
    };
  }
  if (level === "warn" && h.sidecar === "waiting") {
    return {
      level,
      word,
      tile: t("Not connected"),
      card: {
        title: t("{name} needs its sidecar running", { name: detail.name }),
        line: t(
          "This plugin runs as its own program on another computer. Choose Connect a sidecar below to get a pairing code, then start the program with it. Until it connects, nothing the plugin provides works.",
        ),
        retry: true,
      },
    };
  }
  const skipped = h.rankingSkipped ?? 0;
  if (level === "warn" && skipped > 0) {
    return {
      level,
      word,
      tile: tn(skipped, "Ranking left out once", "Ranking left out {count} times"),
      card: {
        title: t("Releases are being ranked without {name}", { name: detail.name }),
        line: t(
          "Its ranking step ran past its time limit, so it was left out. It counts again as soon as it finishes in time.",
        ),
        retry: false,
      },
    };
  }
  if (slow) {
    return {
      level,
      word,
      tile:
        seconds >= 1 ? t("About {seconds} s per answer", { seconds }) : t("Some answers are slow"),
      card: {
        title:
          seconds >= 1
            ? t("Works, but slow: about {seconds} s per answer", { seconds })
            : t("Works, but some answers are slow"),
        line: t("Anything that uses it waits for it."),
        retry: false,
      },
    };
  }
  if (level === "warn") {
    return {
      level,
      word,
      tile: im.errors ? failures : t("Needs attention"),
      card: {
        title: t("{name} needs attention", { name: detail.name }),
        line: t("Activity shows what happened."),
        retry: true,
      },
    };
  }
  const lastOk = h.lastOk && Date.parse(h.lastOk) > Date.UTC(2000, 0, 1) ? h.lastOk : "";
  return {
    level,
    word,
    tile: im.errors ? failures : word,
    card: {
      title: t("{name} isn't answering", { name: detail.name }),
      line: lastOk
        ? t("It last worked {when}.", { when: relativeTime(lastOk) })
        : t("It hasn't answered yet."),
      retry: true,
    },
  };
}
