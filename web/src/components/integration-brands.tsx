import postersplus from "../assets/brands/postersplus.png?no-inline";
import xrdb from "../assets/brands/xrdb.png?no-inline";
import betterposters from "../assets/brands/betterposters.png?no-inline";
import rpdb from "../assets/brands/rpdb.jpg?no-inline";
import { Plug } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { apiBlob, imageNeedsFetch } from "../lib/api";
import dlna from "../assets/brands/dlna.svg?no-inline";
import emby from "../assets/brands/emby.svg?no-inline";
import m3u from "../assets/brands/m3u.svg?no-inline";
import opensubsonic from "../assets/brands/opensubsonic.svg?no-inline";
import webdav from "../assets/brands/webdav.svg?no-inline";
import xtream from "../assets/brands/xtream.svg?no-inline";
import jellyfin from "../assets/brands/jellyfin.svg?no-inline";
import plex from "../assets/brands/plex.svg?no-inline";
import silo from "../assets/brands/silo.png?no-inline";
import vela from "../assets/brands/vela.svg?no-inline";
import simkl from "../assets/brands/simkl.svg?no-inline";
import tmdb from "../assets/brands/themoviedatabase.svg?no-inline";
import trakt from "../assets/brands/trakt.svg?no-inline";

/**
 * Real vendored marks for IntegrationAppCard's `logo` slot.
 * The card does not choose one from `identity.id`; the caller passes it.
 * Color follows the surrounding text (`currentColor` via a mask).
 */
function BrandMark({ src, color }: { src: string; color: string }) {
  return (
    <span
      aria-hidden
      className="block size-7 bg-current"
      style={{
        color,
        maskImage: `url("${src}")`,
        maskPosition: "center",
        maskSize: "contain",
        maskRepeat: "no-repeat",
        WebkitMaskImage: `url("${src}")`,
        WebkitMaskPosition: "center",
        WebkitMaskSize: "contain",
        WebkitMaskRepeat: "no-repeat",
      }}
    />
  );
}

export function PlexMark() {
  return <BrandMark src={plex} color="#e5a00d" />;
}
export function JellyfinMark() {
  return <BrandMark src={jellyfin} color="#aa5cc3" />;
}
export function EmbyMark() {
  return <BrandMark src={emby} color="#52b54b" />;
}
/** Silo's own full-colour app icon, so it is an image rather than a mask. */
export function SiloMark() {
  return <img src={silo} alt="" aria-hidden className="block size-7 rounded-md" />;
}

/** Vela's app icon, from the Vela brand kit. */
export function VelaMark() {
  return <img src={vela} alt="" aria-hidden className="block size-7 rounded-md" />;
}

/**
 * A plugin adapter's mark, served by Ether from the plugin package (its
 * declared adapter icon, else the plugin's icon). Drawn like Silo and Vela;
 * the plug stands in when there is none or it fails to load.
 *
 * Served icons under `/ether/api/...` require owner authentication, so when an
 * owner token (or fetch-routed mock mode) is active we read them through
 * `apiBlob` into a short-lived object URL instead of mounting an unauthenticated
 * `<img>`.
 */
