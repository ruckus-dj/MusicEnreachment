import { libraryReleaseHref } from "../../../ui/features/library/libraryDetailRoute";

/** Exact local release destination; the identity is the local release ID. */
export function releaseHref(releaseId: string) {
  return libraryReleaseHref(releaseId);
}
