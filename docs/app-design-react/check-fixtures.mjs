import { readFile } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const fixturePath = new URL("./src/original-fixtures.json", import.meta.url);

function requireObject(name, value) {
	if (!value || typeof value !== "object" || Array.isArray(value))
		throw new Error(`${name} must be an object`);
	return value;
}

function requireArray(container, name) {
	const value = container[name];
	if (!Array.isArray(value)) throw new Error(`${name} must be an array`);
	return value;
}

function requireUniqueIds(name, records) {
	const ids = records.map((record) => record?.id);
	if (ids.some((id) => typeof id !== "string" || !id))
		throw new Error(`${name} records must have non-empty IDs`);
	if (new Set(ids).size !== ids.length)
		throw new Error(`${name} contains duplicate IDs`);
	return new Set(ids);
}

function checkReference(condition, location, target) {
	if (!condition) throw new Error(`${location} refers to missing ${target}`);
}

export function validateFixtures(data) {
	requireObject("fixture root", data);

	const artists = requireUniqueIds("artists", requireArray(data, "artists"));
	const releases = requireUniqueIds("releases", requireArray(data, "releases"));
	const providerReleases = requireUniqueIds(
		"providerReleases",
		requireArray(data, "providerReleases"),
	);
	requireUniqueIds("incomingGroups", requireArray(data, "incomingGroups"));
	requireUniqueIds("absentGroups", requireArray(data, "absentGroups"));
	const files = requireObject("files", data.files);
	const fileIds = new Set(Object.keys(files));
	const localProvider = requireObject("localProvider", data.localProvider);
	const localIds = Object.fromEntries(
		["artists", "releases", "recordings"].map((kind) => [
			kind,
			requireUniqueIds(
				`localProvider.${kind}`,
				requireArray(localProvider, kind),
			),
		]),
	);
	const localReleaseKeys = new Set(
		localProvider.releases
			.filter((release) => release.provider === "Local")
			.map((release) => release.key),
	);
	for (const release of requireArray(data, "releases"))
		checkReference(
			typeof release.providerId === "string" &&
				(providerReleases.has(release.providerId) ||
					localReleaseKeys.has(release.providerId)),
			`releases ${release.id}`,
			"a provider identity",
		);

	const manualReleaseDemoData = requireObject(
		"manualReleaseDemoData",
		data.manualReleaseDemoData,
	);
	const demoArtists = requireUniqueIds(
		"manualReleaseDemoData.artists",
		requireArray(manualReleaseDemoData, "artists"),
	);
	const demoRecordings = requireUniqueIds(
		"manualReleaseDemoData.recordings",
		requireArray(manualReleaseDemoData, "recordings"),
	);
	const demoSources = requireUniqueIds(
		"manualReleaseDemoData.sources",
		requireArray(manualReleaseDemoData, "sources"),
	);
	requireUniqueIds(
		"manualReleaseDemoData.covers",
		requireArray(manualReleaseDemoData, "covers"),
	);

	const recordingIds = new Set([...demoRecordings, ...localIds.recordings]);
	const trackIds = new Set();

	for (const id of demoArtists)
		checkReference(
			artists.has(id),
			`manualReleaseDemoData.artists ${id}`,
			"artist",
		);
	for (const id of demoSources)
		checkReference(
			fileIds.has(id),
			`manualReleaseDemoData.sources ${id}`,
			"file",
		);

	for (const [name, collection] of [
		["releases", requireArray(data, "releases")],
		["providerReleases", requireArray(data, "providerReleases")],
	]) {
		for (const release of collection) {
			if (release.artistId != null)
				checkReference(
					artists.has(release.artistId),
					`${name} ${release.id}`,
					"artist",
				);
			for (const track of release.tracks ?? []) {
				if (typeof track.id !== "string" || !track.id)
					throw new Error(
						`${name} ${release.id} tracks must have non-empty IDs`,
					);
				if (trackIds.has(track.id))
					throw new Error(
						`${name} ${release.id} track ${track.id} duplicates a track ID`,
					);
				trackIds.add(track.id);
				if (track.artistId != null)
					checkReference(
						artists.has(track.artistId),
						`${name} ${release.id} track ${track.id}`,
						"artist",
					);
				if (track.recordingId != null)
					checkReference(
						recordingIds.has(track.recordingId),
						`${name} ${release.id} track ${track.id}`,
						"recording",
					);
				if (track.localRecordingId != null)
					checkReference(
						localIds.recordings.has(track.localRecordingId),
						`${name} ${release.id} track ${track.id}`,
						"Local recording",
					);
				if (track.sourceId != null)
					checkReference(
						fileIds.has(track.sourceId),
						`${name} ${release.id} track ${track.id}`,
						"file",
					);
				for (const id of track.sourceIds ?? [])
					checkReference(
						fileIds.has(id),
						`${name} ${release.id} track ${track.id}`,
						"file",
					);
			}
		}
	}
	for (const group of requireArray(data, "incomingGroups")) {
		for (const id of group.fileIds ?? [])
			checkReference(fileIds.has(id), `incoming group ${group.id}`, "file");
		if (group.releaseId != null)
			checkReference(
				releases.has(group.releaseId) || providerReleases.has(group.releaseId),
				`incoming group ${group.id}`,
				"release",
			);
	}
	for (const group of requireArray(data, "absentGroups")) {
		if (group.artistId != null)
			checkReference(
				artists.has(group.artistId),
				`absent group ${group.id}`,
				"artist",
			);
	}
	for (const kind of ["artists", "releases", "recordings"]) {
		for (const record of requireArray(localProvider, kind)) {
			const artistIds = [
				...(record.artistIds ?? []),
				...(record.artistId != null ? [record.artistId] : []),
			];
			for (const artistId of artistIds)
				checkReference(
					localIds.artists.has(artistId) || artists.has(artistId),
					`localProvider.${kind} ${record.id}`,
					"artist",
				);
			if (record.releaseId != null)
				checkReference(
					localIds.releases.has(record.releaseId) ||
						releases.has(record.releaseId) ||
						providerReleases.has(record.releaseId),
					`localProvider.${kind} ${record.id}`,
					"release",
				);
		}
	}
	for (const release of requireArray(localProvider, "releases")) {
		for (const [index, track] of (release.tracks ?? []).entries()) {
			if (track.recordingId == null) continue;
			checkReference(
				localIds.recordings.has(track.recordingId),
				`localProvider.releases ${release.id} track ${index + 1}`,
				"recording",
			);
		}
	}

	const recoveryFixture = requireObject(
		"recoveryFixture",
		data.recoveryFixture,
	);
	checkReference(
		typeof recoveryFixture.trackId === "string" &&
			trackIds.has(recoveryFixture.trackId),
		"recoveryFixture",
		"track",
	);

	const criticalIdentities = {
		artists: ["mira", "arden", "forma", "various"],
		releases: [
			"north",
			"hours",
			"signals",
			"rooms",
			"mix",
			"north-japan-local",
		],
		providerReleases: ["p-north", "p-japan", "p-hours", "p-signals", "p-rooms"],
	};
	for (const [name, expected] of Object.entries(criticalIdentities)) {
		const actual = requireArray(data, name).map((record) => record.id);
		checkReference(
			JSON.stringify(actual) === JSON.stringify(expected),
			name,
			"the expected canonical fixture identities",
		);
	}
	checkReference(fileIds.has("north-f3"), "files", "canonical source north-f3");
	checkReference(
		localIds.artists.has("local-artist-vale"),
		"localProvider.artists",
		"canonical identity local-artist-vale",
	);

	return {
		artists,
		releases,
		providerReleases,
		fileIds,
		localIds,
		recordingIds,
		trackIds,
	};
}

const isDirectInvocation =
	process.argv[1] != null &&
	fileURLToPath(import.meta.url) === resolve(process.argv[1]);
if (isDirectInvocation) {
	const fixtures = JSON.parse(await readFile(fixturePath, "utf8"));
	validateFixtures(fixtures);
	console.log("Canonical design fixtures are structurally valid.");
}
