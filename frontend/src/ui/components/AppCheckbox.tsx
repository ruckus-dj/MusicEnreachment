import { useId } from "react";
import { Checkbox } from "react-aria-components";
import { ui } from "./uiStyles";

export function AppCheckbox({
  label,
  description,
  describedBy,
  isSelected,
  onChange,
  isDisabled,
}: {
  label: string;
  description?: string;
  /** Ids of external descriptions rendered outside the checkbox label. */
  describedBy?: string;
  isSelected: boolean;
  onChange: (selected: boolean) => void;
  isDisabled?: boolean;
}) {
  const labelId = useId();
  const descriptionId = useId();
  return (
    <Checkbox
      isSelected={isSelected}
      onChange={onChange}
      isDisabled={isDisabled}
      aria-labelledby={labelId}
      aria-describedby={
        [description ? descriptionId : undefined, describedBy]
          .filter(Boolean)
          .join(" ") || undefined
      }
      className="group flex min-w-0 cursor-pointer items-start gap-2.5 data-[disabled]:cursor-not-allowed data-[disabled]:opacity-60"
    >
      <span className="mt-0.5 grid size-4 shrink-0 place-items-center rounded border border-[var(--control)] bg-[var(--surface)] text-[var(--button-text)] transition-colors group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[focus-visible]:outline-offset-2">
        <svg
          aria-hidden="true"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.8"
          strokeLinecap="round"
          strokeLinejoin="round"
          className="size-3 opacity-0 group-data-[selected]:opacity-100"
        >
          <path d="m3 8 3 3 7-7" />
        </svg>
      </span>
      <span className="grid min-w-0 gap-0.5">
        <span id={labelId} className="text-sm font-medium text-[var(--ink)]">
          {label}
        </span>
        {description ? (
          <span id={descriptionId} className={ui.fieldHelp}>
            {description}
          </span>
        ) : null}
      </span>
    </Checkbox>
  );
}
