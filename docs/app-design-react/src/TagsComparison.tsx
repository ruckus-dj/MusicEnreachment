import { useEffect, useState } from "react";
import "./matching-publication.css";
import {
	Button,
	Checkbox,
	Label,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
	Tab,
	TabList,
	Tabs,
} from "./ui";
import {
	desiredTags,
	originalProviders,
	type CatalogArtist,
	type OriginalRelease,
	type SourceFiles,
} from "./catalogData";

export type TagScope = "artist" | "release" | "track";

const fields: Record<TagScope, string[]> = {
	artist: ["ARTIST", "ARTISTSORT", "MUSICBRAINZ_ARTISTID"],
	release: [
		"ALBUM",
		"ALBUMARTIST",
		"DATE",
		"COUNTRY",
		"LABEL",
		"CATALOGNUMBER",
		"MUSICBRAINZ_ALBUMID",
	],
	track: [
		"TITLE",
		"ARTIST",
		"TRACKNUMBER",
		"DISCNUMBER",
		"GENRE",
		"MUSICBRAINZ_TRACKID",
		"COMMENT",
	],
};

export function TagsComparison({
	releases,
	artists,
	files,
	scope,
	initialTrackId,
	onPreview,
	onSelectFile,
	allowScopeChange = false,
}: {
	releases: OriginalRelease[];
	artists?: CatalogArtist[];
	files: SourceFiles;
	scope: TagScope;
	initialTrackId?: string;
	onPreview?: (ids: string[]) => void;
	onSelectFile?: (trackId: string, fileId: string) => void;
	allowScopeChange?: boolean;
}) {
	const tracks = releases.flatMap((release) =>
		release.tracks.map((track) => ({ release, track })),
	);
	const [trackId, setTrackId] = useState(
		initialTrackId ?? tracks[0]?.track.id ?? "",
	);
	const [fileOverride, setFileOverride] = useState<string | null | undefined>();
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const [activeScope, setActiveScope] = useState<TagScope>(scope);
	useEffect(() => setActiveScope(scope), [scope]);
	const selected =
		tracks.find(({ track }) => track.id === trackId) ??
		(initialTrackId ? undefined : tracks[0]);
	if (!selected) return <p>Позиции отсутствуют.</p>;
	const { release, track } = selected;
	const linkedFileIds = [
		...new Set([
			...(track.sourceIds ?? []),
			...(track.sourceId ? [track.sourceId] : []),
		]),
	];
	const fileId =
		fileOverride === undefined
			? track.sourceId
			: fileOverride && linkedFileIds.includes(fileOverride)
				? fileOverride
				: null;
	const file = fileId ? files[fileId] : undefined;
	const provider = originalProviders
		.flatMap((item) =>
			item.tracks.map((candidate) => ({ release: item, track: candidate })),
		)
		.find(({ track: candidate }) => candidate.id === track.providerTrackId);
	const sourceTags = file?.tags ?? {};
	const providerTags = provider
		? desiredTags(provider.release, provider.track, artists)
		: null;
	const localTags = desiredTags(release, track, artists);
	const publicationTags = track.published?.tags ?? {};
	const rows = fields[activeScope]
		.map((key) => {
			const values = [
				sourceTags[key] ?? [],
				providerTags?.[key] ?? null,
				localTags[key] ?? [],
				track.published ? (publicationTags[key] ?? []) : null,
			];
			return {
				key,
				values,
				different:
					new Set(values.map((value) => JSON.stringify(value ?? []))).size > 1,
			};
		})
		.filter((row) => !differencesOnly || row.different);
	return (
		<section
			className="demo-panel metadata-comparison"
			aria-label={`Сравнение тегов: ${activeScope}`}
		>
			<div className="demo-object-actions comparison-controls">
				<Select
					aria-label="Позиция сравнения"
					selectedKey={track.id}
					onSelectionChange={(key) => {
						setTrackId(String(key));
						setFileOverride(undefined);
					}}
				>
					<Label>Позиция</Label>
					<Button>
						<SelectValue />
						<span aria-hidden="true">⌄</span>
					</Button>
					<Popover>
						<ListBox>
							{tracks.map(({ release: itemRelease, track: item }) => (
								<ListBoxItem key={item.id} id={item.id}>
									{String(item.position).padStart(2, "0")} · {item.title} ·{" "}
									{itemRelease.title}
								</ListBoxItem>
							))}
						</ListBox>
					</Popover>
				</Select>
				<Select
					aria-label="Исходный файл для сравнения"
					selectedKey={fileId ?? ""}
					onSelectionChange={(key) => {
						const value = String(key);
						setFileOverride(value || null);
						if (value) onSelectFile?.(track.id, value);
					}}
				>
					<Label>Исходный файл</Label>
					<Button>
						<SelectValue />
						<span aria-hidden="true">⌄</span>
					</Button>
					<Popover>
						<ListBox>
							<ListBoxItem id="">Исходник не выбран</ListBoxItem>
							{linkedFileIds.map(
								(id) =>
									files[id] && (
										<ListBoxItem key={id} id={id}>
											{files[id].name}
										</ListBoxItem>
									),
							)}
						</ListBox>
					</Popover>
				</Select>
				<span className="demo-secondary">
					Кэш провайдера: 26.09.2026 10:20 · идентификаторы демонстрационные
				</span>
			</div>
			{allowScopeChange && (
				<Tabs
					selectedKey={activeScope}
					onSelectionChange={(key) => setActiveScope(key as TagScope)}
				>
					<TabList aria-label="Область тегов">
						{(
							[
								["artist", "Артист"],
								["release", "Альбом"],
								["track", "Трек"],
							] as const
						).map(([key, label]) => (
							<Tab key={key} id={key}>
								{label}
							</Tab>
						))}
					</TabList>
				</Tabs>
			)}
			<div className="demo-preview-table">
				<table>
					<thead>
						<tr>
							{[
								"Поле",
								"Исходный файл",
								"Провайдер · кэш",
								"Локальная медиатека",
								"Сейчас в публикации",
							].map((heading) => (
								<th scope="col" key={heading}>
									{heading}
								</th>
							))}
						</tr>
					</thead>
					<tbody>
						{rows.map(({ key, values, different }) => (
							<tr key={key}>
								<th scope="row" className={different ? "difference" : ""}>
									{key}
								</th>
								{values.map((value, index) => (
									<td key={`${key}:${index}`}>
										{value === null ? (
											<span className="demo-secondary">
												{index === 1 ? "Нет связи" : "Не опубликован"}
											</span>
										) : value.length ? (
											value.map((item, itemIndex) => (
												<span key={`${itemIndex}:${item}`}>
													{itemIndex > 0 && <br />}
													{item}
												</span>
											))
										) : (
											"—"
										)}
									</td>
								))}
							</tr>
						))}
					</tbody>
				</table>
			</div>
			<p className="demo-secondary">
				{file?.path ?? "Исходник не выбран"}
				<br />
				{track.published
					? `Публикация: ${track.published.path} · чтение ${track.published.readAt} (демо)`
					: "Публикация отсутствует; пустая колонка не подменяется текущими метаданными."}
			</p>
			<div className="demo-object-actions">
				<Checkbox isSelected={differencesOnly} onChange={setDifferencesOnly}>
					<span className="checkbox" />
					Только отличия
				</Checkbox>
				{onPreview && (
					<Button onPress={() => onPreview([track.id])}>
						Предпросмотр публикации
					</Button>
				)}
			</div>
		</section>
	);
}
