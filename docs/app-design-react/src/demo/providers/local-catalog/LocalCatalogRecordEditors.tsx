import { useState } from "react";
import { OwnCatalogRecordEditorView } from "../../../ui/features/local-catalog/OwnCatalogRecordEditorView";
import {
	formatLocalDuration,
	parseLocalDuration,
} from "../../../ui/features/local-release/localReleaseFormModel";
import {
	GenrePicker,
	type GenreReference,
} from "../library/metadata/GenrePicker";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
import type {
	LocalArtistFormDraft,
	LocalRecordingFormDraft,
} from "./localCatalogModel";
import type { LocalProviderRecord } from "./localProviderModel";

type GenreOption = { id: string; name: string };

function genreReferences(record: LocalProviderRecord | undefined) {
	return (record?.genres ?? []).map((id) => ({
		id,
		name: record?.genreNamesById?.[id] ?? id,
	}));
}

export function LocalCatalogArtistForm({
	locale,
	record,
	genresLoaded,
	onLoadGenres,
	genreOptions,
	onSave,
	onBack,
}: {
	locale: "ru" | "en";
	record?: LocalProviderRecord;
	genresLoaded: boolean;
	onLoadGenres?: () => void | Promise<void>;
	genreOptions?: readonly GenreOption[];
	onSave: (draft: LocalArtistFormDraft) => void;
	onBack: () => void;
}) {
	const m = (key: LocalCatalogMessage) => localCatalogMessage(locale, key);
	const [name, setName] = useState(record?.name ?? "");
	const [sortName, setSortName] = useState(record?.sortName ?? "");
	const [disambiguation, setDisambiguation] = useState(
		record?.disambiguation ?? "",
	);
	const [tags, setTags] = useState(record?.tags ?? "");
	const [genres, setGenres] = useState<GenreReference[]>(() =>
		genreReferences(record),
	);
	return (
		<OwnCatalogRecordEditorView
			notice={m("formRequiredNotice")}
			fields={[
				{
					label: `${m("name")} · ${m("required")}`,
					value: name,
					onChange: setName,
					required: true,
				},
				{
					label: `${m("sortName")} · ${m("optional")}`,
					value: sortName,
					onChange: setSortName,
				},
				{
					label: `${m("disambiguation")} · ${m("optional")}`,
					value: disambiguation,
					onChange: setDisambiguation,
				},
				{
					label: `${m("tagsCommaSeparated")} · ${m("optional")}`,
					value: tags,
					onChange: setTags,
				},
			]}
			genre={
				<GenrePicker
					value={genres}
					onChange={setGenres}
					loaded={genresLoaded}
					onLoad={onLoadGenres}
					options={genreOptions}
				/>
			}
			save={{
				label: m("save"),
				disabled: !name.trim(),
				onPress: () =>
					onSave({
						name: name.trim(),
						sortName: sortName.trim(),
						disambiguation: disambiguation.trim(),
						tags,
						genres,
					}),
			}}
			cancel={{ label: m("cancel"), onPress: onBack }}
		/>
	);
}

export function LocalCatalogRecordingForm({
	locale,
	record,
	artists,
	genresLoaded,
	onLoadGenres,
	genreOptions,
	onSave,
	onBack,
}: {
	locale: "ru" | "en";
	record?: LocalProviderRecord;
	artists: readonly LocalProviderRecord[];
	genresLoaded: boolean;
	onLoadGenres?: () => void | Promise<void>;
	genreOptions?: readonly GenreOption[];
	onSave: (draft: LocalRecordingFormDraft) => void;
	onBack: () => void;
}) {
	const m = (key: LocalCatalogMessage) => localCatalogMessage(locale, key);
	const [title, setTitle] = useState(record?.title ?? "");
	const [duration, setDuration] = useState(
		formatLocalDuration(record?.duration_ms),
	);
	const [disambiguation, setDisambiguation] = useState(
		record?.disambiguation ?? "",
	);
	const [explicit, setExplicit] = useState(record?.explicit ?? "");
	const [isrcs, setIsrcs] = useState(record?.isrcs ?? "");
	const [tags, setTags] = useState(record?.tags ?? "");
	const [genres, setGenres] = useState<GenreReference[]>(() =>
		genreReferences(record),
	);
	const [artistIds, setArtistIds] = useState<string[]>(() => [
		...(record?.artistIds ?? []),
	]);
	const [badDuration, setBadDuration] = useState(false);
	function save() {
		if (!title.trim()) return;
		if (Number.isNaN(parseLocalDuration(duration))) {
			setBadDuration(true);
			return;
		}
		setBadDuration(false);
		onSave({
			title: title.trim(),
			durationInput: duration,
			disambiguation: disambiguation.trim(),
			explicit,
			isrcs,
			tags,
			genres,
			artistIds,
		});
	}
	return (
		<OwnCatalogRecordEditorView
			notice={m("formRequiredNotice")}
			help={m("durationHelp")}
			fields={[
				{
					label: `${m("titleField")} · ${m("required")}`,
					value: title,
					onChange: setTitle,
					required: true,
				},
				{
					label: `${m("durationField")} · ${m("optional")}`,
					value: duration,
					onChange: (value) => {
						setDuration(value);
						setBadDuration(false);
					},
					placeholder: "3:24",
				},
				{
					label: `${m("disambiguation")} · ${m("optional")}`,
					value: disambiguation,
					onChange: setDisambiguation,
				},
				{
					label: `${m("isrcCommaSeparated")} · ${m("optional")}`,
					value: isrcs,
					onChange: setIsrcs,
				},
				{
					label: `${m("tagsCommaSeparated")} · ${m("optional")}`,
					value: tags,
					onChange: setTags,
				},
			]}
			choice={{
				label: `${m("explicit")} · ${m("optional")}`,
				value: explicit,
				onChange: setExplicit,
				options: [
					{ id: "", label: m("unspecified") },
					{ id: "true", label: m("yes") },
					{ id: "false", label: m("no") },
				],
			}}
			fieldError={badDuration ? m("badDuration") : undefined}
			genre={
				<GenrePicker
					value={genres}
					onChange={setGenres}
					loaded={genresLoaded}
					onLoad={onLoadGenres}
					options={genreOptions}
				/>
			}
			artistLinks={{
				legend: m("artistLinks"),
				explanation: m("artistLinkExplanation"),
				artists: artists.map((artist) => ({
					id: artist.id,
					name: artist.name ?? artist.key,
					provider: artist.provider,
					providerKey: artist.key,
					externalNotice:
						artist.provider !== "Local" ? m("externalReadonly") : undefined,
				})),
				selectedIds: artistIds,
				onChange: (id, selected) =>
					setArtistIds((current) =>
						selected
							? [...current, id]
							: current.filter((artistId) => artistId !== id),
					),
			}}
			save={{
				label: m("save"),
				disabled: !title.trim(),
				onPress: save,
			}}
			cancel={{ label: m("cancel"), onPress: onBack }}
		/>
	);
}
