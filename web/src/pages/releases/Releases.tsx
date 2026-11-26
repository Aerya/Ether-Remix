import { WhoGetsWhat } from "./WhoGetsWhat";
import { localized, t } from "../../i18n";
import { ConfigSection } from "../../components/ConfigSection";
import { DomainPage, useRouteTab } from "../../components/layout";
import { RELEASE_SEARCH } from "../settings/schema";
import { StartSpeed } from "../playback/StartSpeed";
import { BlocklistTab } from "./Blocklist";
import { HistoryTab } from "./History";
import { PinnedTab } from "./Pinned";
import { QualityTab } from "./Quality";
import { RulesTab } from "./Rules";

const TABS = localized(
  () =>
    [
      { key: "profiles", label: t("Profiles") },
      { key: "rules", label: t("Rules") },
      { key: "check", label: t("Who gets what") },
      { key: "searching", label: t("Searching") },
      { key: "blocklist", label: t("Blocklist") },
      { key: "pinned", label: t("Pinned") },
      { key: "history", label: t("History") },
    ] as const,
);

/** Which release Ether picks for each play, and why. */
export default function Releases() {
  const tab = useRouteTab(TABS);
  return (
    <DomainPage title={t("Releases")} base="/releases" tabs={TABS} active={tab}>
      {tab === "check" ? <WhoGetsWhat /> : null}
      {tab === "profiles" ? <QualityTab /> : null}
      {tab === "rules" ? <RulesTab /> : null}
      {tab === "searching" ? (
        <>
          <StartSpeed />
          <ConfigSection
            configKey="resolver"
            endpoint="resolver"
            groups={RELEASE_SEARCH}
            label={t("Searching")}
          />
        </>
      ) : null}
      {tab === "blocklist" ? <BlocklistTab /> : null}
      {tab === "pinned" ? <PinnedTab /> : null}
      {tab === "history" ? <HistoryTab /> : null}
    </DomainPage>
  );
}
