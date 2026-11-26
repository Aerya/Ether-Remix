import { Select } from "@braedonsaunders/appkit-ui";
import { t } from "../../i18n";
import { useAsync } from "../../lib/hooks";
import { metadataSources, supportsMetadata } from "../../lib/metadataSources";

export function LibraryMetadataSource({
  mediaType,
  value,
  onChange,
  disabled,
}: {
  mediaType?: string;
  value: string;
  onChange: (source: string) => void;
  disabled?: boolean;
}) {
  const sources = useAsync(metadataSources, []);
  const compatible = sources.data?.filter((source) => supportsMetadata(source, mediaType)) ?? [];
  const configured = compatible.filter((source) => source.configured);
  const current = compatible.find((source) => source.id === value);
  return (
    <Select
      searchable={false}
      sheetTitle={t("Details from")}
      aria-label={t("Details from")}
      value={value}
      disabled={disabled || !sources.data}
      onChange={(event) => onChange(event.currentTarget.value)}
    >
      <option value="">
        {sources.error
          ? t("Could not load metadata sources")
          : !sources.data
            ? t("Loading…")
            : configured[0]
              ? t("Automatic ({source})", { source: configured[0].name })
              : t("No source configured")}
      </option>
      {value && !current?.configured ? (
        <option value={value} disabled>
          {t("{source} (unavailable)", { source: current?.name ?? value })}
        </option>
      ) : null}
      {configured.map((source) => (
        <option key={source.id} value={source.id}>
          {source.name}
        </option>
      ))}
    </Select>
  );
}
