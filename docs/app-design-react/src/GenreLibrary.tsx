import { useMemo, useState } from "react";
import { formStyles as styles } from "./formStyles";
import { useI18n } from "./i18n";
import { genreCatalog } from "./genres/catalogue";
import { genreMessages } from "./genres/genreMessages";
import { Button, Input, SearchField, StatusBadge } from "./ui";

export { genreCatalog as demoGenres } from "./genres/catalogue";
export type { Genre } from "./genres/catalogue";

export function GenreLibrary({
	loaded,
	onLoadedChange,
	buttonLabel,
}: {
	loaded: boolean;
	onLoadedChange: (loaded: boolean) => void;
	buttonLabel?: string;
}) {
	const [query, setQuery] = useState("");
	const { locale } = useI18n();
	const copy = genreMessages[locale];
	const normalizedQuery = query.trim().toLocaleLowerCase("en");
	const filteredGenres = useMemo(
		() =>
			genreCatalog.filter((genre) => {
				if (!normalizedQuery) return true;
				return [genre.name, genre.sourceName, genre.id].some((value) =>
					value.toLocaleLowerCase("en").includes(normalizedQuery),
				);
			}),
		[normalizedQuery],
	);

	return (
		<section className="grid min-w-0 gap-3 rounded-md border border-[var(--line)] bg-[var(--raised)] p-3">
			<div className="grid gap-1">
				<div className="flex flex-wrap items-center gap-2">
					<strong>{copy.heading}</strong>
					<StatusBadge tone="success">{copy.offline}</StatusBadge>
					<StatusBadge tone={loaded ? "success" : "info"}>
						{loaded ? copy.loaded : copy.notLoaded}
					</StatusBadge>
				</div>
				<p className="m-0 text-xs text-[var(--muted)]">{copy.description}</p>
			</div>

			<div className="flex flex-wrap items-end gap-2">
				<SearchField
					aria-label={copy.search}
					className="min-w-[min(100%,260px)] flex-1"
					value={query}
					onChange={setQuery}
				>
					<Input placeholder={copy.searchPlaceholder} />
				</SearchField>
				<Button
					className={styles.genreLibraryButton}
					onPress={() => onLoadedChange(true)}
				>
					{buttonLabel ?? copy.update}
				</Button>
			</div>

			<p className="m-0 text-xs" role="status" aria-live="polite">
				{copy.resultCount}:{" "}
				{new Intl.NumberFormat(locale).format(filteredGenres.length)}
			</p>
			{filteredGenres.length ? (
				<ul
					aria-label={copy.results}
					className="m-0 grid max-h-[min(50dvh,420px)] grid-cols-[repeat(auto-fill,minmax(min(100%,210px),1fr))] list-none overflow-auto overscroll-contain rounded border border-[var(--line)] bg-[var(--surface)] p-1"
				>
					{filteredGenres.map((genre) => (
						<li
							key={genre.id}
							className="min-w-0 rounded px-3 py-2 hover:bg-[var(--raised)]"
							title={`${genre.sourceName} · ${genre.id}`}
						>
							<span className="block truncate text-sm">{genre.name}</span>
						</li>
					))}
				</ul>
			) : (
				<p className="m-0 rounded border border-dashed border-[var(--line)] p-4 text-sm text-[var(--muted)]">
					{copy.noResults}
				</p>
			)}
		</section>
	);
}
