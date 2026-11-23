/**
 * Pure channel-number ordering and drag-to-reorder renumbering logic.
 *
 * `compareChannelNumber` mirrors Go's `parseNumberKey` in
 * `internal/livetv/channel_cache.go` (major integer, minor integer after `.`,
 * then raw string) so the console, Plex, Jellyfin/Emby, Vela, Silo and Xtream
 * always sort channels identically.
 */

export type ParsedChannelNumber = {
  major: number;
  minor: number;
  hasMinor: boolean;
  raw: string;
};

export type ReorderChannel = {
  id: string;
  name: string;
  number: string;
};

export type ChannelNumberChange = {
  id: string;
  name: string;
  fromNumber: string;
  toNumber: string;
};

export type ReorderPlan = {
  channelId: string;
  channelName: string;
  fromIndex: number;
  targetIndex: number;
  fromNumber: string;
  toNumber: string;
  /** Channels whose numbers shift to make room (excludes the moved channel). */
  shifted: ChannelNumberChange[];
  /** Every channel whose stored number changes (moved channel first if changed, then shifted). */
  changes: ChannelNumberChange[];
  /** Batched payload for PATCH /livetv/channels/bulk `{ numbers }`. */
  nextNumbers: Record<string, string>;
  /** Exact previous numbers for one-request Undo. */
  prevNumbers: Record<string, string>;
};

function leadingInt(s: string): number {
  let end = 0;
  while (end < s.length) {
    const code = s.charCodeAt(end);
    if (code < 48 || code > 57) break;
    end++;
  }
  return end === 0 ? 0 : Number.parseInt(s.slice(0, end), 10);
}

export function parseChannelNumber(raw: string): ParsedChannelNumber {
  const trimmed = raw.trim();
  const dot = trimmed.indexOf(".");
  const hasMinor = dot >= 0;
  const majorPart = hasMinor ? trimmed.slice(0, dot) : trimmed;
  const minorPart = hasMinor ? trimmed.slice(dot + 1) : "";
  return {
    major: leadingInt(majorPart),
    minor: hasMinor ? leadingInt(minorPart) : 0,
    hasMinor,
    raw,
  };
}

export function compareParsedChannelNumber(a: ParsedChannelNumber, b: ParsedChannelNumber): number {
  if (a.major !== b.major) return a.major - b.major;
  if (a.minor !== b.minor) return a.minor - b.minor;
  return a.raw < b.raw ? -1 : a.raw > b.raw ? 1 : 0;
}

export function compareChannelNumber(a: string, b: string): number {
  return compareParsedChannelNumber(parseChannelNumber(a), parseChannelNumber(b));
}

/**
 * Convert a visual insertion slot (0..N, between rows) to the resulting index
 * in the list with the dragged channel removed (0..N-1).
 */
export function slotToTargetIndex(rawSlot: number, fromIndex: number, length: number): number {
  const slot = Math.max(0, Math.min(length, rawSlot));
  if (slot === fromIndex || slot === fromIndex + 1) return fromIndex;
  return slot < fromIndex ? slot : slot - 1;
}

/**
 * Convert a target index (0..N-1) back to the visual insertion line slot (0..N)
 * in the un-reordered list.
 */
export function targetIndexToSlot(targetIndex: number, fromIndex: number): number {
  return targetIndex > fromIndex ? targetIndex + 1 : targetIndex;
}

/**
 * Compute the minimal channel-number changes required to move `channelId` to
 * `toIndex` in `ordered` (which must already be sorted in channel-number order).
 *
 * - If a numeric gap exists between the new neighbours (integer gap or
 *   subchannel gap), only the moved channel is renumbered.
 * - If no gap exists, the moved channel takes the next number and only the
 *   immediately colliding following channels shift until the first gap.
 */
