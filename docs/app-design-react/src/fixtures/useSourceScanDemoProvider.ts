import { useEffect, useRef, useState } from "react";
import type { SourceFiles } from "../features/library/catalogData";
import type { SourceScanState } from "../features/sources/SourceScanPanel";
import { scanScopeLocations } from "../features/sources/sourceInventory";
import {
	blockStagedSourceQueue,
	createSourceScan,
	processSourceQueue,
	type SourceProcessingState,
} from "../features/sources/sourceProcessingModel";
import type { SourceRoot } from "../features/sources/SourcesScreen";

type Update<T> = (update: (current: T) => T) => void;

export function useSourceScanDemoProvider({
	files,
	workDirectory,
	updateRoots,
	updateProcessing,
}: {
	files: SourceFiles;
	workDirectory: string;
	updateRoots: Update<SourceRoot[]>;
	updateProcessing: Update<SourceProcessingState>;
}) {
	type DemoScanState =
		| Exclude<SourceScanState, { message: string }>
		| { kind: "fixtureFailure" };
	const [states, setStates] = useState<Record<string, DemoScanState>>({});
	const attempts = useRef(new Set<string>());
	const active = useRef(new Set<string>());
	const timers = useRef(new Set<number>());
	useEffect(() => {
		const pending = timers.current;
		const running = active.current;
		return () => {
			for (const timer of pending) window.clearTimeout(timer);
			pending.clear();
			running.clear();
		};
	}, []);
	useEffect(() => {
		if (!workDirectory) return;
		updateProcessing((current) =>
			current.files.some(
				(file) => file.blockedReason === "work_directory_required",
			)
				? processSourceQueue(current)
				: current,
		);
	}, [workDirectory, updateProcessing]);

	function start(root: SourceRoot) {
		if (root.available === false || active.current.has(root.id)) return;
		const fail = root.id === "root-backup" && !attempts.current.has(root.id);
		attempts.current.add(root.id);
		active.current.add(root.id);
		const report = (state: DemoScanState) =>
			setStates((current) => ({ ...current, [root.id]: state }));
		const schedule = (delay: number, callback: () => void) => {
			const timer = window.setTimeout(() => {
				timers.current.delete(timer);
				callback();
			}, delay);
			timers.current.add(timer);
		};
		report({ kind: "queued" });
		schedule(350, () => report({ kind: "traversing" }));
		schedule(950, () => report({ kind: "applying" }));
		schedule(1500, () => {
			active.current.delete(root.id);
			if (fail) {
				report({ kind: "fixtureFailure" });
				return;
			}
			const at = new Date().toISOString();
			const admitted = scanScopeLocations(files, root);
			const admittedLocationIds = new Set(
				admitted.map((location) => location.id),
			);
			const count = admitted.length;
			updateRoots((current) =>
				current.map((item) =>
					item.id === root.id
						? {
								...item,
								available: true,
								inventoryCount: count,
								inventoryPath: root.path,
								lastSuccessfulScanAt: at,
							}
						: item,
				),
			);
			updateProcessing((current) => {
				const scanned = createSourceScan(
					current,
					root.id,
					root.mode,
					files,
					admittedLocationIds,
				);
				return root.mode === "staged" && !workDirectory
					? blockStagedSourceQueue(scanned, root.id)
					: processSourceQueue(
							scanned,
							root.id,
							undefined,
							new Set(admitted.map((location) => `${root.id}:${location.id}`)),
						);
			});
			report({ kind: "succeeded" });
		});
	}

	return { states, start, active: (id: string) => active.current.has(id) };
}

export type SourceScanDemoProvider = ReturnType<
	typeof useSourceScanDemoProvider
>;
