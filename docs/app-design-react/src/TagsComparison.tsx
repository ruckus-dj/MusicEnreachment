import { useEffect, useState } from "react";
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
import {
	comparisonControls,
	comparisonDifference,
	comparisonNote,
	comparisonRow,
	comparisonRowHeading,
	metadataPanel,
	objectActions,
	previewTable,
	previewTableCell,
	previewTableHeading,
	previewTableWrap,
} from "./matchingStyles";
import { navidromeFields } from "./metadata/registry";

export type TagScope = "artist" | "release" | "track";

const artistGroups = new Set(["recording artists", "release artists"]);
const fieldsForTab = (tab: TagScope) =>
	navidromeFields.filter((field) => {
		if (tab === "artist") return artistGroups.has(field.group);
		if (tab === "release") return field.scope === "release";
		return (
			field.scope === "track" ||
			field.scope === "audio" ||
			(field.scope === "recording" && !artistGroups.has(field.group))
		);
	});

export function TagsComparison({
	releases,
	artists,
	files,
	scope,
	initialTrackId,
	onPreview,
	onSelectFile,
	allowScopeChange = false,
	showAllFields = false,
}: {
	releases: OriginalRelease[];
	artists?: CatalogArtist[];
	files: SourceFiles;
	scope: TagScope;
	initialTrackId?: string;
	onPreview?: (ids: string[]) => void;
	onSelectFile?: (trackId: string, fileId: string) => void;
	allowScopeChange?: boolean;
	showAllFields?: boolean;
}) {
	const tracks = releases.flatMap((release) =>
		release.tracks.map((track) => ({ release, track })),
	);
	const [trackId, setTrackId] = useState(
		initialTrackId ?? tracks[0]?.track.id ?? "",
	);
	const [fileOverride, setFileOverride] = useState<string | null | undefined>();
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const [allFields, setAllFields] = useState(false);
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
	const rows = (allFields ? navidromeFields : fieldsForTab(activeScope))
		.map((field) => {
			const key = field.tag;
			const values = [
				sourceTags[key] ?? [],
				providerTags?.[key] ?? null,
				localTags[key] ?? [],
				track.published ? (publicationTags[key] ?? []) : null,
			];
			return {
				key,
				label: field.label,
				values,
				different:
					new Set(values.map((value) => JSON.stringify(value ?? []))).size > 1,
			};
		})
		.filter((row) => !differencesOnly || row.different);
	return (
		<section
			className={metadataPanel}
			aria-label={`Сравнение тегов: ${activeScope}`}
		>
			<div className={comparisonControls}>
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
				<span className="block text-[11px] text-[var(--muted)]">
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
			{showAllFields && (
				<Checkbox isSelected={allFields} onChange={setAllFields}>
					<span className="checkbox" />
					Все поля
				</Checkbox>
			)}
			<div className={previewTableWrap}>
				<table className={`${previewTable} min-w-[720px]`}>
					<thead>
						<tr>
							{[
								"Поле",
								"Исходный файл",
								"Провайдер · кэш",
								"Локальная медиатека",
								"Сейчас в публикации",
							].map((heading) => (
								<th className={previewTableHeading} scope="col" key={heading}>
									{heading}
								</th>
							))}
						</tr>
					</thead>
					<tbody>
						{rows.map(({ key, label, values, different }) => (
							<tr className={comparisonRow} key={key}>
								<th
									scope="row"
									className={`${comparisonRowHeading} ${different ? comparisonDifference : ""}`}
								>
									{label} · {key}
								</th>
								{values.map((value, index) => (
									<td className={previewTableCell} key={`${key}:${index}`}>
										{value === null ? (
											<span className="block text-[11px] text-[var(--muted)]">
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
			<p className={comparisonNote}>
				{file?.path ?? "Исходник не выбран"}
				<br />
				{track.published
					? `Публикация: ${track.published.path} · чтение ${track.published.readAt} (демо)`
					: "Публикация отсутствует; пустая колонка не подменяется текущими метаданными."}
			</p>
			<div className={objectActions}>
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
