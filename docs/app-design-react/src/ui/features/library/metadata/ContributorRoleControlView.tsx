import type { Locale } from "../../../i18n/types";
import { releaseMetadataEditorMessages } from "./releaseMetadataEditorMessages";

export type ContributorRoleControlViewProps = {
  locale: Locale;
  inputId: string;
  value: string;
  index: number;
  disabled: boolean;
  onChange: (role: string) => void;
};

/**
 * Per-contributor role picker. The provider owns the draft row and focus
 * transition; the view owns the labelled select and its role catalog.
 */
export function ContributorRoleControlView({
  locale,
  inputId,
  value,
  index,
  disabled,
  onChange,
}: ContributorRoleControlViewProps) {
  const text = releaseMetadataEditorMessages[locale];
  return (
    <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
      <label htmlFor={inputId}>{`${text.contributorRole} ${index + 1}`}</label>
      <select
        id={inputId}
        className="min-h-9 w-full rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)]"
        value={value}
        disabled={disabled}
        onChange={(event) => {
          const role = event.currentTarget.value;
          if (role === value) return;
          onChange(role);
        }}
      >
        <option value="">{text.contributorRole}</option>
        {value && !Object.hasOwn(text.contributorRoles, value) ? (
          <option value={value}>{value}</option>
        ) : null}
        {Object.entries(text.contributorRoles).map(([role, roleLabel]) => (
          <option key={role} value={role}>
            {roleLabel}
          </option>
        ))}
      </select>
    </div>
  );
}
