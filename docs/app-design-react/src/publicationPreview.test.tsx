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
	originalProviders,
	originalReleases,
} from "./catalogData";
import { ContextPublicationPreview } from "./ContextPublicationPreview";
import {
	createIncomingState,
	incomingFixtures,
	makePlan,
	makeReleasePlan,
	providerReleases,
} from "./incomingModel";
import { PublicationPreview } from "./PublicationPreview";
import { captureMetadataPreview } from "./previewModel";
import {
	formatPublicationMessage,
	publicationMessage,
} from "./publicationMessages";
import { fixturePublicationDestination } from "./publicationOutput";
import { applyAudioUpgradePreview } from "./trackActionModel";
import { I18nProvider } from "./i18n/I18nProvider";

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
				onBack={vi.fn()}
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

it("incoming match preview also uses the shared publication confirmation", () => {
	const state = createIncomingState();
	state.groupId = "north";
	const plan = makePlan(
		state,
		incomingFixtures.files,
		incomingFixtures.releases,
	);
	if (!plan) throw new Error("Missing incoming plan fixture");
	const { container } = render(
		<ContextPublicationPreview
			plan={plan}
			files={incomingFixtures.files}
			releases={incomingFixtures.releases}
			providers={originalProviders}
			artists={originalArtists}
			inventoryVersion={state.inventoryVersion}
			confirmDowngrade={false}
			onPlan={vi.fn()}
			onReleases={vi.fn()}
			onSuccess={vi.fn()}
			onBack={vi.fn()}
		/>,
	);

	expect(screen.getByRole("heading", { name: /Предпросмотр:/ })).toBeTruthy();
	expect(
		screen.getByRole("button", {
			name: publicationMessage("ru", "confirmPublication"),
		}),
	).toBeTruthy();
	expect(container.querySelector("[aria-label^='Предпросмотр:']")).toBeTruthy();
});

it("track and incoming plans render the same flow with force destination facts", () => {
	const state = createIncomingState();
	const plan = makeReleasePlan(
		state,
		release,
		originalFiles,
		publishedTrack.id,
	);
	plan.force = true;
	const { container } = render(
		<ContextPublicationPreview
			destination={destination}
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
			onBack={vi.fn()}
		/>,
	);

	expect(
		screen.getByRole("heading", {
			name: publicationMessage("ru", "contextTitle").replace(
				"{title}",
				release.title,
			),
		}),
	).toBeTruthy();
	expect(container.querySelector("[aria-label^='Предпросмотр:']")).toBeTruthy();
	expect(container.textContent).toContain("/current-publication-root/");
	expect(container.textContent).toContain("MKA");
	expect(container.textContent).not.toContain(
		`${publishedTrack.published!.codec} → MKA`,
	);
});
