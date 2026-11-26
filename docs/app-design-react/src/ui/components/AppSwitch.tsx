import { useId } from "react";
import { Switch } from "react-aria-components";
import { ui } from "./uiStyles";

export function AppSwitch({
  label,
  description,
  describedBy,
  isSelected,
  onChange,
  isDisabled,
}: {
  label: string;
  description?: string;
  /** Ids of external descriptions rendered outside the switch label. */
  describedBy?: string;
  isSelected: boolean;
  onChange: (selected: boolean) => void;
  isDisabled?: boolean;
}) {
  const labelId = useId();
  const descriptionId = useId();
  return (
    <Switch
      isSelected={isSelected}
      onChange={onChange}
      isDisabled={isDisabled}
      aria-labelledby={labelId}
      aria-describedby={
        [description ? descriptionId : undefined, describedBy]
          .filter(Boolean)
          .join(" ") || undefined
      }
      className="group relative flex min-w-0 cursor-pointer items-start gap-3 data-[disabled]:cursor-not-allowed data-[disabled]:opacity-60"
    >
      <span className="box-border flex h-5 w-9 shrink-0 items-center rounded-full border border-[var(--control)] bg-[var(--raised)] px-px transition-colors group-data-[selected]:border-[var(--accent)] group-data-[selected]:bg-[var(--accent)] group-data-[focus-visible]:outline-2 group-data-[focus-visible]:outline-[var(--accent)] group-data-[focus-visible]:outline-offset-2">
        <span className="size-4 rounded-full bg-[var(--surface)] shadow-sm transition-transform group-data-[selected]:translate-x-4" />
      </span>
      <span className="grid min-w-0 gap-0.5">
        <span
          id={labelId}
          className="block text-sm leading-5 font-medium text-[var(--ink)]"
        >
          {label}
        </span>
        {description ? (
          <span id={descriptionId} className={ui.fieldHelp}>
            {description}
          </span>
        ) : null}
      </span>
    </Switch>
  );
}
