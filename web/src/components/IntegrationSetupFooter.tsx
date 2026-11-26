import { Button } from "@braedonsaunders/appkit-ui";
import { t } from "../i18n";

export function IntegrationSetupFooter({
  onCancel,
  onSave,
  disabled,
  saveDisabled,
  saveLabel,
  form,
}: {
  onCancel: () => void;
  onSave?: () => void;
  disabled?: boolean;
  saveDisabled?: boolean;
  saveLabel?: string;
  form?: string;
}) {
  return (
    <div className="flex w-full justify-end gap-2">
      <Button variant="ghost" size="sm" disabled={disabled} onClick={onCancel}>
        {t("Cancel")}
      </Button>
      <Button
        size="sm"
        disabled={disabled || saveDisabled}
        type={form ? "submit" : "button"}
        form={form}
        onClick={onSave}
      >
        {saveLabel ?? t("Save and enable")}
      </Button>
    </div>
  );
}
