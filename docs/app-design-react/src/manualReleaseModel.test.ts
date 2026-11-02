import { describe, expect, it } from "vitest";
import {
	createManualRelease,
	emptyManualMedium,
	emptyManualPosition,
	type ManualReleaseDraft,
} from "./manualReleaseModel";

const emptyDraft = (): ManualReleaseDraft => ({
	title: "",
	metadata: { date: "", country: "", label: "", catalogNumber: "", type: "" },
	artists: [],
	media: [],
	coverId: "",
});

describe("manual release model", () => {
	it("allows the original optional, empty form to produce an incomplete release", () => {
		const draft = emptyDraft();
		expect(emptyManualMedium()).toEqual({
			title: "",
			format: "",
			positions: [],
		});
		expect(emptyManualPosition()).toMatchObject({
			number: "",
			title: "",
			duration: "",
			sourceIds: [],
			selectedSourceId: "",
		});
		const { release, artists } = createManualRelease(draft, 1, []);
		expect(release).toMatchObject({
			title: "",
			year: "",
			artistId: null,
			tracks: [],
			manual: true,
		});
		expect(artists).toEqual([]);
	});

	it("creates metadata and a demo artist for a newly named artist", () => {
		const draft = emptyDraft();
		draft.title = "Night Signals";
		draft.metadata = {
			date: "2024",
			country: "CA",
			label: "North",
			catalogNumber: "NS-1",
			type: "EP",
		};
		draft.artists = [
			{ name: "New Artist", credit: "N. Artist", role: "main", artistId: null },
		];
		const { release, artists } = createManualRelease(draft, 7, []);
		expect(release).toMatchObject({
			title: "Night Signals",
			year: "2024",
			country: "CA",
			label: "North",
			catalog: "NS-1",
			metadata: draft.metadata,
			credits: [
				{
					name: "New Artist",
					credit: "N. Artist",
					role: "main",
					artistId: "manual-7-artist-1",
				},
			],
			artistId: "manual-7-artist-1",
		});
		expect(artists).toContainEqual({
			id: "manual-7-artist-1",
			name: "New Artist",
			genre: "Демо",
			color: "slate",
		});
	});

	it("flattens media and positions in reordered display order", () => {
		const draft = emptyDraft();
		draft.media = [
			{
				title: "Second side",
				format: "LP",
				positions: [
					{
						...emptyManualPosition(),
						number: "B1",
						title: "Second",
						duration: "3:00",
					},
				],
			},
			{
				title: "First side",
				format: "LP",
				positions: [
					{
						...emptyManualPosition(),
						number: "A1",
						title: "First",
						duration: "2:00",
					},
				],
			},
		];
		const { release } = createManualRelease(draft, 1, []);
		expect(
			release.tracks.map(({ title, position, medium, mediumPosition }) => ({
				title,
				position,
				medium,
				mediumPosition,
			})),
		).toEqual([
			{ title: "Second", position: 1, medium: 1, mediumPosition: 1 },
			{ title: "First", position: 2, medium: 2, mediumPosition: 1 },
		]);
	});
});
