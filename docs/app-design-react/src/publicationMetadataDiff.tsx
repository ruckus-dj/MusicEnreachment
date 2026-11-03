import {
	effectivePublicationTags,
	type OriginalRelease,
	type OriginalTrack,
	type CatalogArtist,
} from "./catalogData";
import { navidromeFields } from "./metadata/registry";
import { useI18n } from "./i18n";
import {
	publicationFieldLabel,
	publicationGroupLabel,
	publicationMessage,
} from "./publicationMessages";

export type MetadataChange = {
	key: string;
	label: string;
	before: string[];
	after: string[];
};

export type MetadataChangeGroup = {
	key: string;
	label: string;
	changes: MetadataChange[];
};

const groupLabels: Record<string, string> = {
	"recording artists": "Исполнители композиции",
	"release artists": "Исполнители издания",
	roles: "Роли и участники",
	works: "Произведения",
	movement: "Части произведения",
	release: "Издание",
	recording: "Композиция",
	track: "Позиция",
	audio: "Аудио",
};

const displayValue = (value: string[], empty: string) =>
	value.length ? value.join(" · ") : empty;

export function metadataTagDiff(
	before: Record<string, string[]>,
	after: Record<string, string[]>,
): MetadataChangeGroup[] {
	const knownTags = navidromeFields.map((field) => field.tag);
	const extraTags = [
		...new Set([...Object.keys(before), ...Object.keys(after)]),
	]
		.filter((tag) => !knownTags.includes(tag))
		.sort((left, right) => left.localeCompare(right));
	const grouped = new Map<string, MetadataChange[]>();
	for (const tag of [...knownTags, ...extraTags]) {
		if (JSON.stringify(before[tag] ?? []) === JSON.stringify(after[tag] ?? []))
			continue;
		const field = navidromeFields.find((candidate) => candidate.tag === tag);
		const group = field?.group ?? "additional";
		const changes = grouped.get(group) ?? [];
		changes.push({
			key: tag,
			label: field?.label ?? tag,
			before: before[tag] ?? [],
			after: after[tag] ?? [],
		});
		grouped.set(group, changes);
	}
	return [...grouped].map(([key, changes]) => ({
		key,
		label:
			groupLabels[key] ?? (key === "additional" ? "Дополнительные теги" : key),
		changes,
	}));
}

export function publicationMetadataDiff(
	release: OriginalRelease,
	track: OriginalTrack,
	artists?: CatalogArtist[],
): MetadataChangeGroup[] {
	if (!track.published) return [];
	const beforeTags = track.metadataBaseline ?? track.published.tags;
	const afterTags = effectivePublicationTags(release, track, artists);
	const beforeExtras = track.metadataBaselineExtras ?? {
		coverHash: track.published.coverHash,
		lyrics: track.published.lyrics,
	};
	return metadataSnapshotDiff(beforeTags, afterTags, beforeExtras, {
		coverHash: track.coverHash ?? null,
		lyrics: track.lyrics ?? null,
	});
}

export function metadataSnapshotDiff(
	beforeTags: Record<string, string[]>,
	afterTags: Record<string, string[]>,
	beforeExtras: { coverHash: string | null; lyrics: string | null },
	afterExtras: { coverHash: string | null; lyrics: string | null },
): MetadataChangeGroup[] {
	const groups = metadataTagDiff(beforeTags, afterTags);
	const extraChanges: MetadataChange[] = [];
	if ((beforeExtras.coverHash ?? null) !== (afterExtras.coverHash ?? null))
		extraChanges.push({
			key: "cover",
			label: "Встроенная обложка",
			before: beforeExtras.coverHash
				? [afterExtras.coverHash ? "Есть · текущая" : "Есть"]
				: [],
			after: afterExtras.coverHash
				? [beforeExtras.coverHash ? "Есть · новая" : "Есть"]
				: [],
		});
	if ((beforeExtras.lyrics ?? null) !== (afterExtras.lyrics ?? null))
		extraChanges.push({
			key: "lyrics",
			label: "Текст песни",
			before: beforeExtras.lyrics ? [beforeExtras.lyrics] : [],
			after: afterExtras.lyrics ? [afterExtras.lyrics] : [],
		});
	const cover = extraChanges.filter((change) => change.key === "cover");
	const lyrics = extraChanges.filter((change) => change.key === "lyrics");
	if (cover.length)
		groups.push({ key: "artwork", label: "Обложка", changes: cover });
	if (lyrics.length)
		groups.push({ key: "lyrics", label: "Текст песни", changes: lyrics });
	return groups;
}

export function MetadataDiff({ groups }: { groups: MetadataChangeGroup[] }) {
	const { locale } = useI18n();
	const value = (change: MetadataChange, values: string[]) => {
		if (change.key === "cover")
			return (
				values
					.map((item) => {
						const key = {
							Есть: "artworkPresent",
							"Есть · текущая": "artworkCurrent",
							"Есть · новая": "artworkNew",
						}[item] as
							| "artworkPresent"
							| "artworkCurrent"
							| "artworkNew"
							| undefined;
						return key ? publicationMessage(locale, key) : item;
					})
					.join(" · ") || publicationMessage(locale, "noValue")
			);
		return displayValue(values, publicationMessage(locale, "noValue"));
	};
	if (!groups.length) return <p>{publicationMessage(locale, "noChanges")}</p>;
	return (
		<div
			className="grid max-h-48 min-w-0 gap-2 overflow-y-auto text-xs"
			aria-label={publicationMessage(locale, "changedFields")}
		>
			{groups.map((group) => (
				<section
					key={group.key}
					aria-label={publicationGroupLabel(locale, group.key, group.label)}
				>
					<h3 className="mb-1 mt-0 text-[11px] font-semibold">
						{publicationGroupLabel(locale, group.key, group.label)}
					</h3>
					<div className="grid grid-cols-[minmax(72px,.7fr)_minmax(0,1fr)_minmax(0,1fr)] gap-x-1 text-[10px] text-[var(--muted)]">
						<span>{publicationMessage(locale, "field")}</span>
						<span>{publicationMessage(locale, "current")}</span>
						<span>{publicationMessage(locale, "after")}</span>
					</div>
					{group.changes.map((change) => (
						<div
							key={change.key}
							className="grid min-w-0 grid-cols-[minmax(72px,.7fr)_minmax(0,1fr)_minmax(0,1fr)] gap-x-1 border-b border-[var(--line)]/70 py-1 last:border-0"
						>
							<strong
								className="truncate font-medium"
								title={publicationFieldLabel(locale, change.key, change.label)}
							>
								{publicationFieldLabel(locale, change.key, change.label)}
							</strong>
							{change.key === "lyrics" ? (
								<>
									<pre className="m-0 max-h-16 overflow-auto whitespace-pre-wrap break-words font-sans">
										{value(change, change.before)}
									</pre>
									<pre className="m-0 max-h-16 overflow-auto whitespace-pre-wrap break-words font-sans">
										{value(change, change.after)}
									</pre>
								</>
							) : (
								<>
									<span className="max-h-16 min-w-0 overflow-auto break-words">
										{value(change, change.before)}
									</span>
									<span className="max-h-16 min-w-0 overflow-auto break-words">
										{value(change, change.after)}
									</span>
								</>
							)}
						</div>
					))}
				</section>
			))}
		</div>
	);
}