export function PluginAdapterMark({ src, fallback }: { src?: string; fallback?: ReactNode }) {
  const validSrc = src && /^(https?:|\/)/.test(src) ? src : undefined;
  const needsFetch = imageNeedsFetch(validSrc);
  const [failed, setFailed] = useState<string>();
  const [loaded, setLoaded] = useState<{ src: string; url: string }>();

  useEffect(() => {
    if (!validSrc || !needsFetch) return;
    const controller = new AbortController();
    let created: string | undefined;
    void apiBlob(validSrc, controller.signal)
      .then((blob) => {
        if (!controller.signal.aborted) {
          created = URL.createObjectURL(blob);
          setLoaded({ src: validSrc, url: created });
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setFailed(validSrc);
      });
    return () => {
      controller.abort();
      if (created) URL.revokeObjectURL(created);
    };
  }, [needsFetch, validSrc]);

  const resolved = !validSrc
    ? undefined
    : needsFetch
      ? loaded?.src === validSrc
        ? loaded.url
        : undefined
      : validSrc;

  if (!resolved || failed === validSrc) return fallback ?? <Plug size={22} aria-hidden />;
  return (
    <img
      src={resolved}
      alt=""
      aria-hidden
      className="block size-7 rounded-md object-contain"
      onError={() => setFailed(validSrc)}
    />
  );
}

export function DlnaMark() {
  return <BrandMark src={dlna} color="#48a842" />;
}
/** WebDAV has no official logo; Ether's own badge (cloud + DAV), not a folder. */
export function WebdavMark() {
  return <BrandMark src={webdav} color="#3d8bfd" />;
}
/** M3U has no official logo; Ether's own playlist-file badge. */
export function M3uMark() {
  return <BrandMark src={m3u} color="#f08c2e" />;
}
/** Xtream has no usable logo; Ether's own IPTV badge. */
export function XtreamMark() {
  return <BrandMark src={xtream} color="#17a9a0" />;
}
/** The OpenSubsonic logo on its own tile, the same mark ether.music declares. */
export function SubsonicMark() {
  return <img src={opensubsonic} alt="" aria-hidden className="block size-7 rounded-md" />;
}

/** Marks for protocols a plugin adapter can declare (`protocolHint`). */
export function protocolMark(protocolHint?: string): ReactNode | undefined {
  return protocolHint === "subsonic" ? <SubsonicMark /> : undefined;
}

/**
 * A plugin adapter's mark: the adapter's own declared icon first, then the
 * mark of the protocol it declares (so Subsonic looks like Subsonic before
 * its plugin ships an icon), then the plugin's icon, then the plug.
 * `logo` is an adapter's own icon when Ether serves it with `adapter=`.
 */
export function pluginAdapterLogo(logo?: string, protocolHint?: string): ReactNode {
  const mark = protocolMark(protocolHint);
  if (mark && !(logo && /[?&]adapter=/.test(logo))) return mark;
  return <PluginAdapterMark src={logo} fallback={mark} />;
}

/** Built-in household apps without a vendored client brand (Plex & co. draw their own). */
export function householdIcon(id: string): ReactNode | undefined {
  switch (id) {
    case "dlna":
      return <DlnaMark />;
    case "webdav":
      return <WebdavMark />;
    case "m3u":
      return <M3uMark />;
    case "xtream":
      return <XtreamMark />;
    default:
      return id.startsWith("plugin:") ? <Plug size={22} aria-hidden /> : undefined;
  }
}
export function SimklMark() {
  return <BrandMark src={simkl} color="currentColor" />;
}
/** MDBList has no vendored mark; a list/clapper glyph stands in. */
export function MDBListMark() {
  return (
    <svg
      aria-hidden
      viewBox="0 0 32 32"
      className="size-7"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M4 8h24M4 14h24M4 20h16M4 26h10" />
    </svg>
  );
}
export function TmdbMark() {
  return <BrandMark src={tmdb} color="#01b4e4" />;
}
export function TraktMark() {
  return <BrandMark src={trakt} color="#ed1c24" />;
}

/** Distinct poster-service symbols; Custom deliberately uses a neutral frame. */
export function IntegrationMark({ id }: { id: string }) {
  if (id === "tmdb") return <TmdbMark />;
  if (id === "trakt") return <TraktMark />;
  if (id === "simkl") return <SimklMark />;
  if (id === "mdblist") return <MDBListMark />;
  if (id === "plexservers") return <PlexMark />;
  const posters: Record<string, string> = { postersplus, xrdb, betterposters, rpdb };
  if (posters[id])
    return (
      <img
        src={posters[id]}
        alt=""
        aria-hidden
        className={
          id === "xrdb" || id === "postersplus"
            ? "block size-8 rounded bg-fg dark:bg-bg-subtle p-1 object-contain"
            : "block size-8 object-contain"
        }
      />
    );
  return (
    <svg
      aria-hidden
      viewBox="0 0 32 32"
      className="size-8"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {id === "llm" ? (
        <>
          <path d="m16 3 3.5 9.5L29 16l-9.5 3.5L16 29l-3.5-9.5L3 16l9.5-3.5Z" />
        </>
      ) : id === "introdb" ? (
        <>
          <circle cx="16" cy="16" r="13" />
          <path d="m12 10 9 6-9 6Zm11 0v12" />
        </>
      ) : id === "ratings" ? (
        <>
          <path d="m16 3 4 8 9 1-6.5 6.5L24 28l-8-4-8 4 1.5-9.5L3 12l9-1Z" />
        </>
      ) : id === "opensubtitles" ? (
        <>
          <rect x="3" y="5" width="26" height="22" rx="4" />
          <path d="M8 15h6m4 0h6M8 21h16" />
        </>
      ) : (
        <>
          <rect x="5" y="3" width="22" height="26" rx="3" />
          <circle cx="12" cy="11" r="2" />
          <path d="m6 24 7-8 5 5 5-6 4 4" />
        </>
      )}
    </svg>
  );
}
