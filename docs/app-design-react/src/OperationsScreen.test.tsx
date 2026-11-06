import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import { originalReleases } from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";
import { I18nProvider } from "./i18n";
import { OperationsScreen } from "./OperationsScreen";

beforeEach(() => {
	window.localStorage.setItem("music-design-v2-language", "en");
});

it("keeps incomplete and conflicting deletion requests retryable until completion", () => {
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
	const complete = vi
		.fn()
		.mockReturnValueOnce("retry")
		.mockReturnValueOnce("conflict")
		.mockReturnValueOnce("completed");
	render(
		<I18nProvider>
			<OperationsScreen
				releases={structuredClone(originalReleases)}
				deletions={[operation]}
				onRecover={() => false}
				onCompleteDeletion={complete}
				initialState={{
					retryState: "succeeded",
					dismissedOperations: new Set(["recovery"]),
				}}
			/>
		</I18nProvider>,
	);
	fireEvent.click(screen.getByRole("button", { name: "Complete removal" }));
	expect(screen.getByText("Removal is still pending")).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Retry removal" }));
	expect(screen.getByText("Path conflict · review required")).toBeVisible();
	expect(screen.getByText(/It was not touched/)).toBeVisible();
	fireEvent.click(screen.getByRole("button", { name: "Retry removal" }));
	expect(screen.queryByRole("button", { name: "Retry removal" })).toBeNull();
	expect(
		screen.getByText("The confirmed removal scope was completed."),
	).toBeVisible();
	expect(complete).toHaveBeenCalledTimes(3);
});
