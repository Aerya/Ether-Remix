import { SearchSelect } from "@braedonsaunders/appkit-ui";
import { SettingsRow } from "../../components/SettingsRow";
import { CONTENT_RATINGS } from "../../components/UserLimits";
import { canonicalRating } from "../../lib/contentRatings";
import { t } from "../../i18n";

export function UnratedChannelRating({
  value,
  onChange,
  className,
}: {
  value: string;
  onChange: (value: string) => void;
  /** Lets a form align the row with its own fields (e.g. no side padding). */
  className?: string;
}) {
  return (
    <SettingsRow
      className={className}
      title={t("Rating for channels without one")}
      description={t(
        "Profiles with a rating limit hide channels that have no rating. Pick the rating these channels should count as.",
      )}
      control={
        <div className="w-56">
          <SearchSelect
            value={canonicalRating(value)}
            onChange={onChange}
            options={[
              { value: "", label: t("Not set") },
              ...CONTENT_RATINGS.filter((r) => r.value),
            ]}
            ariaLabel={t("Rating for channels without one")}
            searchable={false}
            placeholder={t("Not set")}
            selectLabel={t("Select")}
            closeLabel={t("Close")}
          />
        </div>
      }
    />
  );
}
