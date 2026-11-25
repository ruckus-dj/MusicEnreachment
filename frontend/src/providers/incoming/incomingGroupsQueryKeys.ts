import { getGetIncomingGroupsQueryKey } from "../../api/generated/client";

export function incomingGroupCountQueryKey() {
  return [...getGetIncomingGroupsQueryKey(), "count"] as const;
}
