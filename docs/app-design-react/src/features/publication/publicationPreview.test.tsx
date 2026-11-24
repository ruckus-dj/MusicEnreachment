import {
	cleanup,
	fireEvent,
	render,
	screen,
	within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
	originalArtists,
	originalFiles,
	originalReleases,
} from "../library/catalogData";
import { firstPublicationPreviewMessages } from "../library/firstPublicationPreviewMessages";
import { ContextPublicationPreview } from "./ContextPublicationPreview";
import {
	createIncomingState,
	makeReleasePlan,
	providerReleases,
} from "../incoming/incomingModel";
import { PublicationPreview } from "./PublicationPreview";
import { captureMetadataPreview } from "./previewModel";
import {
	formatPublicationMessage,
	publicationMessage,
} from "./publicationMessages";
import {
	fixturePublicationDestination,
	type PublicationDestination,
} from "./publicationOutput";
import { applyAudioUpgradePreview } from "../library/trackActionModel";
import { I18nProvider } from "../../i18n/I18nProvider";

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
		<PublicationPreview
			mode="audioUpgrade"
			token={token}
			inventoryVersion={0}
			scopeKey={token.scopeKey}
			releases={result}
			files={originalFiles}
			artists={originalArtists}
			onBack={vi.fn()}
			onApply={(selection) => {
				result = applyAudioUpgradePreview(
					result,
					selection.selectedIds,
					originalFiles,
				);
				return true;
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
			<ContextPublicationPreview
				plan={plan}
				files={originalFiles}
				releases={originalReleases}
				providers={providerReleases}
				artists={originalArtists}
				inventoryVersion={state.inventoryVersion}
				confirmDowngrade={false}
				onPlan={vi.fn()}
				onReleases={vi.fn()}
				onSuccess={vi.fn()}
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
		<PublicationPreview
			destination={destination}
			token={token}
			inventoryVersion={0}
			scopeKey={token.scopeKey}
			releases={originalReleases}
			files={originalFiles}
			artists={originalArtists}
			onApply={vi.fn(() => true)}
			onBack={vi.fn()}
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

it("compares one CURRENT read-only and confirms an explicit republish without a dialog", () => {
	const state = createIncomingState();
	const plan = makeReleasePlan(
		state,
		release,
		originalFiles,
		publishedTrack.id,
	);
	const onReleases = vi.fn();
	const view = (
		force: boolean,
		target: PublicationDestination = destination,
		releases = originalReleases,
	) => (
		<ContextPublicationPreview
			destination={target}
			plan={{ ...plan, force }}
			files={originalFiles}
			releases={releases}
			providers={providerReleases}
			artists={originalArtists}
			inventoryVersion={state.inventoryVersion}
			confirmDowngrade={false}
			onPlan={vi.fn()}
			onReleases={onReleases}
			onSuccess={vi.fn()}
		/>
	);
	const { container, rerender } = render(view(false));
	const copy = firstPublicationPreviewMessages.ru;
	expect(screen.getByText(copy.comparisonReadOnly)).toBeInTheDocument();
	expect(
		screen.queryByRole("button", { name: copy.retainedRepublish }),
	).toBeNull();
	expect(onReleases).not.toHaveBeenCalled();

	rerender(view(true));
	expect(
		screen.getByRole("region", { name: copy.section }),
	).toBeInTheDocument();
	expect(container.textContent).toContain("/current-publication-root/");
	expect(container.textContent).not.toContain(
		`${publishedTrack.published!.codec} → MKA`,
	);
	const confirm = screen.getByRole("button", { name: copy.retainedRepublish });
	expect(confirm).toBeDisabled();
	expect(screen.getByRole("alert")).toHaveTextContent(
		publicationMessage("ru", "currentOutsideOutput"),
	);

	const changed = structuredClone(originalReleases);
	const changedTrack = changed
		.find((item) => item.id === release.id)!
		.tracks.find((item) => item.id === publishedTrack.id)!;
	changedTrack.published!.readAt = "New CURRENT";
	rerender(view(true, fixturePublicationDestination, changed));
	expect(confirm).toBeDisabled();
	expect(screen.getByRole("alert")).toHaveTextContent(
		publicationMessage("ru", "stalePreview"),
	);

	changedTrack.published!.readAt = publishedTrack.published!.readAt;
	changedTrack.title = "Renamed after preview";
	rerender(view(true, fixturePublicationDestination, changed));
	expect(confirm).toBeDisabled();

	rerender(view(true, fixturePublicationDestination));
	expect(confirm).toBeEnabled();
	fireEvent.click(confirm);
	expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
	expect(onReleases).toHaveBeenCalledTimes(1);
	const updated = (onReleases.mock.calls[0][0] as typeof originalReleases).find(
		(item) => item.id === release.id,
	)!;
	expect(
		updated.tracks.find((item) => item.id === publishedTrack.id)?.published
			?.sourceId,
	).toBe(publishedTrack.sourceId);
	expect(
		updated.tracks.filter((item) => item.id !== publishedTrack.id),
	).toEqual(release.tracks.filter((item) => item.id !== publishedTrack.id));
});
