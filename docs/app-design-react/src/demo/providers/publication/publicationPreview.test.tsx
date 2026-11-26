import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../demoPreferences";
import { firstPublicationPreviewMessages } from "../../../ui/features/library/firstPublicationPreviewMessages";
import { ContextPublicationPreviewOutlet } from "../../../ui/features/publication/ContextPublicationPreview";
import { publicationMessage } from "../../../ui/features/publication/publicationMessages";
import {
	createIncomingState,
	makeReleasePlan,
	providerReleases,
} from "../incoming/incomingModel";
import {
	effectivePublicationTags,
	originalArtists,
	originalFiles,
	originalReleases,
} from "../library/catalogData";
import {
	ContextPublicationPreview,
	type ContextPublicationPreviewProps,
	DemoPositionPublication,
	type DemoPositionPublicationBinderProps,
} from "./ContextPublicationPreview";
import {
	commitContextualPublication,
	type ContextualPublicationPreparation,
	settleContextualPublication,
} from "./contextualPublicationJob";
import {
	fixturePublicationDestination,
	type PublicationDestination,
} from "./publicationOutput";

afterEach(() => {
	cleanup();
	localStorage.clear();
});

const release = originalReleases.find((item) => item.id === "north")!;
const publishedTrack = release.tracks.find((track) => track.published)!;

const settleFixture = (
	releases: typeof originalReleases,
	destination: PublicationDestination,
	prepared: ContextualPublicationPreparation,
) =>
	settleContextualPublication(
		{
			releases,
			destination,
			files: originalFiles,
			providers: providerReleases,
			artists: originalArtists,
			inventoryVersion: createIncomingState().inventoryVersion,
		},
		prepared,
	);

it("localizes the publication reason before adding artist warnings", () => {
	localStorage.setItem("music-design-v2-language", "en");
	const state = createIncomingState();
	const plan = makeReleasePlan(
		state,
		release,
		originalFiles,
		publishedTrack.id,
	);
	plan.force = true;
	const { container } = render(
		<I18nProvider>
			<ContextPublicationPreviewOutlet<
				Omit<ContextPublicationPreviewProps, "children">
			>
				source={{
					key: "publication-preview",
					Component: ContextPublicationPreview,
					props: {
						workflow: "comparison",
						plan,
						files: originalFiles,
						releases: originalReleases,
						providers: providerReleases,
						artists: originalArtists,
						inventoryVersion: state.inventoryVersion,
						onClose: vi.fn(),
						onQueue: vi.fn(),
					},
				}}
			/>
		</I18nProvider>,
	);
	expect(container.textContent).toContain(
		publicationMessage("en", "selectedMetadataAndAudio"),
	);
	expect(container.textContent).not.toMatch(
		/Будут обновлены|Позиция исключена|Источник и эффективные/,
	);
});

it("queues only the chosen linked source for a first publication", () => {
	localStorage.setItem("music-design-v2-language", "en");
	const releases = structuredClone(originalReleases);
	const album = releases.find(({ id }) => id === "hours")!;
	const [track, sibling] = album.tracks;
	const primary = originalFiles[track.sourceId!];
	const alternative = originalFiles[sibling.sourceId!];
	track.sourceIds = [primary.id, alternative.id];
	const onQueue = vi.fn<(job: ContextualPublicationPreparation) => void>();
	const state = createIncomingState();
	const copy = firstPublicationPreviewMessages.en;
	render(
		<I18nProvider>
			<ContextPublicationPreviewOutlet<DemoPositionPublicationBinderProps>
				source={{
					key: track.id,
					Component: DemoPositionPublication,
					props: {
						destination: fixturePublicationDestination,
						release: album,
						positionId: track.id,
						state,
						files: originalFiles,
						releases,
						providers: providerReleases,
						artists: originalArtists,
						onQueue,
					},
				}}
			/>
		</I18nProvider>,
	);
	fireEvent.click(screen.getByRole("button", { name: copy.preview }));
	const confirm = screen.getByRole("button", { name: copy.publish });
	expect(confirm).toBeDisabled();
	fireEvent.click(
		screen.getByRole("radio", {
			name: new RegExp(alternative.name.split(".")[0]),
		}),
	);
	expect(track.sourceId).toBe(primary.id);
	expect(confirm).toBeEnabled();
	fireEvent.click(confirm);
	const job = onQueue.mock.calls[0]?.[0];
	if (!job) throw new Error("Chosen source did not queue");
	expect(track.published).toBeFalsy();
	expect(
		settleFixture(releases, fixturePublicationDestination, job)?.published
			?.sourceId,
	).toBe(alternative.id);
	const withoutAlternative = structuredClone(releases);
	withoutAlternative.find(({ id }) => id === album.id)!.tracks[0].sourceIds = [
		primary.id,
	];
	expect(
		settleFixture(withoutAlternative, fixturePublicationDestination, job),
	).toBeNull();
});

