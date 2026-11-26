import {
	cleanup,
	fireEvent,
	render,
	screen,
	within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../../app/i18n/host";
import { firstPublicationPreviewMessages } from "../../../ui/features/library/firstPublicationPreviewMessages";
import { ContextPublicationPreviewOutlet } from "../../../ui/features/publication/ContextPublicationPreview";
import { InlinePublicationPreviewOutlet } from "../../../ui/features/publication/InlinePublicationPreview";
import {
	formatPublicationMessage,
	publicationMessage,
} from "../../../ui/features/publication/publicationMessages";
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
import { applyAudioUpgradePreview } from "../library/trackActionModel";
import {
	ContextPublicationPreview,
	type ContextPublicationPreviewProps,
	DemoPositionPublication,
	type DemoPositionPublicationBinderProps,
} from "./ContextPublicationPreview";
import {
	PublicationPreview,
	type PublicationPreviewBinderProps,
} from "./PublicationPreview";
import { captureMetadataPreview } from "./previewModel";
import {
	fixturePublicationDestination,
	type PublicationDestination,
} from "./publicationOutput";

afterEach(() => {
	cleanup();
	localStorage.clear();
});

it("audio-only publication uses shared confirmation without applying pending metadata or old measurements", () => {
	const current = structuredClone(
		originalReleases.find((item) => item.id === "north")!,
	);
	const track = current.tracks.find((item) => item.published)!;
	const source = Object.values(originalFiles).find(
		(file) => file.codec === "FLAC" && file.available && file.locations?.length,
	)!;
	track.sourceId = source.id;
	track.title = "Pending title must not be published";
	track.audioMetadata = {};
	track.published = {
		...track.published!,
		codec: "MP3",
		tags: { TITLE: ["CURRENT title"], REPLAYGAIN_TRACK_GAIN: ["-5"] },
		coverHash: "CURRENT-cover",
		lyrics: "CURRENT lyrics",
	};
	const before = structuredClone(track.published);
	const token = captureMetadataPreview(0, "audio-only", [track.id]);
	let result = [current];
	render(
		<InlinePublicationPreviewOutlet<PublicationPreviewBinderProps>
			locale="ru"
			source={{
				key: token.scopeKey,
				Component: PublicationPreview,
				props: {
					mode: "audioUpgrade",
					token,
					inventoryVersion: 0,
					scopeKey: token.scopeKey,
					releases: result,
					files: originalFiles,
					artists: originalArtists,
					onBack: vi.fn(),
					onApply: (selection) => {
						result = applyAudioUpgradePreview(
							result,
							selection.selectedIds,
							originalFiles,
						);
						return true;
					},
				},
			}}
		/>,
	);
	expect(
		screen.getByRole("heading", {
			name: publicationMessage("ru", "previewTitle"),
		}),
	).toBeInTheDocument();
	fireEvent.click(
		screen.getByRole("button", {
			name: formatPublicationMessage("ru", "applyCount", {
				create: 0,
				update: 1,
			}),
		}),
	);
	const dialog = screen.getByRole("dialog");
	expect(dialog).toHaveTextContent(
		publicationMessage("ru", "audioUpgradeDescription"),
	);
	fireEvent.click(
		within(dialog).getByRole("button", {
			name: publicationMessage("ru", "confirm"),
		}),
	);
	const published = result[0].tracks.find((item) => item.id === track.id)!
		.published!;
	expect(published.tags.TITLE).toEqual(before.tags.TITLE);
	expect(published.tags.REPLAYGAIN_TRACK_GAIN).toBeUndefined();
	expect(published.coverHash).toBe(before.coverHash);
	expect(published.lyrics).toBe(before.lyrics);
	expect(published.path).toBe(before.path);
	expect(published.codec).toBe("FLAC");
});

const release = originalReleases.find((item) => item.id === "north")!;
const publishedTrack = release.tracks.find((track) => track.published)!;
const destination = {
	...fixturePublicationDestination,
	outputDirectory: "/current-publication-root",
	format: "mka" as const,
};

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
						confirmDowngrade: false,
						onPlan: vi.fn(),
						onReleases: vi.fn(),
						onSuccess: vi.fn(),
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

it("bulk metadata preview renders the shared publication flow and current facts", () => {
	const token = captureMetadataPreview(0, `selection:${publishedTrack.id}`, [
		publishedTrack.id,
	]);
	const { container } = render(
		<InlinePublicationPreviewOutlet<PublicationPreviewBinderProps>
			locale="ru"
			source={{
				key: token.scopeKey,
				Component: PublicationPreview,
				props: {
					destination,
					token,
					inventoryVersion: 0,
					scopeKey: token.scopeKey,
					releases: originalReleases,
					files: originalFiles,
					artists: originalArtists,
					onApply: vi.fn(() => true),
					onBack: vi.fn(),
				},
			}}
		/>,
	);

	expect(
		screen.getByRole("heading", {
			name: publicationMessage("ru", "previewTitle"),
		}),
	).toBeTruthy();
	expect(container.textContent).toContain(publishedTrack.published!.path);
	expect(
		container.querySelector("[aria-label='Предпросмотр публикации']"),
	).toBeTruthy();
});

it("matches demo position controls for unchanged, forced and changed retained previews", async () => {
	localStorage.setItem("music-design-v2-language", "ru");
	const state = createIncomingState();
	const onReleases = vi.fn();
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
							onReleases,
							onSuccess: vi.fn(),
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
	expect(onReleases).not.toHaveBeenCalled();

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
	expect(onReleases).not.toHaveBeenCalled();
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
	expect(onReleases).not.toHaveBeenCalled();
	rerender(view());
	fireEvent.click(screen.getByRole("button", { name: copy.preview }));
	expect(
		await screen.findByText(copy.retainedCapabilityReady),
	).toBeInTheDocument();
	confirm = screen.getByRole("button", { name: copy.retainedRepublish });
	expect(confirm).toBeEnabled();
	fireEvent.click(confirm);
	expect(onReleases).toHaveBeenCalledTimes(1);
	const forcedUpdate = onReleases.mock.calls[0][0] as typeof originalReleases;
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

	onReleases.mockClear();
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
	expect(onReleases).toHaveBeenCalledTimes(1);
	const changedUpdate = onReleases.mock.calls[0][0] as typeof originalReleases;
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
