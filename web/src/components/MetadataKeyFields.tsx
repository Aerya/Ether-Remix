import { t } from "../i18n";
import { builtinMetadataSources, type BuiltinMetadataSource } from "../lib/metadataSources";
import { Field, SecretInput, TestButton, type TestState } from "./fields";

export function MetadataKeyFields({
  source,
  value,
  test,
  disabled,
  allowEmptyTest,
  onChange,
  onTest,
}: {
  source: BuiltinMetadataSource;
  value: string;
  test: TestState;
  disabled?: boolean;
  allowEmptyTest?: boolean;
  onChange: (value: string) => void;
  onTest: () => void;
}) {
  const provider = builtinMetadataSources[source];
  return (
    <div className="space-y-3">
      <Field label={provider.label()}>
        <SecretInput
          ariaLabel={provider.label()}
          value={value}
          disabled={disabled || test.status === "running"}
          onChange={onChange}
        />
      </Field>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <TestButton
          state={test}
          onTest={onTest}
          disabled={disabled || (!allowEmptyTest && !value.trim())}
        />
        <a
          href={provider.keyURL}
          target="_blank"
          rel="noreferrer"
          className="text-sm text-primary underline"
        >
          {t("Get an API key")}
        </a>
      </div>
    </div>
  );
}
