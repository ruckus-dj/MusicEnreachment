import { expect, it } from "vitest";
import {
	fieldByKey,
	fieldsForScope,
	metadataFieldLabel,
	metadataGroupLabel,
	navidromeFields,
} from "./registry";
import {
	metadataKeyToTag,
	projectMetadataValues,
	validateMetadataValues,
	validateMetadataIssues,
	metadataValidationMessage,
} from "./projection";
import {
	readMetadataCredits,
	readMetadataContributors,
	readMetadataWorks,
	writeMetadataContributors,
	writeMetadataCredits,
	writeMetadataWorks,
} from "./model";
import { desiredTags } from "../catalogData";

it("projects joined display credits separately from ordered individual names", () => {
	const credits = [
		{ artistId: "a", creditName: "A", joinPhrase: " feat. " },
		{ artistId: "b", creditName: "B", joinPhrase: "" },
	];
	const values = writeMetadataCredits({}, "artist", credits);
	expect(values.artist).toEqual(["A feat. B"]);
	expect(values.artists).toEqual(["A", "B"]);
	expect(projectMetadataValues(values).tags.ARTIST).toEqual(["A feat. B"]);
	expect(
		readMetadataCredits(values, "artist").map((credit) => credit.creditName),
	).toEqual(["A", "B"]);
});

it("validates work UUIDs independently of their editor presentation kind", () => {
	expect(
		validateMetadataValues({ musicbrainz_workid: ["not-a-uuid"] }, "recording"),
	).not.toEqual([]);
	expect(
		validateMetadataValues(
			{ musicbrainz_workid: ["12345678-1234-4234-8234-123456789abc"] },
			"recording",
		),
	).toEqual([]);
});

it("uses the advisory values Navidrome actually reads", () => {
	for (const value of ["0", "1", "2", "4"])
		expect(
			validateMetadataValues({ explicitstatus: [value] }, "recording"),
		).toEqual([]);
	for (const value of ["true", "false", "explicit", "clean"])
		expect(
			validateMetadataValues({ explicitstatus: [value] }, "recording"),
		).not.toEqual([]);
});

it("registers the complete unique 89-field Navidrome mapping", () => {
	expect(navidromeFields).toHaveLength(89);
	expect(new Set(navidromeFields.map((field) => field.key)).size).toBe(89);
	expect(
		fieldsForScope("audio").every((field) => field.scope === "audio"),
	).toBe(true);
	expect(fieldByKey("artistsort")?.derived).toBe(true);
	expect(fieldByKey("musicbrainz_artistid")?.kind).toBe("identity");
	expect(fieldByKey("script")?.scope).toBe("release");
});

it("localizes every registered field and group without changing registry identities", () => {
	for (const field of navidromeFields) {
		expect(metadataFieldLabel(field.key, "en"), field.key).not.toBe(field.key);
		expect(metadataFieldLabel(field.key, "ru")).toBe(field.label);
	}
	expect(metadataFieldLabel("unknown_vendor_tag", "en")).toBe(
		"unknown_vendor_tag",
	);
	expect(metadataGroupLabel("recording artists", "en")).toBe(
		"Recording artists",
	);
	expect(metadataGroupLabel("recording artists", "ru")).toBe(
		"Исполнители записи",
	);
	expect(fieldByKey("artist")).toMatchObject({
		key: "artist",
		tag: "ARTIST",
		group: "recording artists",
	});
});

it("keeps structured validation messages locale-aware while preserving the legacy string API", () => {
	const issues = validateMetadataIssues(
		{ releasedate: ["2026-02-30"] },
		"release",
	);
	expect(issues).toEqual([{ key: "releasedate", code: "date" }]);
	expect(metadataValidationMessage(issues[0], "en")).toBe(
		"Release date must be YYYY, YYYY-MM, or YYYY-MM-DD",
	);
	expect(metadataValidationMessage(issues[0], "ru")).toBe(
		"Некорректное значение поля «Дата выпуска»: ожидается формат YYYY, YYYY-MM или YYYY-MM-DD",
	);
	expect(
		validateMetadataValues({ releasedate: ["2026-02-30"] }, "release"),
	).toEqual(["releasedate must be YYYY, YYYY-MM, or YYYY-MM-DD"]);
});

it("keeps descriptive identifiers editable and does not synthesize entity MBIDs", () => {
	expect(fieldByKey("asin")?.kind).toBe("identity");
	expect(fieldByKey("musicbrainz_discid")?.kind).toBe("identity");
	const tags = desiredTags(
		{
			id: "release",
			title: "Album",
			artistId: "local-artist",
			year: "2020",
			country: "",
			catalog: "",
			label: "",
			providerId: "provider-local-id",
			provider: "MusicBrainz",
			tracks: [],
		},
		{
			id: "track",
			title: "Track",
			position: 1,
			duration: null,
			artistId: "local-artist",
			sourceId: null,
			published: null,
			recordingId: "demo-recording-id",
		},
	);
	expect(tags.MUSICBRAINZ_ARTISTID).toEqual([]);
	expect(tags.MUSICBRAINZ_ALBUMID).toEqual([]);
	expect(tags.MUSICBRAINZ_TRACKID).toEqual([]);
	expect(tags.DATE).toEqual([]);
	expect(tags.RELEASEDATE).toEqual(["2020"]);
});

