import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { useEffect, useState } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SourceFiles } from "../../catalogData";
import { I18nProvider } from "../../i18n/I18nProvider";
import {
	initialSourceRoots,
	type SourceRoot,
	SourcesScreen,
} from "../../SourcesScreen";
import { useSourceScanDemoProvider } from "../../fixtures/useSourceScanDemoProvider";
import {
	initialSourceProcessingState,
	type SourceProcessingState,
} from "../../sourceProcessingModel";

const files: SourceFiles = {
	"track-1": {
		id: "track-1",
		name: "01 - First Light.flac",
		duration: 252,
		size: "24.3 MB",
		tags: {},
		available: true,
		path: "/mnt/music/01 - First Light.flac",
		codec: "flac",
		locations: [
			{
				id: "loc-1",
				rootId: "root-library",
				path: "/mnt/music/01 - First Light.flac",
			},
		],
	},
};

const storageKey = "music-design-v2-language";

function Harness({
	selectedRootId,
	onRoots,
	onProcessingState,
}: {
	selectedRootId?: string;
	onRoots: (roots: SourceRoot[]) => void;
	onProcessingState: (state: SourceProcessingState) => void;
}) {
	const [roots, setRoots] = useState(() => structuredClone(initialSourceRoots));
	const [processing, setProcessing] = useState(initialSourceProcessingState);
	const scans = useSourceScanDemoProvider({
		files,
		workDirectory: "/work",
		updateRoots: setRoots,
		updateProcessing: setProcessing,
	});
	useEffect(() => onRoots(roots), [onRoots, roots]);
	useEffect(
		() => onProcessingState(processing),
		[onProcessingState, processing],
	);
	return (
		<SourcesScreen
			roots={roots}
			onRoots={setRoots}
			files={files}
			onRemove={() => {}}
			selectedRootId={selectedRootId}
			workDirectory="/work"
			processing={processing}
			scans={scans}
			onProcessingState={setProcessing}
		/>
	);
}

const element = (
	selectedRootId: string | undefined,
	onRoots: (roots: SourceRoot[]) => void,
	onProcessingState: (state: SourceProcessingState) => void,
) => (
	<I18nProvider>
		<Harness
			selectedRootId={selectedRootId}
			onRoots={onRoots}
			onProcessingState={onProcessingState}
		/>
	</I18nProvider>
);

beforeEach(() => {
	vi.useFakeTimers();
	localStorage.setItem(storageKey, "en");
	window.location.hash = "";
});
afterEach(() => vi.useRealTimers());

