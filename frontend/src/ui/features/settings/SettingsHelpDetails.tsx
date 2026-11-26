import { type ReactNode, useContext } from "react";
import {
  Button,
  Disclosure,
  DisclosurePanel,
  DisclosureStateContext,
} from "react-aria-components";

const styles = {
  disclosure: "min-w-0",
  trigger:
    "inline-flex min-h-6 w-fit cursor-pointer items-center gap-1 rounded text-xs font-medium text-[var(--accent-strong)] underline-offset-2 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]",
  panel:
    "react-aria-DisclosurePanel ui-disclosure-panel mt-1 min-w-0 overflow-clip text-xs text-[var(--muted)] [&_p]:m-0 [&_p]:text-xs [&_p]:text-[var(--muted)]",
} as const;

function DisclosureChevron() {
  const state = useContext(DisclosureStateContext);
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 16 16"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={`size-3.5 shrink-0 motion-safe:transition-transform ${
        state?.isExpanded ? "rotate-90" : ""
      }`}
    >
      <path d="m6 3 5 5-5 5" />
    </svg>
  );
}

/** Collapsible explanation for uncommon settings detail. */
export function SettingsHelpDetails({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <Disclosure className={styles.disclosure}>
      <Button slot="trigger" className={styles.trigger}>
        <DisclosureChevron />
        <span>{label}</span>
      </Button>
      <DisclosurePanel className={styles.panel}>{children}</DisclosurePanel>
    </Disclosure>
  );
}
