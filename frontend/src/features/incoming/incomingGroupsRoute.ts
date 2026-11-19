export type IncomingGroupsRoute =
  | { view: "list"; expandedGroupIds: string[] }
  | {
      view: "matching";
      groupId: string;
      releaseId?: string;
      localReleaseId?: string;
      prepareReleaseId?: string;
      creatingLocalRelease?: boolean;
    };

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
  const match =
    /^\/incoming-groups\/([^/]+)\/matching(?:\/(local-release))?$/.exec(
      pathname,
    );
  if (!match) return undefined;
  try {
    const groupId = decodeURIComponent(match[1]);
    if (!groupId) return undefined;
    const params = new URLSearchParams(search);
    const releaseId = params.get("release");
    const localReleaseId = params.get("local");
    const prepareReleaseId = params.get("prepare");
    return {
      view: "matching",
      groupId,
      ...(releaseId ? { releaseId } : {}),
      ...(localReleaseId ? { localReleaseId } : {}),
      ...(prepareReleaseId ? { prepareReleaseId } : {}),
      ...(match[2] ? { creatingLocalRelease: true } : {}),
    };
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

export function incomingGroupMatchingHref(
  groupId: string,
  releaseId?: string,
  options?: {
    localReleaseId?: string;
    prepareReleaseId?: string;
    creatingLocalRelease?: boolean;
  },
) {
  const path = `#/incoming-groups/${encodeURIComponent(groupId)}/matching`;
  const params = new URLSearchParams();
  if (releaseId) params.set("release", releaseId);
  if (options?.localReleaseId) params.set("local", options.localReleaseId);
  if (options?.prepareReleaseId)
    params.set("prepare", options.prepareReleaseId);
  const query = params.toString();
  return `${path}${options?.creatingLocalRelease ? "/local-release" : ""}${query ? `?${query}` : ""}`;
}