it("registers without scanning, opens its route, and scans only on explicit start", () => {
	const onRoots = vi.fn<(roots: SourceRoot[]) => void>();
	const onProcessingState = vi.fn<(state: SourceProcessingState) => void>();
	const lastRoots = () => onRoots.mock.calls.at(-1)?.[0] ?? [];
	const lastProcessing = () => onProcessingState.mock.calls.at(-1)?.[0];
	const view = render(element(undefined, onRoots, onProcessingState));

	// Directories in the list link to their own route; the list itself never
	// queues scan work.
	const libraryLink = within(
		screen.getByRole("grid", { name: "Connected directories" }),
	).getByRole("link", { name: /Музыкальная библиотека/ });
	expect(libraryLink).toHaveAttribute("href", "#sources/root-library");
	expect(lastProcessing()?.files).toEqual([]);

	// Registration: create a source and submit.
	fireEvent.click(screen.getByRole("button", { name: "Add source" }));
	const form = screen
		.getByRole("heading", { name: "New source" })
		.closest("section");
	if (!form) throw new Error("Missing registration form");
	fireEvent.change(within(form).getByLabelText("Source name"), {
		target: { value: "Новый источник" },
	});
	fireEvent.click(within(form).getByRole("radio", { name: "In place" }));
	fireEvent.click(within(form).getByRole("button", { name: "Add source" }));

	const added = lastRoots().find(
		(root) => !initialSourceRoots.some((item) => item.id === root.id),
	);
	if (!added) throw new Error("Registration did not add a source root");
	// Opening the new root is the whole result of registration.
	expect(window.location.hash).toBe(`#sources/${encodeURIComponent(added.id)}`);
	expect(added.available).toBeNull();
	expect(added.lastSuccessfulScanAt).toBeUndefined();
	expect(added.inventoryCount).toBeUndefined();
	expect(
		screen.getByText(/No server directory was opened or scanned/),
	).toBeInTheDocument();
	expect(screen.queryByText("Scan queued.")).not.toBeInTheDocument();
	expect(lastProcessing()?.files).toEqual([]);
	const rootsCallsAfterRegistration = onRoots.mock.calls.length;

	// The opened route shows a never-scanned source with an empty queue. An
	// unchecked (unknown) source is scannable, but nothing has started yet: the
	// route alone neither changes the scan state nor queues processing.
	view.rerender(element(added.id, onRoots, onProcessingState));
	expect(
		screen.getByRole("heading", { name: "Новый источник" }),
	).toBeInTheDocument();
	expect(screen.getAllByText("Not scanned yet").length).toBeGreaterThan(0);
	expect(
		screen.getByText("No inventory records have been queued for processing."),
	).toBeInTheDocument();
	expect(screen.queryByText("Scan queued.")).not.toBeInTheDocument();
	const scanStart = screen.getByRole("button", { name: "Scan directory" });
	expect(scanStart).toBeEnabled();
	expect(onRoots.mock.calls.length).toBe(rootsCallsAfterRegistration);
	expect(lastProcessing()?.files).toEqual([]);

	// Explicit start on the newly registered unknown source: queued, then the
	// staged fixture stages. Scan state and inventory stay untouched until the
	// staged scan succeeds.
	fireEvent.click(scanStart);
	expect(screen.getByText("Scan queued.")).toBeInTheDocument();
	expect(scanStart).toBeDisabled();
	expect(onRoots.mock.calls.length).toBe(rootsCallsAfterRegistration);
	expect(
		lastRoots().find((root) => root.id === added.id)?.lastSuccessfulScanAt,
	).toBeUndefined();

	act(() => vi.advanceTimersByTime(350));
	expect(screen.getByText("Reading the directory…")).toBeInTheDocument();
	expect(onRoots.mock.calls.length).toBe(rootsCallsAfterRegistration);

	act(() => vi.advanceTimersByTime(600));
	expect(screen.getByText("Updating inventory…")).toBeInTheDocument();
	expect(onRoots.mock.calls.length).toBe(rootsCallsAfterRegistration);

	act(() => vi.advanceTimersByTime(550));
	expect(screen.getByText("Scan completed.")).toBeInTheDocument();
	expect(onRoots.mock.calls.length).toBe(rootsCallsAfterRegistration + 1);
	const scannedNewRoot = lastRoots().find((root) => root.id === added.id);
	expect(scannedNewRoot?.available).toBe(true);
	expect(scannedNewRoot?.lastSuccessfulScanAt).toBeTruthy();
	expect(scannedNewRoot?.inventoryCount).toBe(0);
	expect(lastProcessing()?.files).toEqual([]);

	// Explicit start on a source that has fixture inventory records: only the
	// completed scan records it and queues exactly its own processing work.
	view.rerender(element("root-library", onRoots, onProcessingState));
	expect(screen.getAllByText("Not scanned yet").length).toBeGreaterThan(0);
	expect(lastProcessing()?.files).toEqual([]);
	const rootsCallsBeforeLibraryScan = onRoots.mock.calls.length;
	fireEvent.click(screen.getByRole("button", { name: "Scan directory" }));
	expect(screen.getByText("Scan queued.")).toBeInTheDocument();
	expect(lastProcessing()?.files).toEqual([]);
	expect(onRoots.mock.calls.length).toBe(rootsCallsBeforeLibraryScan);

	act(() => vi.advanceTimersByTime(1500));
	expect(screen.getByText("Scan completed.")).toBeInTheDocument();
	const scannedRoot = lastRoots().find((root) => root.id === "root-library");
	expect(scannedRoot?.lastSuccessfulScanAt).toBeTruthy();
	expect(scannedRoot?.inventoryCount).toBe(1);
	expect(screen.getByText("Inventory records: 1")).toBeInTheDocument();
	// The removed scan-started notice is not fabricated by the adapter.
	expect(screen.queryByText(/processing started for/)).not.toBeInTheDocument();
	const processing = lastProcessing();
	if (!processing) throw new Error("Missing processing state");
	expect(processing.files.map((file) => file.id)).toEqual([
		"root-library:loc-1",
	]);
	expect(processing.copies).toEqual([]);
	expect(
		screen.getByText("/mnt/music/01 - First Light.flac"),
	).toBeInTheDocument();
});
