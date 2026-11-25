import { AppButton } from "../../components/AppButton";

export function SettingsDraftConflictNotice({
  summary,
  label,
  announcement,
  takeSaved,
  continueEditing,
  disabled,
}: {
  summary: string;
  label?: string;
  announcement: "alert" | "status";
  takeSaved: { label: string; onPress: () => void };
  continueEditing: { label: string; onPress: () => void };
  disabled?: boolean;
}) {
  return (
    <section
      aria-label={label}
      className="grid gap-2 rounded border border-[var(--warning)] bg-[var(--warning-soft)] p-3"
      role={announcement}
    >
      <p className="m-0 wrap-anywhere">{summary}</p>
      <div className="flex flex-wrap gap-2">
        <AppButton isDisabled={disabled} onPress={takeSaved.onPress}>
          {takeSaved.label}
        </AppButton>
        <AppButton isDisabled={disabled} onPress={continueEditing.onPress}>
          {continueEditing.label}
        </AppButton>
      </div>
    </section>
  );
}