it("uses file aliases and preserves explicit empty-list clears", () => {
	const projection = projectMetadataValues({
		musicbrainz_recordingid: ["12345678-1234-4234-8234-123456789abc"],
		musicbrainz_trackid: ["abcdefab-1234-4234-8234-123456789abc"],
		recordlabel: ["Label"],
		explicitstatus: ["1"],
		releasedate: ["2026-11"],
		comment: [],
		artistsort: ["must not be edited"],
	});
	expect(projection.tags).toEqual({
		MUSICBRAINZ_TRACKID: ["12345678-1234-4234-8234-123456789abc"],
		MUSICBRAINZ_RELEASETRACKID: ["abcdefab-1234-4234-8234-123456789abc"],
		LABEL: ["Label"],
		ITUNESADVISORY: ["1"],
		RELEASEDATE: ["2026-11"],
		COMMENT: [],
	});
	expect(metadataKeyToTag("recordlabel")).toBe("LABEL");
	expect(metadataKeyToTag("missing")).toBeUndefined();
});

it("rejects unknown and wrong-scope values and accepts empty clear values", () => {
	expect(
		validateMetadataValues(
			{ unknown: ["x"], album: ["release"], comment: [] },
			"recording",
		),
	).toEqual([
		"Unknown metadata field: unknown",
		"album belongs to release, not recording",
	]);
	expect(validateMetadataValues({ comment: [] }, "recording")).toEqual([]);
	expect(
		validateMetadataValues({ releasedate: ["2026-02-30"] }, "release"),
	).toHaveLength(1);
});

it("validates exact partial dates, including years below 100", () => {
	expect(
		validateMetadataValues({ originaldate: ["0001-02-28"] }, "recording"),
	).toEqual([]);
	expect(
		validateMetadataValues({ originaldate: ["0001-02-29"] }, "recording"),
	).toHaveLength(1);
	expect(
		validateMetadataValues({ originaldate: ["2024-02"] }, "recording"),
	).toEqual([]);
});

it("round-trips ordered credits and works without shifting sparse identities", () => {
	const values = {
		artist: ["First", "Second"],
		artists: ["First", "Second"],
		musicbrainz_artistid: ["", "12345678-1234-4234-8234-123456789abc"],
		work: ["One", "Two"],
		musicbrainz_workid: ["", "abcdefab-1234-4234-8234-123456789abc"],
	};
	expect(
		readMetadataCredits(values, "artist").map((credit) => credit.mbid ?? ""),
	).toEqual(["", "12345678-1234-4234-8234-123456789abc"]);
	expect(readMetadataWorks(values).map((work) => work.mbid ?? "")).toEqual([
		"",
		"abcdefab-1234-4234-8234-123456789abc",
	]);
	expect(
		writeMetadataCredits(values, "artist", [
			{ artistId: "", creditName: "First", joinPhrase: " & " },
		]).artists,
	).toEqual(["First"]);
	expect(
		writeMetadataWorks(values, [{ title: "One", mbid: "" }]).musicbrainz_workid,
	).toEqual([""]);
});

it("stores performer instruments in registered performer values with aligned IDs", () => {
	const values = writeMetadataContributors(
		{ performerinstrument: ["legacy violin"] },
		"performer",
		[
			{
				name: "First",
				instrument: "violin",
				mbid: "12345678-1234-4234-8234-123456789abc",
			},
			{ name: "Second" },
		],
	);
	expect(values).toEqual({
		performer: ["First (violin)", "Second"],
		musicbrainz_performerid: ["12345678-1234-4234-8234-123456789abc", ""],
	});
	expect(values).not.toHaveProperty("performerinstrument");
	expect(readMetadataContributors(values, "performer")).toEqual([
		{
			name: "First",
			instrument: "violin",
			mbid: "12345678-1234-4234-8234-123456789abc",
		},
		{ name: "Second" },
	]);
	expect(validateMetadataValues(values, "recording")).toEqual([]);
});

it("reads MusicBrainz credit identities separately from local artist links", () => {
	const credits = readMetadataCredits(
		{
			artist: ["First"],
			musicbrainz_artistid: ["12345678-1234-4234-8234-123456789abc"],
		},
		"artist",
	);
	expect(credits).toEqual([
		{
			artistId: "",
			mbid: "12345678-1234-4234-8234-123456789abc",
			creditName: "First",
			joinPhrase: "",
		},
	]);
	const written = writeMetadataCredits({}, "artist", credits);
	expect(written.musicbrainz_artistid).toEqual([
		"12345678-1234-4234-8234-123456789abc",
	]);
});
