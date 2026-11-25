import { useEffect, useRef, useState } from "react";
import type { Locale } from "../../../app/i18n";
import type {
	SourceArtifactCleanupCandidate,
	SourceArtifactCleanupResult,
	SourceArtifactCleanupViewProps,
} from "../../../ui/features/sources/SourceArtifactCleanupView";
import {
	type SourceMessageKey,
	sourceMessage,
} from "../../../ui/features/sources/sourceInventoryMessages";
import type { SourceFiles } from "../library/catalogData";
import { retainedCopyLabel } from "./retainedCopyPath";
import type { SourceRoot } from "./SourcesScreen";
import {
	eligibleSourceCopies,
	type SourceProcessingState,
} from "./sourceProcessingModel";

type DemoOperation = NonNullable<SourceArtifactCleanupViewProps["operation"]>;

const candidateLimit = 1000;

export function useSourceArtifactCleanupDemoProvider({
	locale,
	roots,
	files,
	processing,
	updateProcessing,
	failFirstAttempt = false,
}: {
	locale: Locale;
	roots: SourceRoot[];
	files: SourceFiles;
	processing: SourceProcessingState;
	updateProcessing: (state: SourceProcessingState) => void;
	/** Fixture-only failure injection; normal demo actions never fail. */
	failFirstAttempt?: boolean;
}): SourceArtifactCleanupViewProps {
	const [selected, setSelected] = useState<string[]>([]);
	const [submitted, setSubmitted] = useState<string[]>([]);
	const [operation, setOperation] = useState<DemoOperation>();
	const [results, setResults] = useState<SourceArtifactCleanupResult[]>();
	const [error, setError] = useState<{ text: string }>();
	const [failedCopies, setFailedCopies] = useState<string[]>([]);
	const pending = useRef(false);
	const attempted = useRef(false);
	const timers = useRef<Set<number>>(new Set());
	const processingRef = useRef(processing);
	processingRef.current = processing;
	const localeRef = useRef(locale);
	localeRef.current = locale;

	useEffect(() => {
		const scheduled = timers.current;
		return () => {
			for (const timer of scheduled) window.clearTimeout(timer);
			scheduled.clear();
		};
	}, []);

	const schedule = (delay: number, callback: () => void) => {
		const timer = window.setTimeout(() => {
			timers.current.delete(timer);
			callback();
		}, delay);
		timers.current.add(timer);
	};
	const message = (key: SourceMessageKey) =>
		sourceMessage(localeRef.current, key);

	const candidates: SourceArtifactCleanupCandidate[] = eligibleSourceCopies(
		processing,
	)
		.slice(0, candidateLimit)
		.map((copy) => ({
			artifact_id: copy.id,
			source_relative_path:
				retainedCopyLabel(copy, processing, roots, files) ??
				message("copyPathUnavailable"),
			state: failedCopies.includes(copy.id)
				? "cleanup_failed"
				: "cleanup_eligible",
		}));
	const batchCount = candidates.length;
	const isPending =
		operation?.state === "queued" || operation?.state === "running";

	function load() {
		if (pending.current) return;
		setError(undefined);
		setFailedCopies((current) =>
			current.filter((id) =>
				processingRef.current.copies.some((copy) => copy.id === id),
			),
		);
	}

	function toggle(id: string) {
		if (pending.current) return;
		setSelected((current) =>
			current.includes(id)
				? current.filter((selectedId) => selectedId !== id)
				: [...current, id],
		);
	}

	function start(ids: readonly string[]) {
		if (!ids.length || ids.length > candidateLimit || pending.current) return;
		const frozen = [...ids];
		const currentIds = new Set(
			eligibleSourceCopies(processingRef.current).map((copy) => copy.id),
		);
		if (
			new Set(frozen).size !== frozen.length ||
			frozen.some((id) => !currentIds.has(id))
		) {
			setError({ text: message("cleanupSelectionStale") });
			return;
		}

		pending.current = true;
		const fail = failFirstAttempt && !attempted.current;
		attempted.current = true;
		setError(undefined);
		setSubmitted(frozen);
		setResults(undefined);
		setOperation({ state: "queued", stage: "queued" });

		schedule(200, () => setOperation({ state: "running", stage: "removing" }));
		schedule(600, () => {
			const current = processingRef.current;
			const frozenEligible = new Set(
				eligibleSourceCopies(current).map((copy) => copy.id),
			);
			// A later claim can fail without undoing the earlier confirmed items.
			const firstBlocked = frozen.findIndex((id) => !frozenEligible.has(id));
			const claimed = firstBlocked < 0 ? frozen : frozen.slice(0, firstBlocked);
			setSelected([]);

			if (fail) {
				const safeError = message("cleanupCopyFailed");
				setFailedCopies((existing) => [...new Set([...existing, ...claimed])]);
				setResults(
					claimed.map((id) => ({
						artifact_id: id,
						state: "failed",
						safe_error: safeError,
					})),
				);
				setOperation({
					state: "failed",
					stage: "finished",
					safe_error: message("cleanupBatchFailed"),
				});
				pending.current = false;
				return;
			}

			const removed = new Set(claimed);
			updateProcessing({
				...current,
				files: current.files.map((file) =>
					file.copyId && removed.has(file.copyId)
						? { ...file, copyId: undefined }
						: file,
				),
				copies: current.copies.filter((copy) => !removed.has(copy.id)),
			});
			setFailedCopies((existing) => existing.filter((id) => !removed.has(id)));
			setResults(
				claimed.map((id) => ({ artifact_id: id, state: "succeeded" })),
			);
			setOperation(
				firstBlocked < 0
					? { state: "succeeded", stage: "finished" }
					: {
							state: "failed",
							stage: "finished",
							safe_error: message("cleanupSelectionStale"),
						},
			);
			pending.current = false;
		});
	}

	return {
		locale,
		candidates,
		batchCount,
		selected,
		submitted,
		operation,
		results,
		loading: false,
		busy: false,
		isPending,
		error,
		streamError: "",
		onLoad: load,
		onToggle: toggle,
		onStart: start,
	};
}
