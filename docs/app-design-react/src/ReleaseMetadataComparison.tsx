import { useState } from "react";
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
	comparisonFilter,
	comparisonNote,
	comparisonRow,
	comparisonRowHeading,
	metadataPanel,
	metadataPanelHeading,
	previewTable,
	previewTableCell,
	previewTableHeading,
	previewTableWrap,
} from "./matchingStyles";
import { navidromeFields } from "./metadata/registry";

type Scope = "artist" | "release" | "track";
const artistGroups = new Set(["recording artists", "release artists"]);
const fieldsForScope = (scope: Scope) =>
	navidromeFields.filter((field) => {
		if (scope === "artist") return artistGroups.has(field.group);
		if (scope === "release") return field.scope === "release";
		return (
			field.scope === "track" ||
			field.scope === "audio" ||
			(field.scope === "recording" && !artistGroups.has(field.group))
		);
	});

export function ReleaseMetadataComparison({
	release,
	artists,
	files,
	showAllFields = false,
}: {
	release: OriginalRelease;
	artists?: CatalogArtist[];
	files: SourceFiles;
	showAllFields?: boolean;
}) {
	const [trackId, setTrackId] = useState(release.tracks[0]?.id ?? "");
	const [scope, setScope] = useState<Scope>("release");
	const [differencesOnly, setDifferencesOnly] = useState(false);
	const [allFields, setAllFields] = useState(false);
	const [fileOverride, setFileOverride] = useState<string | null | undefined>(
		undefined,
	);
	const track =
		release.tracks.find((item) => item.id === trackId) ?? release.tracks[0];
	if (!track) return null;
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
	const rows = (allFields ? navidromeFields : fieldsForScope(scope))
		.map((field) => {
			const key = field.tag;
			const values = [
				sourceTags[key] ?? [],
				providerTags?.[key] ?? null,
				localTags[key] ?? [],
				track.published ? (publicationTags[key] ?? []) : null,
			];
			const distinct = new Set(
				values.map((value) => JSON.stringify(value ?? [])),
			);
			return { key, label: field.label, values, different: distinct.size > 1 };
		})
		.filter((row) => !differencesOnly || row.different);
	const scopes: [Scope, string][] = [
		["artist", "Артист"],
		["release", "Альбом"],
		["track", "Трек"],
	];
	return (
		<section className={metadataPanel} aria-label="Сравнение метаданных">
			<h2 className={metadataPanelHeading}>Сравнение метаданных</h2>
			{release.manual && (
				<p>
					Тип: {release.metadata?.type || "—"} · Исполнители:{" "}
					{release.credits
						?.map((credit) => credit.credit || credit.name)
						.filter(Boolean)
						.join(", ") || "не указаны"}
				</p>
			)}
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
							{release.tracks.map((item) => (
								<ListBoxItem key={item.id} id={item.id}>
									{item.title}
								</ListBoxItem>
							))}
						</ListBox>
					</Popover>
				</Select>
				<Select
					aria-label="Исходный файл сравнения"
					selectedKey={fileId ?? ""}
					onSelectionChange={(key) => setFileOverride(String(key) || null)}
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
			</div>
			<div className={comparisonFilter} aria-label="Область тегов">
				<Tabs
					selectedKey={scope}
					onSelectionChange={(key) => setScope(key as Scope)}
				>
					<TabList aria-label="Область тегов">
						{scopes.map(([id, label]) => (
							<Tab key={id} id={id}>
								{label}
							</Tab>
						))}
					</TabList>
				</Tabs>
				<Checkbox isSelected={differencesOnly} onChange={setDifferencesOnly}>
					<span className="checkbox" />
					Только отличия
				</Checkbox>
				{showAllFields && (
					<Checkbox isSelected={allFields} onChange={setAllFields}>
						<span className="checkbox" />
						Все поля
					</Checkbox>
				)}
			</div>
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
			<a href={`#tags/${track.id}/release`}>
				Открыть подробное сравнение альбома
			</a>
		</section>
	);
}
