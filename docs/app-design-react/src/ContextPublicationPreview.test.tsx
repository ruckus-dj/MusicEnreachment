import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
	originalProviders,
	originalReleases,
	publicationMetadataIssueMessages,
} from "./catalogData";
import type { IncomingPlan } from "./incomingModel";
import { ContextPublicationPreview } from "./ContextPublicationPreview";
import { formatPublicationMessage } from "./publicationMessages";

it("distinguishes unprocessable selected positions from positions skipped by choice", () => {
	const release = structuredClone(
		originalReleases.find((item) => item.id === "north"),
	);
	if (!release) throw new Error("Missing north release fixture");
	const targetTrack = structuredClone(
		release.tracks.find((item) => item.id === "north-t9"),
	);
	if (!targetTrack) throw new Error("Missing north-t9 fixture");
	const assignment = {
		sourceIds: ["unavailable-source"],
		selectedSourceId: "unavailable-source",
		sourceMeta: {},
		method: "manual",
		confidence: null,
		providerTrackId: null,
		included: true,
	};
	const plan: IncomingPlan = {
		mode: "local",
		groupId: "test",
		target: { ...release, tracks: [targetTrack] },
		localReleaseId: release.id,
		assignments: { [targetTrack.id]: assignment },
		inventoryVersion: 4,
	};
	const props = {
		plan,
		files: {},
		releases: [release],
		providers: originalProviders,
		artists: [],
		inventoryVersion: 4,
		confirmDowngrade: false,
		onPlan: vi.fn(),
		onReleases: vi.fn(),
		onSuccess: vi.fn(),
		onBack: vi.fn(),
	};
	const { rerender } = render(<ContextPublicationPreview {...props} />);
	expect(screen.getByText("Пропустить: 1")).toBeInTheDocument();
	expect(
		screen.getByText("Выбранный файл недоступен для этой операции"),
	).toBeInTheDocument();

	const localTrack = release.tracks.find((item) => item.id === targetTrack.id);
	if (!localTrack) throw new Error("Missing local position fixture");
	localTrack.recordingMetadata = { explicitstatus: ["maybe"] };
	rerender(
		<ContextPublicationPreview
			{...props}
			plan={{
				...plan,
				target: {
					...plan.target,
					tracks: [
						{
							...targetTrack,
							recordingMetadata: { explicitstatus: ["maybe"] },
						},
					],
				},
			}}
		/>,
	);
	expect(screen.getByText("Невозможно обработать: 1")).toBeInTheDocument();
	fireEvent.click(
		within(
			screen.getByRole("region", { name: "Сравнить метаданные" }),
		).getByRole("button", { name: /9\. Winter Birds/ }),
	);
	expect(
		screen.getByText(
			formatPublicationMessage("ru", "invalidMetadata", {
				fields: publicationMetadataIssueMessages(
					release,
					localTrack,
					"ru",
				).join("; "),
			}),
		),
	).toBeInTheDocument();

	rerender(
		<ContextPublicationPreview
			{...props}
			plan={{
				...plan,
				target: {
					...plan.target,
					tracks: [
						{
							...targetTrack,
							recordingMetadata: { explicitstatus: ["maybe"] },
						},
					],
				},
				assignments: {
					[targetTrack.id]: { ...assignment, included: false },
				},
			}}
		/>,
	);
	expect(screen.getByText("Пропустить: 1")).toBeInTheDocument();
});
