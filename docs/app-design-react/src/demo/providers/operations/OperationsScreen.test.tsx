import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../demoPreferences";
import { ApplicationRouteOutlet } from "../../../ui/app/ApplicationRouteOutlet";
import { libraryPositionHref } from "../../../ui/features/library/libraryDetailRoute";
import { queuedOperationFixtures } from "../../fixtures/operationsQueueFixtures";
import { originalReleases } from "../library/catalogData";
import type { DeletionOperation } from "../library/DeletionRequest";
import { DemoOperationsBinder } from "./OperationsScreen";
import {
	demoOperationSettleDelayMs,
	demoOperationStartDelayMs,
	type OperationsDemoProviderOptions,
	useOperationsDemoProvider,
} from "./useOperationsDemoProvider";

beforeEach(() => {
	window.localStorage.setItem("music-design-v2-language", "en");
	window.location.hash = "#/operations";
	vi.useFakeTimers();
});

afterEach(() => {
	vi.useRealTimers();
	window.location.hash = "";
});

function fixturePositionHref(positionId: string) {
	const release = originalReleases.find((item) =>
		item.tracks.some((track) => track.id === positionId),
	);
	return release ? libraryPositionHref(release.id, positionId) : undefined;
}

function OperationsHarness({
	queue,
	positionHrefForTrack = fixturePositionHref,
	...props
}: OperationsDemoProviderOptions & {
	queue?: typeof queuedOperationFixtures;
	positionHrefForTrack?: (positionId: string) => string | undefined;
}) {
	const provider = useOperationsDemoProvider(props);
	return (
		<ApplicationRouteOutlet
			route="operations"
			provider={DemoOperationsBinder}
			providerProps={{
				deletions: props.deletions,
				positionHrefForTrack,
				provider,
				queue,
			}}
		/>
	);
}

function advance(delay: number) {
	act(() => {
		vi.advanceTimersByTime(delay);
	});
}

function syncLocation() {
	act(() => window.dispatchEvent(new HashChangeEvent("hashchange")));
}

it("settles removal automatically and keeps failed attempts retryable until completion", () => {
	const track = originalReleases
		.flatMap((release) => release.tracks)
		.find((item) => item.published);
	if (!track?.published) throw new Error("Missing publication fixture");
	const operation: DeletionOperation = {
		kind: "publication",
		target: track.title,
		scope: "confirmed publication",
		publications: 1,
		paths: [track.published.path],
		libraryTrackIds: [track.id],
	};
	const settleDeletion = vi
		.fn()
		.mockReturnValueOnce("retry")
		.mockReturnValueOnce("conflict")
		.mockReturnValueOnce("completed");
	const dismissDeletion = vi.fn();
	render(
		<I18nProvider>
			<OperationsHarness
				deletions={[operation]}
				queue={[]}
				settleDeletion={settleDeletion}
				dismissDeletion={dismissDeletion}
				recover={() => false}
				initialState={{ signals: "succeeded", recovery: "succeeded" }}
			/>
		</I18nProvider>,
	);

	expect(screen.queryByText(track.title)).toBeNull();
	fireEvent.click(screen.getByRole("button", { name: /status/i }));
	fireEvent.click(screen.getByRole("option", { name: /^All$/ }));
	syncLocation();
	fireEvent.click(screen.getByRole("button", { name: /kind/i }));
	fireEvent.click(screen.getByRole("option", { name: "Removal" }));
	syncLocation();
	expect(screen.getByText(track.title)).toBeVisible();
	expect(screen.queryByRole("button", { name: "Complete removal" })).toBeNull();

	advance(demoOperationStartDelayMs);
	expect(within(screen.getByRole("grid")).getByText("Running")).toBeVisible();
	expect(screen.queryByRole("button", { name: "Complete removal" })).toBeNull();
	expect(settleDeletion).not.toHaveBeenCalled();

	advance(demoOperationSettleDelayMs);
	expect(settleDeletion).toHaveBeenCalledTimes(1);
	expect(settleDeletion).toHaveBeenNthCalledWith(1, operation);
	const removalError =
		"Removal is incomplete. Retry to verify the managed path and finish removal; an unavailable path is not treated as an absent file.";
	expect(screen.getByRole("status")).toHaveTextContent(removalError);
	const grid = screen.getByRole("grid");
	const failedRow = within(grid)
		.getByRole("link", { name: track.title })
		.closest("tr");
	if (!failedRow) throw new Error("Missing failed operation row");
	expect(within(failedRow).getByText("Removal is still pending")).toBeVisible();
	expect(within(failedRow).queryByText(removalError)).toBeNull();

	fireEvent.click(screen.getByRole("button", { name: "Retry" }));
	expect(screen.getByText(track.title)).toBeVisible();
	expect(screen.queryByRole("button", { name: "Complete removal" })).toBeNull();
	advance(demoOperationStartDelayMs);
	expect(within(screen.getByRole("grid")).getByText("Running")).toBeVisible();
	advance(demoOperationSettleDelayMs);
	expect(settleDeletion).toHaveBeenCalledTimes(2);
	expect(settleDeletion).toHaveBeenNthCalledWith(2, operation);
	expect(screen.getByText("Path conflict · review required")).toBeVisible();
	expect(screen.getByRole("status")).toHaveTextContent(
		"The file changed or the managed path is occupied by another file or publication. It was not touched; review the publication before retrying.",
	);

	fireEvent.click(screen.getByRole("button", { name: "Retry" }));
	advance(demoOperationStartDelayMs);
	advance(demoOperationSettleDelayMs);
	expect(settleDeletion).toHaveBeenCalledTimes(3);
	expect(settleDeletion).toHaveBeenNthCalledWith(3, operation);
	expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
	expect(
		screen.getByText("The confirmed removal scope was completed."),
	).toBeVisible();
	expect(dismissDeletion).not.toHaveBeenCalled();
});

