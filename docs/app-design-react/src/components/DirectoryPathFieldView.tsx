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
  "focus-visible:relative focus-visible:z-10 focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-[var(--focus)]";

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
    <div className="grid min-w-0 gap-1">
      <label className="font-semibold" htmlFor={id}>
        {label}
      </label>
      <div className="flex min-w-0">
        <input
          className={`${focus} min-h-9 w-full min-w-0 rounded-l-[5px] border border-r-0 border-[var(--control)] bg-[var(--raised)] px-[9px] py-1.5 text-[var(--ink)] disabled:text-[var(--muted)] max-[599px]:min-h-10`}
          id={id}
          type="text"
          readOnly
          value={value}
          disabled={disabled}
          aria-describedby={helpIds || undefined}
        />
        <button
          className={`${focus} min-h-9 flex-none rounded-r-[5px] border border-[var(--control)] bg-[var(--surface)] px-2.5 py-1.5 whitespace-nowrap text-[var(--ink)] enabled:hover:border-[var(--accent)] enabled:hover:bg-[var(--raised)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] max-[599px]:min-h-10`}
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
