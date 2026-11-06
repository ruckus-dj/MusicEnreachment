import type { CatalogArtist } from "../catalogData";

export type ArtistProjectionCredit = {
	artistId: string;
	creditName: string;
	joinPhrase?: string;
};

export type ArtistProjection = {
	names: string[];
	mbids: string[];
	missingMbid: boolean;
	nameCollision: boolean;
};

/** Projects one ordered artist group without changing catalog records. */
export function projectArtistCreditGroup(
	credits: ArtistProjectionCredit[],
	artists: CatalogArtist[],
): ArtistProjection {
	const linked = credits.map((credit) => ({
		...credit,
		artist: artists.find((artist) => artist.id === credit.artistId),
	}));
	const nameCounts = new Map<string, Set<string>>();
	for (const { artist } of linked) {
		if (!artist) continue;
		const ids = nameCounts.get(artist.name) ?? new Set<string>();
		ids.add(artist.id);
		nameCounts.set(artist.name, ids);
	}
	const creditNameCounts = new Map<string, Set<string>>();
	for (const { artist, creditName } of linked) {
		if (!artist) continue;
		const ids = creditNameCounts.get(creditName) ?? new Set<string>();
		ids.add(artist.id);
		creditNameCounts.set(creditName, ids);
	}
	const names = linked.map(({ artist, creditName }) => {
		if (
			!artist ||
			((nameCounts.get(artist.name)?.size ?? 0) < 2 &&
				(creditNameCounts.get(creditName)?.size ?? 0) < 2)
		)
			return creditName;
		return artist.disambiguation
			? `${creditName} (${artist.disambiguation})`
			: creditName;
	});
	const identityByProjectedName = new Map<string, string>();
	const nameCollision = names.some((name, index) => {
		const artist = linked[index].artist;
		if (!artist) return false;
		const priorIdentity = identityByProjectedName.get(name);
		if (priorIdentity && priorIdentity !== artist.id) return true;
		identityByProjectedName.set(name, artist.id);
		return false;
	});
	const mbids = linked.map(({ artist }) => artist?.mbid ?? "");
	return {
		names,
		mbids,
		missingMbid: linked.length > 0 && mbids.some((mbid) => !mbid),
		nameCollision,
	};
}
