import { useId, useState } from "react";
import { directoryPickerMessages } from "./DirectoryPickerMessages";
import {
  type BrowseDirectory,
  DirectoryPickerView,
} from "./DirectoryPickerView";

export type DirectoryPathFieldProps = {
  id: string;
  label: string;
  value: string;
  onSelect: (path: string) => void;
  disabled?: boolean;
  description?: string;
  "aria-describedby"?: string;
};

const focus =
  "focus-visible:relative focus-visible:z-10 focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-[var(--accent)]";

export function DirectoryPathFieldView({
  id,
  label,
  value,
  onSelect,
  disabled = false,
  description,
  "aria-describedby": ariaDescribedBy,
  browse,
  locale,
  resolveInitialPath,
}: DirectoryPathFieldProps & {
  browse: BrowseDirectory;
  locale: "en" | "ru";
  resolveInitialPath?: (value: string) => string;
}) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const descriptionId = useId();
  const helpIds = [ariaDescribedBy, description ? descriptionId : undefined]
    .filter(Boolean)
    .join(" ");
  return (
    <div className="grid min-w-0 gap-1.5">
      <label className="text-sm font-medium text-[var(--ink)]" htmlFor={id}>
        {label}
      </label>
      <div className="flex min-w-0">
        <input
          className={`${focus} h-9 w-full min-w-0 rounded-l-lg border border-r-0 border-[var(--control)] bg-[var(--surface)] px-3 font-mono text-xs text-[var(--ink)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)]`}
          id={id}
          type="text"
          readOnly
          value={value}
          disabled={disabled}
          aria-describedby={helpIds || undefined}
        />
        <button
          className={`${focus} h-9 flex-none rounded-r-lg border border-[var(--control)] bg-[var(--surface)] px-3 text-sm font-medium whitespace-nowrap text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] disabled:cursor-not-allowed disabled:bg-[var(--raised)] disabled:text-[var(--muted)]`}
          type="button"
          disabled={disabled}
          onClick={() => setPickerOpen(true)}
        >
          {directoryPickerMessages[locale].choose}
        </button>
      </div>
      {description && (
        <p id={descriptionId} className="m-0 text-xs text-[var(--muted)]">
          {description}
        </p>
      )}
      <DirectoryPickerView
        open={pickerOpen}
        initialPath={resolveInitialPath?.(value) ?? value}
        browse={browse}
        locale={locale}
        onSelect={onSelect}
        onClose={() => setPickerOpen(false)}
      />
    </div>
  );
}
