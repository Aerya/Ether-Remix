import { t } from "../i18n";

export function qualityFallback(reason?: string): string {
  if (!reason) return "";
  const match = /^Played in (.+): no acceptable (\d+p) or better release was found\.$/.exec(reason);
  return match
    ? t("Played in {quality}: no acceptable {minimum} or better release was found.", {
        quality: match[1],
        minimum: match[2],
      })
    : reason;
}

function classifyDecisionClause(line: string): { text: string; generic?: boolean } {
  const fallback = qualityFallback(line);
  if (fallback !== line) return { text: fallback };

  if (/needs a video transcode|without a video transcode/i.test(line)) {
    return { text: t("Every matching release needs a video transcode, and transcoding is off") };
  }
  if (/no acceptable candidates/i.test(line)) {
    return { text: t("No releases matched this profile") };
  }
  if (/no healthy candidates/i.test(line)) {
    return { text: t("Every matching release failed its health check") };
  }
  if (/cached unavailable \(cooloff\)/i.test(line)) {
    return { text: t("Skipped — recently checked and unavailable") };
  }
  if (/provider busy|stream limit reached/i.test(line)) {
    return { text: t("Source stream limit reached") };
  }
  if (/title has not been released/i.test(line)) {
    return { text: t("Title has not been released yet") };
  }
  if (/background check cannot decide this source|add-on streams open only for play/i.test(line)) {
    return { text: t("Background check skipped live-only sources") };
  }
  if (/discovery share reached|held back by local budget/i.test(line)) {
    return { text: t("Search budget saved for playback") };
  }
  if (/indexer daily budget exhausted|daily api limit reached/i.test(line)) {
    return { text: t("Daily indexer search limit reached") };
  }
  if (/context canceled/i.test(line)) {
    return { text: t("Cancelled before finishing") };
  }
  if (/context deadline exceeded|timed out|no answer within/i.test(line)) {
    return { text: t("Timed out waiting for sources") };
  }
  if (/pinned release .* cannot be played/i.test(line)) {
    return { text: t("Pinned release couldn't be played") };
  }
  const queuedBehind = /\b(queued at [^:]+? behind \d+ active downloads?)\b/i.exec(line);
  if (queuedBehind) {
    return {
      text: `${t("Not cached yet — queued to download on debrid")} (${queuedBehind[1]})`,
    };
  }
  if (
    /not cached on the debrid service.*(?:download queued|sent to the debrid service to download)|download queued/i.test(
      line,
    )
  ) {
    return { text: t("Not cached yet — queued to download on debrid") };
  }
  if (/active-download limit/i.test(line)) {
    return { text: t("Not cached, and debrid is at its download limit") };
  }
  if (/did not take it to download|download queuing is unavailable/i.test(line)) {
    return { text: t("Not cached, and debrid couldn't queue the download") };
  }
  if (/cached torrents only is on/i.test(line)) {
    return { text: t("Not cached on debrid (cached-only mode is on)") };
  }
  if (/not cached/i.test(line)) {
    return { text: t("Not cached by any debrid provider") };
  }
  if (/incorrect api key|rejected the api key|unauthorized/i.test(line)) {
    return { text: t("Source rejected the API key") };
  }
  if (/no active premium subscription|account suspended|account not verified/i.test(line)) {
    return { text: t("Source account needs attention") };
  }
  if (/no indexers? configured|no enabled indexer/i.test(line)) {
    return { text: t("No indexers configured") };
  }
  if (/indexer paused after repeated failures/i.test(line)) {
    return { text: t("Indexer paused after repeated failures") };
  }
  if (/article missing on all providers/i.test(line)) {
    return { text: t("Missing articles on all Usenet providers") };
  }
  if (/corrupt article or media data/i.test(line)) {
    return { text: t("Release data is corrupt or incomplete") };
  }
  if (/media source temporarily unavailable/i.test(line)) {
    return { text: t("Source temporarily unavailable") };
  }
  if (/release cannot be streamed/i.test(line)) {
    return { text: t("Release cannot be streamed") };
  }
  if (/not available yet/i.test(line)) {
    return { text: t("No playable release available yet"), generic: true };
  }

  let cleaned = line.trim();
  while (/^(?:resolver|newznab|torznab|debrid|usenet):\s*/i.test(cleaned)) {
    cleaned = cleaned.replace(/^(?:resolver|newznab|torznab|debrid|usenet):\s*/i, "");
  }
  cleaned = cleaned.replace(/\s+/g, " ").trim();
  if (!cleaned) return { text: "" };
  return { text: cleaned.charAt(0).toUpperCase() + cleaned.slice(1) };
}

/**
 * Plain-language summary of a resolver decision error for the History table and
 * drawer header. Multi-source Go error chains are collapsed and deduplicated;
 * the raw detail stays available in the decision drawer.
 */
export function decisionErrorSummary(raw?: string): string {
  if (!raw) return "";
  const trimmed = raw.trim();
  if (!trimmed) return "";
  const fallback = qualityFallback(trimmed);
  if (fallback !== trimmed) return fallback;

  const items: { text: string; generic?: boolean }[] = [];
  const seen = new Set<string>();
  for (const rawLine of trimmed.split(/\r?\n/)) {
    const line = rawLine.trim();
    if (!line) continue;
    const item = classifyDecisionClause(line);
    if (!item.text || seen.has(item.text)) continue;
    seen.add(item.text);
    items.push(item);
  }
  const filtered = items.some((x) => !x.generic) ? items.filter((x) => !x.generic) : items;
  return filtered.map((x) => x.text).join(" · ");
}