export function computeReorderPlan(
  ordered: readonly ReorderChannel[],
  channelId: string,
  toIndex: number,
): ReorderPlan | null {
  const n = ordered.length;
  if (n < 2) return null;
  const fromIndex = ordered.findIndex((c) => c.id === channelId);
  if (fromIndex < 0) return null;
  const targetIndex = Math.max(0, Math.min(n - 1, toIndex));
  if (targetIndex === fromIndex) return null;

  const moved = ordered[fromIndex];
  if (!moved) return null;
  const remaining = ordered.filter((_, i) => i !== fromIndex);
  const prev = targetIndex > 0 ? (remaining[targetIndex - 1] ?? null) : null;
  const next = targetIndex < remaining.length ? (remaining[targetIndex] ?? null) : null;

  const p = prev ? parseChannelNumber(prev.number) : null;
  const nx = next ? parseChannelNumber(next.number) : null;

  let assignedStr: string;
  if (!p && nx) {
    // Dropped at the very top of the lineup.
    if (nx.major > 1) {
      assignedStr = String(nx.major - 1);
    } else if (nx.major === 1 && nx.hasMinor && nx.minor > 0) {
      assignedStr = "1";
    } else {
      assignedStr = "1";
    }
  } else if (p && !nx) {
    // Dropped at the very bottom of the lineup.
    assignedStr = String(Math.max(1, p.major + 1));
  } else if (p && nx) {
    if (p.major === nx.major && nx.hasMinor && nx.minor > p.minor) {
      // Subchannel slot within the same major (e.g. 4.1..4.3 -> 4.2, or 4.1..4.2 -> 4.2 + shift).
      assignedStr = `${p.major}.${p.minor + 1}`;
    } else if (p.major === nx.major && (p.hasMinor || nx.hasMinor)) {
      // Duplicate subchannel number (e.g. 4.2 and 4.2 -> 4.3 + shift duplicate).
      assignedStr = `${p.major}.${Math.max(p.minor, nx.minor) + 1}`;
    } else if (nx.major - p.major > 1) {
      // Clean integer gap between majors (e.g. 5..100 -> 6, 10..12 -> 11).
      assignedStr = String(p.major + 1);
    } else if (nx.major === p.major + 1 && nx.hasMinor && nx.minor > 0) {
      // Integer fits right before next's subchannel (e.g. 2..3.1 -> 3).
      assignedStr = String(nx.major);
    } else {
      // Adjacent integer majors (e.g. 2..3) or duplicate integers (e.g. 2..2):
      // take next integer and shift following colliding channels.
      assignedStr = String(Math.max(1, p.major + 1));
    }
  } else {
    return null;
  }

  // Walk forward from `targetIndex`, shifting only channels that collide with
  // (or sort at/before) the previous assigned number, stopping at the first gap.
  const shifted: ChannelNumberChange[] = [];
  let lastKey = parseChannelNumber(assignedStr);
  let prevOrigMajor: number | null = null;
  let prevShiftedMajor: number | null = null;

  for (let i = targetIndex; i < remaining.length; i++) {
    const ch = remaining[i];
    if (!ch) continue;
    const ck = parseChannelNumber(ch.number);
    if (compareParsedChannelNumber(ck, lastKey) > 0) {
      break;
    }

    let nextStr: string;
    if (ck.hasMinor && ck.minor > 0) {
      if (
        prevOrigMajor !== null &&
        ck.major === prevOrigMajor &&
        prevShiftedMajor !== null &&
        (prevShiftedMajor > lastKey.major ||
          (prevShiftedMajor === lastKey.major && ck.minor > lastKey.minor))
      ) {
        // Keep subchannels attached to their shifted major (e.g. 3 -> 4, 3.1 -> 4.1).
        nextStr = `${prevShiftedMajor}.${ck.minor}`;
      } else {
        nextStr = `${lastKey.major}.${lastKey.minor + 1}`;
      }
    } else {
      const nextMajor = lastKey.major + 1;
      prevOrigMajor = ck.major;
      prevShiftedMajor = nextMajor;
      nextStr = String(nextMajor);
    }

    if (nextStr !== ch.number) {
      shifted.push({
        id: ch.id,
        name: ch.name,
        fromNumber: ch.number,
        toNumber: nextStr,
      });
    }
    lastKey = parseChannelNumber(nextStr);
  }

  const changes: ChannelNumberChange[] = [];
  if (assignedStr !== moved.number) {
    changes.push({
      id: moved.id,
      name: moved.name,
      fromNumber: moved.number,
      toNumber: assignedStr,
    });
  }
  changes.push(...shifted);

  if (changes.length === 0) return null;

  const nextNumbers: Record<string, string> = {};
  const prevNumbers: Record<string, string> = {};
  for (const ch of changes) {
    nextNumbers[ch.id] = ch.toNumber;
    prevNumbers[ch.id] = ch.fromNumber;
  }

  return {
    channelId: moved.id,
    channelName: moved.name,
    fromIndex,
    targetIndex,
    fromNumber: moved.number,
    toNumber: assignedStr,
    shifted,
    changes,
    nextNumbers,
    prevNumbers,
  };
}
