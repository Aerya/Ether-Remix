import * as S from "../settings/schema";
import { useMemo } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useApp } from "../../app-context";
import { ConfigSection } from "../../components/ConfigSection";
import { SectionHeading } from "../../components/feedback";
import { DomainPage, SubtabNav, useRouteTab } from "../../components/layout";
import { localized, t, tx } from "../../i18n";
import { useLocale } from "../../i18n/react";
import { settingHref } from "../../components/SettingFocus";
import { DeviceCapabilities } from "./Devices";
import {
  HardwareStatus,
  canUseRtxVideo,
  canUseVaapi,
  useHardwareStatus,
  type Hardware,
} from "./Hardware";
import { MarkerCoverage } from "./Markers";
import { SkipButtons } from "./SkipButtons";
import { HouseholdAudio } from "./HouseholdAudio";
import { Prerolls } from "./Prerolls";
import { SubtitleTiming } from "./SubtitleTiming";

/** Where subtitle providers are set up: their group of cards in Integrations. */
const SUBTITLE_PROVIDERS_HREF = settingHref("/services/overview", "subtitle-providers");

const TABS = localized(
  () =>
    [
      { key: "audio", label: t("Audio") },
      { key: "subtitles", label: t("Subtitles") },
      { key: "transcoding", label: t("Streams & transcoding") },
      { key: "devices", label: t("App compatibility") },
      { key: "extras", label: t("Extras") },
      { key: "prerolls", label: t("Pre-rolls") },
    ] as const,
);

function TrailerStatus({ draft }: { draft: Record<string, unknown> }) {
  if (!draft.enabled || String(draft.resolverCommand ?? "").trim()) return null;
  return (
    <p role="status" className="text-sm text-fg-muted">
      {t(
        "Trailers use yt-dlp. Install yt-dlp, or set your own resolver command under More options.",
      )}
    </p>
  );
}

const VIDEO_TRANSCODING_OFF_DISABLED = new Set([
  "hwAccel",
  "vaapiDevice",
  "maxTranscodes",
  "rtxVideo",
]);

/**
 * How a chosen release reaches the screen: audio, subtitles, streams and
 * transcoding, what each app can play, and what plays around the title.
 * Forms stay mounted across tabs, so edits on several tabs save together.
 */
export default function Playback() {
  const tab = useRouteTab(TABS);
  const { section } = useParams();
  const navigate = useNavigate();
  // Subtitles splits into how subtitles behave (settings) and the tool that
  // repairs a downloaded file (timing). The settings draft stays mounted on
  // both, so an unsaved change survives a look at the tool.
  const subtitlesPart = tab === "subtitles" && section === "timing" ? "timing" : "settings";
  const { locale } = useLocale();
  const { dashboard } = useApp();
  const hw = useHardwareStatus(tab === "transcoding");
  const hwData = hw.data ?? (dashboard?.sections?.hwaccel as Hardware | undefined) ?? null;
  const showVaapi = canUseVaapi(hwData);
  const showRtx = canUseRtxVideo(hwData);
  const transcodeGroups = useMemo(
    () =>
      S.TRANSCODE.filter((g) => g.id !== "enhance" || showRtx).map((g) =>
        g.id === "convert" && !showVaapi
          ? { ...g, more: g.more?.filter((f) => f.key !== "vaapiDevice") }
          : g,
      ),
    [locale, showRtx, showVaapi],
  );
  return (
    <DomainPage title={t("Playback")} base="/playback" tabs={TABS} active={tab}>
      {tab === "transcoding" ? <HardwareStatus state={hw} /> : null}
      <ConfigSection
        hidden={tab !== "transcoding"}
        configKey="server"
        endpoint="server"
        groups={S.STREAM_LIMITS}
        label={t("Who can watch at once")}
      />
      <ConfigSection
        hidden={tab !== "transcoding"}
        configKey="transcode"
        endpoint="transcode"
        groups={transcodeGroups}
        fieldDisabled={(field, draft) =>
          draft.videoTranscoding === "off" && VIDEO_TRANSCODING_OFF_DISABLED.has(field.key)
        }
        label={t("Streams & transcoding")}
      />
      <ConfigSection
        hidden={tab !== "transcoding"}
        configKey="liveTV"
        endpoint="livetv"
        groups={S.LIVE_LATENCY}
        label={t("Live TV")}
      />
      {tab === "devices" ? <DeviceCapabilities /> : null}
      {tab === "prerolls" ? (
        <SectionHeading
          title={t("Before the feature")}
          description={t(
            "What plays before a movie or episode starts: your pre-rolls and cinema trailers.",
          )}
        />
      ) : null}
      <ConfigSection
        hidden={tab !== "prerolls"}
        configKey="extras"
        groups={S.EXTRAS}
        label={t("Trailers")}
        before={(draft) => <TrailerStatus draft={draft} />}
      />
      {tab === "prerolls" ? <Prerolls /> : null}
      <SkipButtons hidden={tab !== "extras"} />
      {tab === "extras" ? <MarkerCoverage /> : null}
      <ConfigSection
        hidden={tab !== "extras"}
        configKey="personalize"
        groups={S.UP_NEXT}
        label={t("Up next")}
      />
      {tab === "subtitles" ? (
        <SubtabNav
          ariaLabel={t("Subtitles sections")}
          active={subtitlesPart}
          onSelect={(key) =>
            navigate(key === "timing" ? "/playback/subtitles/timing" : "/playback/subtitles")
          }
          tabs={[
            { key: "settings", label: t("Settings") },
            { key: "timing", label: t("Timing fixes") },
          ]}
        />
      ) : null}
      {tab === "audio" || tab === "subtitles" ? (
        <HouseholdAudio part={tab} hidden={tab === "subtitles" && subtitlesPart === "timing"} />
      ) : null}
      {tab === "subtitles" && subtitlesPart === "settings" ? (
        <p className="max-w-3xl text-sm text-fg-muted">
          {tx(
            "Missing subtitles are downloaded from the subtitle providers you set up in {link}.",
            {
              link: (
                <Link to={SUBTITLE_PROVIDERS_HREF} className="font-medium text-primary underline">
                  {t("Integrations")}
                </Link>
              ),
            },
          )}
        </p>
      ) : null}
      {tab === "subtitles" && subtitlesPart === "timing" ? <SubtitleTiming /> : null}
    </DomainPage>
  );
}
