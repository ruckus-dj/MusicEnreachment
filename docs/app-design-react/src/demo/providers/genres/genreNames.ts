const protectedAcronyms = new Map([
	["edm", "EDM"],
	["idm", "IDM"],
	["ebm", "EBM"],
	["uk", "UK"],
	["us", "US"],
	["aor", "AOR"],
	["nwobhm", "NWOBHM"],
	["nrg", "NRG"],
]);

const genreWordPattern = /[\p{L}\p{N}]+(?:['’][\p{L}\p{N}]+)*/gu;

/**
 * Applies conservative display capitalization without rewriting separators or
 * inferring genre aliases. This is a readable display form, not a canonical
 * genre taxonomy.
 */
export function normalizeGenreName(raw: string): string {
	return raw.replace(genreWordPattern, (word) => {
		const acronym = protectedAcronyms.get(word.toLocaleLowerCase("en"));
		if (acronym) return acronym;

		const firstCharacter = Array.from(word)[0];
		return `${firstCharacter.toLocaleUpperCase("en")}${word
			.slice(firstCharacter.length)
			.toLocaleLowerCase("en")}`;
	});
}

export type GenreName = {
	id: string;
	name: string;
};

export type NormalizedGenre = {
	id: string;
	sourceName: string;
	name: string;
};

/** Keeps the source spelling and stable identity alongside its display name. */
export function normalizeGenre(genre: GenreName): NormalizedGenre {
	return {
		id: genre.id,
		sourceName: genre.name,
		name: normalizeGenreName(genre.name),
	};
}
