import type { ReactNode } from "react";
import { Toolbar } from "react-aria-components";

/**
 * Ordinary grouping for search and filter controls. A RAC Toolbar captures
 * arrow keys for its own roving focus, which would steal caret movement from a
 * nested search input, so search/filter rows use a labeled group instead.
 */
export function AppToolbar({
  label,
  children,
  className = "",
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <fieldset
      className={`m-0 flex min-w-0 flex-wrap items-center gap-2 border-0 p-0${className ? ` ${className}` : ""}`}
    >
      <legend className="sr-only">{label}</legend>
      {children}
    </fieldset>
  );
}

/** RAC Toolbar for a row that contains only actions. */
export function AppActionToolbar({
  label,
  children,
  className = "",
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Toolbar
      aria-label={label}
      className={`flex flex-wrap items-center gap-2${className ? ` ${className}` : ""}`}
    >
      {children}
    </Toolbar>
  );
}
