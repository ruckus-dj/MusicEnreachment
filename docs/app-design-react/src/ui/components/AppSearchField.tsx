import { type RefObject, useRef } from "react";
import {
  SearchField as AriaSearchField,
  Button,
  Input,
  Label,
  type SearchFieldProps,
} from "react-aria-components";
import { AppIcon } from "./AppIcon";
import { ui } from "./uiStyles";

export type AppSearchFieldProps = Omit<
  SearchFieldProps,
  "children" | "className"
> & {
  label?: string;
  placeholder?: string;
  className?: string;
  inputClassName?: string;
  /** Host-owned ref used to return focus to the input after filter changes. */
  inputRef?: RefObject<HTMLInputElement | null>;
};

/**
 * Search input following the React Aria SearchField pattern: visible or
 * accessible label, leading search affordance and a pointer/keyboard clear
 * action. Styling stays in literal Tailwind utilities.
 */
export function AppSearchField({
  label,
  placeholder,
  className = "",
  inputClassName = "",
  inputRef,
  ...props
}: AppSearchFieldProps) {
  const localRef = useRef<HTMLInputElement>(null);
  const resolvedRef = inputRef ?? localRef;
  return (
    <AriaSearchField
      {...props}
      className={`group grid min-w-0 gap-1.5${className ? ` ${className}` : ""}`}
    >
      {label ? <Label className={ui.fieldLabel}>{label}</Label> : null}
      <div className="relative min-w-0">
        <AppIcon
          name="search"
          className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-[var(--muted)]"
        />
        <Input
          ref={resolvedRef}
          placeholder={placeholder}
          className={`${ui.input} pl-9 pr-10${inputClassName ? ` ${inputClassName}` : ""}`}
        />
        <Button
          slot="clear"
          onPress={() => resolvedRef.current?.focus()}
          className="absolute right-1.5 top-1/2 grid size-7 -translate-y-1/2 cursor-pointer place-items-center rounded-md text-[var(--muted)] hover:bg-[var(--raised)] hover:text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] group-data-[empty]:hidden"
        >
          <AppIcon name="close" className="size-3.5" />
        </Button>
      </div>
    </AriaSearchField>
  );
}
