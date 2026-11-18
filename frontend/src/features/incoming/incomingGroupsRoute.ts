export type IncomingGroupsRoute =
  | { view: "list"; expandedGroupIds: string[] }
  | { view: "matching"; groupId: string; releaseId?: string };

export const incomingGroupsListRoute: IncomingGroupsRoute = {
  view: "list",
  expandedGroupIds: [],
};

export function parseIncomingGroupsRoute(
  hash: string,
): IncomingGroupsRoute | undefined {
  const path = hash.startsWith("#") ? hash.slice(1) : hash;
  const [pathname, search = ""] = path.split("?", 2);
  if (pathname === "/incoming-groups") {
    return {
      view: "list",
      expandedGroupIds: [
        ...new Set(new URLSearchParams(search).getAll("group").filter(Boolean)),
      ],
    };
  }
  const match = /^\/incoming-groups\/([^/]+)\/matching$/.exec(pathname);
  if (!match) return undefined;
  try {
    const groupId = decodeURIComponent(match[1]);
    if (!groupId) return undefined;
    const releaseId = new URLSearchParams(search).get("release") ?? "";
    return releaseId
      ? { view: "matching", groupId, releaseId }
      : { view: "matching", groupId };
  } catch {
    return undefined;
  }
}

export function incomingGroupsListHref(
  expandedGroupIds: readonly string[] = [],
) {
  const params = new URLSearchParams();
  for (const groupId of expandedGroupIds) params.append("group", groupId);
  const query = params.toString();
  return query ? `#/incoming-groups?${query}` : "#/incoming-groups";
}

export function incomingGroupMatchingHref(groupId: string, releaseId?: string) {
  const path = `#/incoming-groups/${encodeURIComponent(groupId)}/matching`;
  return releaseId ? `${path}?release=${encodeURIComponent(releaseId)}` : path;
}
