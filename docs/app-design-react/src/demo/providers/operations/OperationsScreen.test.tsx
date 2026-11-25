import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../../app/i18n";
import type { DeletionOperation } from "../../components/DeletionRequest";
import { queuedOperationFixtures } from "../../fixtures/operationsQueueFixtures";
import { originalReleases } from "../library/catalogData";
import { OperationsScreen } from "./OperationsScreen";
import {
	demoOperationSettleDelayMs,
	demoOperationStartDelayMs,
	type OperationsDemoProviderOptions,
	useOperationsDemoProvider,
} from "./useOperationsDemoProvider";

beforeEach(() => {
	window.localStorage.setItem("music-design-v2-language", "en");
	vi.useFakeTimers();
});

afterEach(() => {
	vi.useRealTimers();
});

function OperationsHarness(props: OperationsDemoProviderOptions) {
	const provider = useOperationsDemoProvider(props);
	return <OperationsScreen deletions={props.deletions} provider={provider} />;
}

function advance(delay: number) {
	act(() => {
		vi.advanceTimersByTime(delay);
	});
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
	fireEvent.click(screen.getByRole("button", { name: /kind/i }));
	fireEvent.click(screen.getByRole("option", { name: "Removal" }));
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
	const grid = screen.getByRole("grid");
	const created = within(grid).getByRole("columnheader", { name: "Created" });
	expect(created).toHaveAttribute("aria-sort", "descending");
	fireEvent.click(screen.getByRole("button", { name: "Next" }));
	expect(screen.getByText("51–100 of 2,400")).toBeVisible();
	fireEvent.click(created);
	expect(created).toHaveAttribute("aria-sort", "ascending");
	expect(screen.getByText("1–50 of 2,400")).toBeVisible();
	const oldest = queuedOperationFixtures.at(-1);
	if (!oldest) throw new Error("Missing queue fixture");
	const first = within(grid).getAllByRole("row")[1];
	expect(first).toHaveTextContent(oldest.target);
	expect(first).toHaveTextContent(
		new Intl.DateTimeFormat("en", {
			dateStyle: "short",
			timeStyle: "short",
		}).format(oldest.createdAt),
	);
});
