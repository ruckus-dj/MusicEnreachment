import type { DeletionOperation } from "../../DeletionRequest";
import type {
	DemoOperationStatus,
	OperationsDemoProvider,
} from "../../fixtures/useOperationsDemoProvider";
import { useI18n } from "../../i18n";
import { OperationsList, type OperationsListRow } from "./OperationsList";
import { operationMessages } from "./operationMessages";

export function OperationsScreen({
	deletions,
	provider,
}: {
	deletions: DeletionOperation[];
	provider: OperationsDemoProvider;
}) {
	const { locale } = useI18n();
	const copy = operationMessages[locale];
	const rows: OperationsListRow[] = [];

	function addRow(
		status: DemoOperationStatus,
		row: Omit<OperationsListRow, "state" | "stateTone">,
	) {
		if (status === "succeeded") return;
		rows.push({
			...row,
			error: status === "failed" ? row.error : undefined,
			actions: status === "failed" ? row.actions : undefined,
			state: copy[status],
			stateTone: status === "failed" ? "error" : "info",
		});
	}

	addRow(provider.signals, {
		id: "signals",
		target: copy.signalsTrack,
		operation: copy.publishPositions,
		error: `${copy.writeAccessFailure} ${copy.prepareAgain}`,
		actions: [
			{ id: "retry", label: copy.retry, onPress: provider.retrySignals },
		],
	});
	addRow(provider.recovery, {
		id: "recovery",
		target: copy.recoveryTrack,
		operation: copy.publishPositions,
		error: copy.knownTargetMissingResult,
		actions: [
			{ id: "retry", label: copy.retry, onPress: provider.retryRecovery },
		],
	});

	for (const [index, operation] of deletions.entries()) {
		const state = provider.deletionStates.get(operation);
		const failure = state?.failure;
		addRow(state?.status ?? "queued", {
			id: `removal-${index}`,
			target: operation.target || copy.unknownTarget,
			targetDetail: operation.scope,
			operation:
				operation.kind === "publication"
					? copy.removePublications
					: operation.kind === "artist"
						? copy.removeArtist
						: copy.removeLibrary,
			error:
				failure === "stale"
					? copy.removalStale
					: failure === "conflict"
						? copy.removalConflict
						: copy.removalRetry,
			stage:
				failure === "stale"
					? copy.staleScope
					: failure === "conflict"
						? copy.removalStateConflict
						: failure === "retry"
							? copy.removalStateRetry
							: undefined,
			actions:
				failure === "stale"
					? [{ id: "review", label: copy.returnAndConfirm, href: "#library" }]
					: [
							{
								id: "retry",
								label: copy.retry,
								onPress: () => provider.retryDeletion(operation),
							},
						],
		});
	}

	return (
		<OperationsList
			copy={copy}
			rows={rows}
			notice={provider.notice ? copy[provider.notice] : undefined}
			onRefresh={provider.refresh}
			isRefreshing={provider.isRefreshing}
		/>
	);
}