it("matches demo position controls for unchanged, forced and changed retained previews", async () => {
	localStorage.setItem("music-design-v2-language", "ru");
	const state = createIncomingState();
	const onQueue = vi.fn<(job: ContextualPublicationPreparation) => void>();
	const copy = firstPublicationPreviewMessages.ru;
	const signals = originalReleases.find((item) => item.id === "signals")!;
	const signalsTrack = signals.tracks.find((item) => item.id === "signals-t1")!;
	const baseline = structuredClone(originalReleases);
	const baselineRelease = baseline.find((item) => item.id === signals.id)!;
	const baselineTrack = baselineRelease.tracks.find(
		(item) => item.id === signalsTrack.id,
	)!;
	const baselineDestination: PublicationDestination = {
		...fixturePublicationDestination,
		format: "original",
	};
	// Exercise a same-container demo snapshot and two media sharing a position
	// number. Only the exact requested position may be changed.
	const otherTrack = baselineRelease.tracks.find(
		(item) => item.id !== signalsTrack.id,
	)!;
	otherTrack.position = baselineTrack.position;
	otherTrack.medium = 2;
	baselineRelease.tracks = [
		otherTrack,
		...baselineRelease.tracks.filter(({ id }) => id !== otherTrack.id),
	];
	baselineTrack.published!.path = baselineTrack.published!.path.replace(
		/\.mka$/,
		".flac",
	);
	baselineTrack.published!.container = "FLAC";
	baselineTrack.published!.tags = structuredClone(
		effectivePublicationTags(
			baselineRelease,
			baselineTrack,
			originalArtists,
			originalFiles,
		),
	);
	const view = (
		releases = baseline,
		destination = baselineDestination,
		hasCustomTemplate = false,
	) => {
		const release = releases.find((item) => item.id === signals.id)!;
		return (
			<I18nProvider>
				<ContextPublicationPreviewOutlet<DemoPositionPublicationBinderProps>
					source={{
						key: "demo-position-publication",
						Component: DemoPositionPublication,
						props: {
							destination,
							release,
							positionId: signalsTrack.id,
							state,
							files: originalFiles,
							releases,
							providers: providerReleases,
							artists: originalArtists,
							onQueue,
							hasCustomTemplate,
						},
					}}
				/>
			</I18nProvider>
		);
	};
	const { rerender } = render(view());
	fireEvent.click(screen.getByRole("radio", { name: copy.retainedRepublish }));
	fireEvent.click(screen.getByRole("button", { name: copy.preview }));
	expect(await screen.findByText(copy.retainedUnchanged)).toBeInTheDocument();
	let confirm = screen.getByRole("button", { name: copy.retainedRepublish });
	expect(confirm).toBeDisabled();
	expect(onQueue).not.toHaveBeenCalled();

	const stale = structuredClone(baseline);
	stale
		.find((item) => item.id === signals.id)!
		.tracks.find((item) => item.id === signalsTrack.id)!.published!.readAt =
		"New CURRENT";
	rerender(view(stale));
	expect(screen.getByRole("alert")).toHaveTextContent(
		publicationMessage("ru", "stalePreview"),
	);
	expect(
		screen.getByRole("button", { name: copy.retainedRepublish }),
	).toBeDisabled();
	rerender(view());

	rerender(view(baseline, { ...baselineDestination, format: "m4a" }));
	expect(screen.getByRole("alert")).toHaveTextContent(
		publicationMessage("ru", "stalePreview"),
	);
	confirm = screen.getByRole("button", { name: copy.retainedRepublish });
	expect(confirm).toBeDisabled();
	fireEvent.click(confirm);
	expect(onQueue).not.toHaveBeenCalled();
	rerender(view(baseline, baselineDestination, true));
	expect(screen.getByRole("alert")).toHaveTextContent(
		publicationMessage("ru", "pathUndetermined"),
	);
	expect(
		screen.getByRole("button", { name: copy.retainedRepublish }),
	).toBeDisabled();
	rerender(view());

	fireEvent.click(screen.getByRole("radio", { name: copy.forced }));
	const unavailable = structuredClone(baseline);
	unavailable
		.find((item) => item.id === signals.id)!
		.tracks.find((item) => item.id === signalsTrack.id)!.observedFile = {
		state: "missing",
	};
	rerender(view(unavailable));
	fireEvent.click(screen.getByRole("button", { name: copy.preview }));
	expect(
		screen.getByRole("button", { name: copy.retainedRepublish }),
	).toBeDisabled();
	expect(onQueue).not.toHaveBeenCalled();
	rerender(view());
	fireEvent.click(screen.getByRole("button", { name: copy.preview }));
	expect(
		await screen.findByText(copy.retainedCapabilityReady),
	).toBeInTheDocument();
	confirm = screen.getByRole("button", { name: copy.retainedRepublish });
	expect(confirm).toBeEnabled();
	const currentBeforeAdmission = structuredClone(baselineTrack.published);
	fireEvent.click(confirm);
	expect(onQueue).toHaveBeenCalledTimes(1);
	expect(baselineTrack.published).toEqual(currentBeforeAdmission);
	const [forcedJob] = onQueue.mock.calls[0];
	const settled = settleFixture(baseline, baselineDestination, forcedJob);
	expect(settleFixture(stale, baselineDestination, forcedJob)).toBeNull();
	if (!settled) throw new Error("Forced publication did not settle");
	const forcedUpdate = commitContextualPublication(
		baseline,
		forcedJob,
		settled,
	);
	const forcedTrack = forcedUpdate
		.find((item) => item.id === signals.id)!
		.tracks.find((item) => item.id === signalsTrack.id)!;
	expect(forcedTrack.published?.sourceId).toBe(
		signalsTrack.published!.sourceId,
	);
	expect(forcedTrack.published?.path).toBe(baselineTrack.published!.path);
	expect(forcedTrack.published?.tags).toEqual(baselineTrack.published!.tags);
	expect(
		forcedUpdate
			.find((item) => item.id === signals.id)!
			.tracks.filter((item) => item.id !== signalsTrack.id),
	).toEqual(
		baselineRelease.tracks.filter((item) => item.id !== signalsTrack.id),
	);

	onQueue.mockClear();
	const changed = structuredClone(baseline);
	const changedRelease = changed.find((item) => item.id === signals.id)!;
	const changedTrack = changedRelease.tracks.find(
		(item) => item.id === signalsTrack.id,
	)!;
	changedTrack.recordingMetadata = {
		...changedTrack.recordingMetadata,
		comment: ["Edited comment"],
	};
	rerender(view(changed));
	fireEvent.click(screen.getByRole("radio", { name: copy.ordinary }));
	fireEvent.click(screen.getByRole("radio", { name: copy.retainedRepublish }));
	fireEvent.click(screen.getByRole("button", { name: copy.preview }));
	expect(
		await screen.findByText(copy.retainedCapabilityReady),
	).toBeInTheDocument();
	confirm = screen.getByRole("button", { name: copy.retainedRepublish });
	expect(confirm).toBeEnabled();
	fireEvent.click(confirm);
	expect(onQueue).toHaveBeenCalledTimes(1);
	const [changedJob] = onQueue.mock.calls[0];
	const changedResult = settleFixture(changed, baselineDestination, changedJob);
	if (!changedResult) throw new Error("Ordinary publication did not settle");
	const changedUpdate = commitContextualPublication(
		changed,
		changedJob,
		changedResult,
	);
	const changedPublished = changedUpdate
		.find((item) => item.id === signals.id)!
		.tracks.find((item) => item.id === signalsTrack.id)!.published!;
	expect(changedPublished.sourceId).toBe(signalsTrack.published!.sourceId);
	expect(changedPublished.path).toBe(baselineTrack.published!.path);
	expect(changedPublished.tags.COMMENT).toEqual(["Edited comment"]);
	expect(
		changedUpdate
			.find((item) => item.id === signals.id)!
			.tracks.filter((item) => item.id !== signalsTrack.id),
	).toEqual(
		changedRelease.tracks.filter((item) => item.id !== signalsTrack.id),
	);
});

