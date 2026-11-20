export type ManagedPathObservation =
	| "present"
	| "absent"
	| "replaced"
	| "unknown";
/** Operation-level states; `stale` comes only from confirmed-scope rejection. */
export type ManagedPublicationDeletionResult =
	| "completed"
	| "stale"
	| "retry"
	| "conflict";

/**
 * Interprets explicit demo observations; it performs no filesystem operation.
 * An absent managed target may still complete metadata cleanup after a prior
 * unlink/failed commit, while a replacement at the same path is never touched.
 * An unknown observation (for example an unavailable root) is not proof of
 * absence and is always retried.
 */
export function resolveManagedPublicationDeletion(
	path: ManagedPathObservation,
	deleteSucceeded: boolean,
	metadataCommitSucceeded: boolean,
): Exclude<ManagedPublicationDeletionResult, "stale"> {
	if (path === "unknown") return "retry";
	if (path === "replaced") return "conflict";
	if (path === "present" && !deleteSucceeded) return "retry";
	return metadataCommitSucceeded ? "completed" : "retry";
}