it("links a runtime publication target by its own release and position", () => {
	render(
		<I18nProvider>
			<OperationsHarness
				deletions={[]}
				publicationJobs={[
					{
						id: "runtime-job",
						releaseId: "runtime-release",
						positionId: "runtime-position",
						title: "Runtime target",
						createdAt: 0,
					},
				]}
				queue={[]}
				settleDeletion={() => "completed"}
				recover={() => false}
				initialState={{ signals: "succeeded", recovery: "succeeded" }}
			/>
		</I18nProvider>,
	);
	fireEvent.click(screen.getByRole("button", { name: /status/i }));
	fireEvent.click(screen.getByRole("option", { name: "Queued" }));
	syncLocation();
	expect(screen.getByRole("link", { name: "Runtime target" })).toHaveAttribute(
		"href",
		"#/library/releases/runtime-release/positions/runtime-position",
	);
});

it("sorts the whole filtered queue before pagination and returns to page one", () => {
	render(
		<I18nProvider>
			<OperationsHarness
				deletions={[]}
				settleDeletion={() => "completed"}
				dismissDeletion={vi.fn()}
				recover={() => false}
				initialState={{ signals: "succeeded", recovery: "succeeded" }}
			/>
		</I18nProvider>,
	);
	fireEvent.click(screen.getByRole("button", { name: /status/i }));
	fireEvent.click(screen.getByRole("option", { name: "Queued" }));
	syncLocation();
	const grid = screen.getByRole("grid");
	const created = within(grid).getByRole("columnheader", { name: "Created" });
	expect(created).toHaveAttribute("aria-sort", "descending");
	fireEvent.click(screen.getByRole("button", { name: "Next" }));
	syncLocation();
	expect(window.location.hash).toBe("#/operations?status=queued&offset=50");
	expect(screen.getByText("51–100 of 2,400")).toBeVisible();
	fireEvent.click(created);
	syncLocation();
	expect(created).toHaveAttribute("aria-sort", "ascending");
	expect(screen.getByText("1–50 of 2,400")).toBeVisible();
	const oldest = queuedOperationFixtures.at(-1);
	if (!oldest) throw new Error("Missing queue fixture");
	const first = within(grid).getAllByRole("row")[1];
	expect(first).toHaveTextContent(oldest.target);
	expect(first).toHaveTextContent(
		new Date(oldest.createdAt).toLocaleString("en"),
	);
});