it("warns about an output downgrade and confirms a compatible ready retained plan once", async () => {
	localStorage.setItem("music-design-v2-language", "ru");
	const copy = firstPublicationPreviewMessages.ru;
	const state = createIncomingState();
	const releases = structuredClone(originalReleases);
	const release = releases.find((item) => item.id === "signals")!;
	const track = release.tracks.find((item) => item.id === "signals-t1")!;
	const source = Object.values(originalFiles).find(
		(file) =>
			file.codec === "MP3" && file.available && Boolean(file.locations?.length),
	)!;
	const destination: PublicationDestination = {
		...fixturePublicationDestination,
		format: "m4a",
	};
	const retainedPath = `${destination.outputDirectory}/Retained/01 - Signal.m4a`;
	track.sourceId = source.id;
	track.sourceIds = [source.id];
	track.observedFile = null;
	track.published = {
		...track.published!,
		codec: "ALAC",
		container: "M4A",
		sourceId: source.id,
		path: retainedPath,
	};
	const onQueue = vi.fn<(job: ContextualPublicationPreparation) => void>();
	render(
		<I18nProvider>
			<ContextPublicationPreviewOutlet<DemoPositionPublicationBinderProps>
				source={{
					key: "demo-position-publication",
					Component: DemoPositionPublication,
					props: {
						destination,
						release,
						positionId: track.id,
						state,
						files: originalFiles,
						releases,
						providers: providerReleases,
						artists: originalArtists,
						onQueue,
					},
				}}
			/>
		</I18nProvider>,
	);
	fireEvent.click(screen.getByRole("radio", { name: copy.retainedRepublish }));
	fireEvent.click(screen.getByRole("button", { name: copy.preview }));
	expect(
		await screen.findByText(copy.outputQualityDowngradeWarning),
	).toBeInTheDocument();
	expect(screen.queryByRole("checkbox")).toBeNull();
	const confirm = screen.getByRole("button", { name: copy.retainedRepublish });
	expect(confirm).toBeEnabled();
	fireEvent.click(confirm);
	expect(onQueue).toHaveBeenCalledTimes(1);
	const [job] = onQueue.mock.calls[0];
	const settled = settleFixture(releases, destination, job);
	expect(settled?.published?.sourceId).toBe(source.id);
	expect(settled?.published?.path).toBe(retainedPath);
});
