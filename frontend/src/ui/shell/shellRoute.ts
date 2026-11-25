import type { ApplicationFrameState } from "./ShellStartupView";
import {
  type ProductShellProps,
  type ShellSectionId,
  shellSections,
} from "./ShellView";

/**
 * Semantic route families shared by both hosts. Hosts keep their own URL
 * grammar, aliases and entity identifiers; only the family and its shell
 * presentation are portable.
 */
export const shellRouteFamilies = [
  "home",
  "setup",
  "review",
  "library",
  "local",
  "sources",
  "operations",
  "settings",
] as const;

export type ShellRouteFamily = (typeof shellRouteFamilies)[number];

const sectionByFamily: Partial<Record<ShellRouteFamily, ShellSectionId>> = {
  review: "review",
  library: "library",
  local: "local",
  sources: "sources",
  operations: "operations",
  settings: "settings",
};

/** Resolves a host-owned route family to the portable navigation section. */
export function shellSectionForFamily(
  family: ShellRouteFamily | undefined,
): ShellSectionId | undefined {
  return family ? sectionByFamily[family] : undefined;
}

/** Resolves one navigation section name; entity identifiers stay in the host. */
export function shellSectionId(value: string): ShellSectionId | undefined {
  return shellSections.find((id) => id === value);
}

/** Host data for the shell frame; routing and URL grammar stay in the host. */
export type ShellFrameData = Omit<
  ProductShellProps,
  "locale" | "themeStore" | "activeSection" | "fallbackSection" | "children"
>;

/**
 * Selects the portable application frame for a route family. The host decides
 * whether startup authority applies; active navigation and fallback selection
 * stay here.
 */
export function shellApplicationFrameState(
  family: ShellRouteFamily | undefined,
  mode: "startup" | "product",
  frame: ShellFrameData,
): ApplicationFrameState {
  if (mode === "startup") return { kind: "startup" };
  return {
    kind: "product",
    ...frame,
    activeSection: shellSectionForFamily(family),
    fallbackSection:
      family === "home" || family === "setup" ? "home" : "library",
  };
}
