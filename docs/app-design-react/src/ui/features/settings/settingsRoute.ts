export const settingsTabIDs = [
  "general",
  "folders",
  "tools",
  "providers",
  "genres",
] as const;

export type SettingsTabID = (typeof settingsTabIDs)[number];

export const defaultSettingsTab: SettingsTabID = "general";

const settingsPath = "/settings";

export function isSettingsTabID(
  value: string | null | undefined,
): value is SettingsTabID {
  return settingsTabIDs.some((tab) => tab === value);
}

export function parseSettingsRoute(
  hash: string,
): { tab: SettingsTabID } | undefined {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const queryStart = path.indexOf("?");
  const routePath = queryStart === -1 ? path : path.slice(0, queryStart);
  if (routePath !== settingsPath) return undefined;
  const params = new URLSearchParams(
    queryStart === -1 ? "" : path.slice(queryStart + 1),
  );
  const requestedTab = params.get("tab");
  return {
    tab: isSettingsTabID(requestedTab) ? requestedTab : defaultSettingsTab,
  };
}

export function settingsTabFromHash(hash: string): SettingsTabID {
  return parseSettingsRoute(hash)?.tab ?? defaultSettingsTab;
}

export function settingsTabHash(tab: SettingsTabID): string {
  return tab === defaultSettingsTab
    ? `#${settingsPath}`
    : `#${settingsPath}?tab=${tab}`;
}
