import { formatLocalDuration } from "../local-release/localReleaseFormModel";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
import {
	type LocalCatalogType,
	localCatalogArtistNames,
	localCatalogGenreNames,
	localCatalogRecordingTitle,
	localCatalogReleaseTitle,
} from "./localCatalogModel";
import type {
	LocalProviderRecord,
	LocalProviderState,
} from "./localProviderModel";
import { OwnCatalogDetailsView } from "./OwnCatalogDetailsView";

export function LocalCatalogDetails({
	locale,
	state,
	type,
	record,
	onBack,
	onEdit,
}: {
	locale: "ru" | "en";
	state: LocalProviderState;
	type: LocalCatalogType;
	record: LocalProviderRecord;
	onBack: () => void;
	onEdit: () => void;
}) {
	const m = (key: LocalCatalogMessage) => localCatalogMessage(locale, key);
	const fact = (key: LocalCatalogMessage, value?: string | null) => ({
		label: m(key),
		value: value ?? "",
	});
	const title =
		(type === "artists"
			? record.name
			: type === "recordings"
				? localCatalogRecordingTitle(record)
				: localCatalogReleaseTitle(record)) || "—";
	const artists = localCatalogArtistNames(state, record.artistIds).join(", ");
	const facts = [fact("providerKey", record.key)];
	if (type === "artists") {
		const related = state.items.releases
			.filter((release) => release.artistIds?.includes(record.id))
			.map(localCatalogReleaseTitle)
			.join(", ");
		facts.push(
			fact("name", record.name),
			fact("sortName", record.sortName),
			fact("relatedReleases", related || m("noLinkedReleases")),
		);
	}
	if (type === "recordings") {
		const usedIn = state.items.releases
			.filter((release) =>
				release.tracks?.some((track) => track.recordingId === record.id),
			)
			.map(localCatalogReleaseTitle)
			.join(", ");
		facts.push(
			fact("recordingArtists", artists),
			fact("duration", formatLocalDuration(record.duration_ms)),
			fact("explicit", record.explicit),
			fact("isrc", record.isrcs),
			fact("recordingUsedIn", usedIn),
		);
	}
	if (type === "releases")
		facts.push(
			fact("releaseGroup", record.release_group_title),
			fact("releaseArtists", artists),
			fact("primaryType", record.primaryType),
			fact("language", record.language),
			fact("script", record.script),
			fact("releaseDate", record.release_date),
			fact("country", record.country_code),
			fact("format", record.format),
			fact("label", record.label),
			fact("catalogNumber", record.catalog_number),
		);
	facts.push(
		fact("disambiguation", record.disambiguation),
		fact("genres", localCatalogGenreNames(record).join(", ")),
		fact("tags", record.tags),
	);
	return (
		<OwnCatalogDetailsView
			title={title}
			eyebrow={m("currentDataNoHistory")}
			backLabel={m("back")}
			editLabel={m("edit")}
			onBack={onBack}
			onEdit={onEdit}
			body={{
				kind: "record",
				facts,
				tracklist:
					type === "releases"
						? {
								title: m("tracklist"),
								positions: m("positions"),
								empty: m("noPositions"),
								tracks: (record.tracks ?? []).map((track, index) => {
									const recording = state.items.recordings.find(
										(item) => item.id === track.recordingId,
									);
									const duration =
										track.durationOverrideMs === "" ||
										track.durationOverrideMs == null
											? recording?.duration_ms
											: track.durationOverrideMs;
									return {
										id: track.id || String(index),
										number: String(index + 1),
										title:
											track.titleOverride ||
											(recording
												? localCatalogRecordingTitle(recording)
												: m("recordingNotSelected")),
										description: [
											recording?.provider,
											formatLocalDuration(duration),
										]
											.filter(Boolean)
											.join(" · "),
									};
								}),
							}
						: undefined,
			}}
		/>
	);
}
