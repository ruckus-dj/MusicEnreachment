import { useCallback, useEffect, useRef, useState } from "react";
import {
	type OriginalTrack,
	originalReleases,
	recoveryFixture,
} from "../features/library/catalogData";
import type { DeletionOperation } from "../components/DeletionRequest";
import type { OperationMessage } from "../features/operations/operationMessages";
import type { ManagedPublicationDeletionResult } from "../features/publication/managedPublicationDeletionModel";

/** Demo scheduling: admission is visible before the provider settles the work. */
export const demoOperationStartDelayMs = 400;
export const demoOperationSettleDelayMs = 800;

export type DemoOperationStatus = "queued" | "running" | "failed" | "succeeded";
export type DemoDeletionFailure = "retry" | "conflict" | "stale";

export interface DemoDeletionState {
	status: DemoOperationStatus;
	/** Present only after the provider settles a removal without success. */
	failure?: DemoDeletionFailure;
}

export type RecoveredSnapshot = NonNullable<OriginalTrack["published"]>;

export interface OperationsDemoInitialState {
	signals?: DemoOperationStatus;
	recovery?: DemoOperationStatus;
}

export interface OperationsDemoProviderOptions {
	deletions: DeletionOperation[];
	/** Applies the confirmed scope and reports the resulting operation state. */
	settleDeletion: (
		operation: DeletionOperation,
	) => ManagedPublicationDeletionResult;
	dismissDeletion?: (operation: DeletionOperation) => void;
	recover: (snapshot: RecoveredSnapshot) => boolean | undefined;
	initialState?: OperationsDemoInitialState;
}

export interface OperationsDemoProvider {
	signals: DemoOperationStatus;
	recovery: DemoOperationStatus;
	deletionStates: ReadonlyMap<DeletionOperation, DemoDeletionState>;
	notice: OperationMessage | "";
	refresh: () => void;
	isRefreshing: boolean;
	reset: () => void;
	retrySignals: () => void;
	dismissSignals: () => void;
	retryRecovery: () => void;
	dismissRecovery: () => void;
	retryDeletion: (operation: DeletionOperation) => void;
	dismissDeletion: (operation: DeletionOperation) => void;
}

function frozenRecoverySnapshot(): RecoveredSnapshot | null {
	const frozen = originalReleases
		.flatMap((release) => release.tracks)
		.find((track) => track.id === recoveryFixture.trackId)?.published;
	return frozen
		? { ...structuredClone(frozen), path: recoveryFixture.targetPath }
		: null;
}

/**
 * Demo operations lifecycle. It lives above the Operations screen so queued
 * removals keep settling while the operator is on another route; there is no
 * user-driven "complete removal" step. Simulated recovery happens on retry.
 */
