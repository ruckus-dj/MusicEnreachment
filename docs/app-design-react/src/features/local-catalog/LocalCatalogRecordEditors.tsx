import { useState } from "react";
import { Button } from "react-aria-components";
import type { LocalProviderRecord } from "../../localProviderModel";
import { GenrePicker, type GenreReference } from "../../metadata/GenrePicker";
import {
	Checkbox,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
	Button as UiButton,
} from "../../ui";
import {
	formatLocalDuration,
	parseLocalDuration,
} from "../local-release/localReleaseFormModel";
import {
	actions,
	errorClass,
	Field,
	muted,
	panel,
	primary,
	secondary,
} from "../local-release/localReleaseUi";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
import type {
	LocalArtistFormDraft,
	LocalRecordingFormDraft,
} from "./localCatalogModel";

type GenreOption = { id: string; name: string };

function genreReferences(record: LocalProviderRecord | undefined) {
	return (record?.genres ?? []).map((id) => ({
		id,
		name: record?.genreNamesById?.[id] ?? id,
	}));
}

function ArtistGenres({
	genres,
	onGenres,
	genresLoaded,
	onLoadGenres,
	genreOptions,
}: {
	genres: GenreReference[];
	onGenres: (next: GenreReference[]) => void;
	genresLoaded: boolean;
	onLoadGenres?: () => void | Promise<void>;
	genreOptions?: readonly GenreOption[];
}) {
	return (
		<GenrePicker
			value={genres}
			onChange={onGenres}
			loaded={genresLoaded}
			onLoad={onLoadGenres}
			options={genreOptions}
		/>
	);
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
		<section className={`${panel} grid gap-3`}>
			<p className={muted}>{m("formRequiredNotice")}</p>
			<div className="grid gap-2 sm:grid-cols-2">
				<Field
					label={`${m("name")} · ${m("required")}`}
					value={name}
					onChange={setName}
					required
				/>
				<Field
					label={`${m("sortName")} · ${m("optional")}`}
					value={sortName}
					onChange={setSortName}
				/>
				<Field
					label={`${m("disambiguation")} · ${m("optional")}`}
					value={disambiguation}
					onChange={setDisambiguation}
				/>
				<Field
					label={`${m("tagsCommaSeparated")} · ${m("optional")}`}
					value={tags}
					onChange={setTags}
				/>
			</div>
			<ArtistGenres
				genres={genres}
				onGenres={setGenres}
				genresLoaded={genresLoaded}
				onLoadGenres={onLoadGenres}
				genreOptions={genreOptions}
			/>
			<div className={actions}>
				<Button
					className={primary}
					isDisabled={!name.trim()}
					onPress={() =>
						onSave({
							name: name.trim(),
							sortName: sortName.trim(),
							disambiguation: disambiguation.trim(),
							tags,
							genres,
						})
					}
				>
					{m("save")}
				</Button>
				<Button className={secondary} onPress={onBack}>
					{m("cancel")}
				</Button>
			</div>
		</section>
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
		<section className={`${panel} grid gap-3`}>
			<p className={muted}>{m("formRequiredNotice")}</p>
			<div className="grid gap-2 sm:grid-cols-2">
				<Field
					label={`${m("titleField")} · ${m("required")}`}
					value={title}
					onChange={setTitle}
					required
				/>
				<Field
					label={`${m("durationField")} · ${m("optional")}`}
					value={duration}
					onChange={(value) => {
						setDuration(value);
						setBadDuration(false);
					}}
					placeholder="3:24"
				/>
				<Field
					label={`${m("disambiguation")} · ${m("optional")}`}
					value={disambiguation}
					onChange={setDisambiguation}
				/>
				<Field
					label={`${m("isrcCommaSeparated")} · ${m("optional")}`}
					value={isrcs}
					onChange={setIsrcs}
				/>
				<Field
					label={`${m("tagsCommaSeparated")} · ${m("optional")}`}
					value={tags}
					onChange={setTags}
				/>
				<div className="grid gap-1 text-xs font-medium">
					<Select
						selectedKey={explicit}
						onSelectionChange={(key) => setExplicit(String(key ?? ""))}
					>
						<Label>
							{m("explicit")} · {m("optional")}
						</Label>
						<UiButton>
							<SelectValue />
						</UiButton>
						<Popover>
							<ListBox>
								<ListBoxItem id="">{m("unspecified")}</ListBoxItem>
								<ListBoxItem id="true">{m("yes")}</ListBoxItem>
								<ListBoxItem id="false">{m("no")}</ListBoxItem>
							</ListBox>
						</Popover>
					</Select>
				</div>
			</div>
			<p className={muted}>{m("durationHelp")}</p>
			{badDuration && (
				<p className={errorClass} role="alert">
					{m("badDuration")}
				</p>
			)}
			<ArtistGenres
				genres={genres}
				onGenres={setGenres}
				genresLoaded={genresLoaded}
				onLoadGenres={onLoadGenres}
				genreOptions={genreOptions}
			/>
			<fieldset className="m-0 grid min-w-0 gap-1 border-0 p-0">
				<legend className="mb-1 p-0 text-xs font-semibold">
					{m("artistLinks")}
				</legend>
				<p className={muted}>{m("artistLinkExplanation")}</p>
				<div className="flex flex-wrap gap-2">
					{artists.map((artist) => (
						<div className="flex items-center gap-1" key={artist.id}>
							<Checkbox
								isSelected={artistIds.includes(artist.id)}
								onChange={(selected) =>
									setArtistIds((current) =>
										selected
											? [...current, artist.id]
											: current.filter((id) => id !== artist.id),
									)
								}
							>
								{artist.name}
							</Checkbox>
							<span className={muted}>
								· {artist.provider}
								{artist.provider !== "Local"
									? ` · ${m("externalReadonly")}`
									: ""}{" "}
								· <code>{artist.key}</code>
							</span>
						</div>
					))}
				</div>
			</fieldset>
			<div className={actions}>
				<Button className={primary} isDisabled={!title.trim()} onPress={save}>
					{m("save")}
				</Button>
				<Button className={secondary} onPress={onBack}>
					{m("cancel")}
				</Button>
			</div>
		</section>
	);
}
