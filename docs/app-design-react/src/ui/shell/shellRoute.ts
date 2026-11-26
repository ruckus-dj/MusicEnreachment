import type { ApplicationFrameState } from "./ShellStartupView";
import type { ProductShellProps, ShellSectionId } from "./ShellView";

/**
 * Semantic route families used by the portable router and shell.
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

/** Resolves a route family to the navigation section. */
export function shellSectionForFamily(
  family: ShellRouteFamily | undefined,
): ShellSectionId | undefined {
  return family ? sectionByFamily[family] : undefined;
}

/** Data and destinations for the shell frame. */
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