export function useOperationsDemoProvider({
	deletions,
	settleDeletion,
	dismissDeletion: onDismissDeletion,
	recover,
	initialState,
}: OperationsDemoProviderOptions): OperationsDemoProvider {
	const [signals, setSignals] = useState<DemoOperationStatus>(
		initialState?.signals ?? "failed",
	);
	const [recovery, setRecovery] = useState<DemoOperationStatus>(
		initialState?.recovery ?? "failed",
	);
	const [deletionStates, setDeletionStates] = useState<
		Map<DeletionOperation, DemoDeletionState>
	>(() => new Map());
	const [notice, setNotice] = useState<OperationMessage | "">("");
	const [isRefreshing, setRefreshing] = useState(false);
	useEffect(() => {
		if (!isRefreshing) return;
		const timer = setTimeout(
			() => setRefreshing(false),
			demoOperationStartDelayMs,
		);
		return () => clearTimeout(timer);
	}, [isRefreshing]);
	const settleDeletionRef = useRef(settleDeletion);
	const dismissDeletionRef = useRef(onDismissDeletion);
	const recoverRef = useRef(recover);
	// Timer callbacks need the latest projection of the demo catalog state.
	settleDeletionRef.current = settleDeletion;
	dismissDeletionRef.current = onDismissDeletion;
	recoverRef.current = recover;

	useEffect(() => {
		setDeletionStates((previous) => {
			const present = new Set(deletions);
			let changed = false;
			const next = new Map(previous);
			for (const operation of deletions) {
				if (next.has(operation)) continue;
				next.set(operation, { status: "queued" });
				changed = true;
			}
			for (const operation of next.keys()) {
				if (present.has(operation)) continue;
				next.delete(operation);
				changed = true;
			}
			return changed ? next : previous;
		});
	}, [deletions]);

	useEffect(() => {
		if (signals !== "queued") return;
		const timer = setTimeout(
			() => setSignals("running"),
			demoOperationStartDelayMs,
		);
		return () => clearTimeout(timer);
	}, [signals]);

	useEffect(() => {
		if (signals !== "running") return;
		const timer = setTimeout(() => {
			setSignals("succeeded");
			setNotice("retrySucceeded");
		}, demoOperationSettleDelayMs);
		return () => clearTimeout(timer);
	}, [signals]);

	useEffect(() => {
		if (recovery !== "queued") return;
		const timer = setTimeout(
			() => setRecovery("running"),
			demoOperationStartDelayMs,
		);
		return () => clearTimeout(timer);
	}, [recovery]);

	useEffect(() => {
		if (recovery !== "running") return;
		const timer = setTimeout(() => {
			const snapshot = frozenRecoverySnapshot();
			// The provider performs the former result check on retry; the dialog
			// based on explicit fixture observations is no longer part of the UI.
			const accepted =
				snapshot !== null &&
				recoveryFixture.tagsMatch &&
				recoveryFixture.technicalMatch &&
				recoveryFixture.authorityCurrent &&
				recoverRef.current(snapshot) !== false;
			setRecovery(accepted ? "succeeded" : "failed");
			setNotice(accepted ? "recoveryRegistered" : "recoveryFailed");
		}, demoOperationSettleDelayMs);
		return () => clearTimeout(timer);
	}, [recovery]);

	useEffect(() => {
		// Apply one removal at a time against the latest catalog, never commit
		// two removals computed from the same demo snapshot.
		const active =
			[...deletionStates].find(([, state]) => state.status === "running") ??
			[...deletionStates].find(([, state]) => state.status === "queued");
		if (!active) return;
		const [operation, state] = active;
		let timer: ReturnType<typeof setTimeout>;
		if (state.status === "queued") {
			timer = setTimeout(() => {
				setDeletionStates((previous) => {
					const current = previous.get(operation);
					if (current?.status !== "queued") return previous;
					return new Map(previous).set(operation, { status: "running" });
				});
			}, demoOperationStartDelayMs);
		} else {
			timer = setTimeout(() => {
				const result = settleDeletionRef.current(operation);
				setDeletionStates((previous) => {
					const current = previous.get(operation);
					if (current?.status !== "running") return previous;
					return new Map(previous).set(
						operation,
						result === "completed"
							? { status: "succeeded" }
							: { status: "failed", failure: result },
					);
				});
				setNotice(
					result === "completed"
						? "removalSucceeded"
						: result === "stale"
							? "removalStale"
							: result === "conflict"
								? "removalConflict"
								: "removalRetry",
				);
			}, demoOperationSettleDelayMs);
		}
		return () => clearTimeout(timer);
	}, [deletionStates]);

	const retrySignals = useCallback(() => {
		setSignals((current) => (current === "failed" ? "queued" : current));
	}, []);

	const dismissSignals = useCallback(() => {
		setSignals("succeeded");
		setNotice("dismissed");
	}, []);

	const retryRecovery = useCallback(() => {
		setRecovery((current) => (current === "failed" ? "queued" : current));
	}, []);

	const dismissRecovery = useCallback(() => {
		setRecovery("succeeded");
		setNotice("dismissed");
	}, []);

	const retryDeletion = useCallback((operation: DeletionOperation) => {
		setDeletionStates((previous) => {
			const current = previous.get(operation);
			if (
				!current ||
				current.status !== "failed" ||
				current.failure === "stale"
			)
				return previous;
			return new Map(previous).set(operation, { status: "queued" });
		});
	}, []);

	const dismissDeletion = useCallback((operation: DeletionOperation) => {
		setDeletionStates((previous) => {
			if (!previous.has(operation)) return previous;
			return new Map(previous).set(operation, { status: "succeeded" });
		});
		dismissDeletionRef.current?.(operation);
		setNotice("dismissed");
	}, []);

	return {
		signals,
		recovery,
		deletionStates,
		notice,
		refresh: () => setRefreshing(true),
		isRefreshing,
		reset: () => {
			setSignals("succeeded");
			setRecovery("succeeded");
			setDeletionStates(new Map());
			setNotice("");
			setRefreshing(false);
		},
		retrySignals,
		dismissSignals,
		retryRecovery,
		dismissRecovery,
		retryDeletion,
		dismissDeletion,
	};
}
