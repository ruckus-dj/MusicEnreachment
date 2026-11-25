import type { Locale } from "../../../i18n/types";
import { releaseMetadataEditorMessages } from "./releaseMetadataEditorMessages";

export type ContributorModeControlViewProps = {
  locale: Locale;
  inputId: string;
  value: "absent" | "value" | "explicit_empty";
  disabled: boolean;
  onChange: (state: "absent" | "value" | "explicit_empty") => void;
};

/** Contributor override-state control used by the grouped roles section. */
export function ContributorModeControlView({
  locale,
  inputId,
  value,
  disabled,
  onChange,
}: ContributorModeControlViewProps) {
  const text = releaseMetadataEditorMessages[locale];
  return (
    <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
      <label htmlFor={inputId}>{text.mode}</label>
      <select
        id={inputId}
        aria-label={`${text.groups.contributors} — ${text.mode}`}
        className="min-h-9 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)]"
        value={value}
        disabled={disabled}
        onChange={(event) =>
          onChange(
            event.currentTarget.value as "absent" | "value" | "explicit_empty",
          )
        }
      >
        <option value="absent">{text.absent}</option>
        <option value="value">{text.value}</option>
        <option value="explicit_empty">{text.explicitEmpty}</option>
      </select>
    </div>
  );
}
