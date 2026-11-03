import sourceGenres from "./musicbrainzCatalog.json";
import { normalizeGenre } from "./genreNames";
import type { NormalizedGenre } from "./genreNames";

export type Genre = NormalizedGenre;

/** The bundled MusicBrainz snapshot with conservative English display names. */
export const genreCatalog: Genre[] = sourceGenres.map(normalizeGenre);

const genreById = new Map(genreCatalog.map((genre) => [genre.id, genre]));
const genreBySourceName = new Map<string, Genre>();
const genreByDisplayName = new Map<string, Genre>();
for (const genre of genreCatalog) {
	genreBySourceName.set(genre.sourceName, genre);
	genreByDisplayName.set(genre.name, genre);
}

/** Resolves only exact catalog identities/names; unknown values are untouched. */
export function resolveCatalogGenreName(reference: {
	id?: string;
	name?: string;
}): string {
	const genreByIdentity = reference.id
		? genreById.get(reference.id)
		: undefined;
	const genreByExactName = reference.name
		? (genreBySourceName.get(reference.name) ??
			genreByDisplayName.get(reference.name))
		: undefined;
	const genre = genreByIdentity ?? genreByExactName;
	return genre?.name ?? reference.name ?? "";
}

/** Normalizes an exact known catalog name without rewriting arbitrary metadata. */
export function resolveCatalogGenreValue(raw: string): string {
	return (
		genreBySourceName.get(raw)?.name ?? genreByDisplayName.get(raw)?.name ?? raw
	);
}
