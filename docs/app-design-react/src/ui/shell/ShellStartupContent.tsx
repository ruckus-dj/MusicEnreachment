import type { ComponentType } from "react";
import type { Locale } from "../i18n/types";
import { ShellStartupView } from "./ShellStartupView";

/** Host-owned Setup entry point; the portable tree decides when to mount it. */
export interface ShellSetupBinding<Props extends object> {
  component: ComponentType<Props>;
  props: Props;
}

/**
 * Typed startup facts. The host owns the setup query and its authority; this
 * tree selects the canonical startup presentation and mounts the Setup facade.
 */
export type ShellStartupFacts<SetupProps extends object> =
  | { kind: "loading-server" }
  | { kind: "setup-error"; message?: string; retry: () => void }
  | {
      kind: "diagnostic";
      message?: string;
      platform: string;
      retry: () => void;
    }
  | { kind: "setup"; setup: ShellSetupBinding<SetupProps> };

export function ShellStartupContent<SetupProps extends object>({
  locale,
  facts,
}: {
  locale: Locale;
  facts: ShellStartupFacts<SetupProps>;
}) {
  if (facts.kind === "setup") {
    const Setup = facts.setup.component;
    return <Setup {...facts.setup.props} />;
  }
  return <ShellStartupView locale={locale} state={facts} />;
}
