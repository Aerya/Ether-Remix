import { t } from "../../../i18n";

/** Translate raw Go / network / downloader import errors into plain owner-friendly reasons. */
export function friendlyMediaError(raw?: string | null): string {
  const text = (raw ?? "").trim();
  const lower = text.toLowerCase();
  if (!lower) return t("Couldn’t import this media. Check the link and try again.");

  if (
    lower.includes("context deadline exceeded") ||
    lower.includes("timed out") ||
    lower.includes("timeout")
  ) {
    return t("Timed out while downloading. Check the link and try again.");
  }
  if (lower.includes("context canceled") || lower.includes("cancelled")) {
    return t("Import was cancelled.");
  }
  if (lower.includes("yt-dlp") || lower.includes("downloader")) {
    return t("Install the video downloader to import from this site.");
  }
  if (
    lower.includes("cannot resolve media host") ||
    lower.includes("no such host") ||
    lower.includes("connection refused") ||
    lower.includes("network is unreachable")
  ) {
    return t("Couldn’t reach that website. Check the link and network connection.");
  }
  if (lower.includes("private media")) {
    return t("Only public web links can be imported.");
  }
  if (lower.includes("public http media link") || lower.includes("invalid http media link")) {
    return t("Enter a valid http:// or https:// media link.");
  }
  if (lower.includes("too many redirects")) {
    return t("That link redirected too many times.");
  }
  if (
    lower.includes("http 404") ||
    lower.includes("http 410") ||
    lower.includes("archive metadata unavailable")
  ) {
    return t("Media wasn’t found at that link.");
  }
  if (lower.includes("http 401") || lower.includes("http 403")) {
    return t("That website denied access to the media.");
  }
  if (lower.includes("http 429")) {
    return t("That site is limiting downloads right now. Try again in a few minutes.");
  }
  if (/http 5\d\d/.test(lower)) {
    return t("That website returned a server error. Try again shortly.");
  }
  if (lower.includes("archive item has no mp4 video")) {
    return t("No playable MP4 video was found at that Archive.org link.");
  }
  if (
    lower.includes("exceeds size cap") ||
    lower.includes("between 1 byte and 32 mb") ||
    lower.includes("500 mb")
  ) {
    return t("That file is larger than the size limit.");
  }
  if (lower.includes("5 gb reel budget")) {
    return t("Reel storage is full (5 GB limit). Remove unused clips to free space.");
  }
  if (lower.includes("at most 30 seconds")) {
    return t("Library clips can be at most 30 seconds long.");
  }
  if (lower.includes("maximum 3 minutes")) {
    return t("Reel clips can be at most 3 minutes long.");
  }
  if (
    lower.includes("invalid media content type") ||
    lower.includes("html media page") ||
    lower.includes("not video media") ||
    lower.includes("not audio media") ||
    lower.includes("not image media") ||
    lower.includes("has no video stream") ||
    lower.includes("has no audio stream")
  ) {
    return t("No supported media stream was found at that link.");
  }
  if (lower.includes("empty media") || lower.includes("normalized link clip is empty")) {
    return t("The downloaded media file was empty.");
  }
  if (lower.includes("import queue full")) {
    return t("Too many imports are running right now. Try again shortly.");
  }
  return t("Couldn’t import this media. Check the link and try again.");
}
