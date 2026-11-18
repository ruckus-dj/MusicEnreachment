export type SourceFileSort = "track" | "name";

type TrackOrder = { discNumber?: number; trackNumber?: number };
type SourceFileOrder = TrackOrder & { name: string };

const discTags = [
	"DISCNUMBER",
	"DISC",
	"DISC NUMBER",
	"DISC_NUMBER",
	"PARTNUMBER",
];
const trackTags = ["TRACKNUMBER", "TRACK", "TRACK NUMBER", "TRACK_NUMBER"];

function readNumber(tags: ReadonlyMap<string, string[]>, keys: string[]) {
	for (const key of keys) {
		for (const rawValue of tags.get(key) ?? []) {
			const value = rawValue.trim();
			// Like matching, do not infer positions from compound tags.
			if (!/^\+?[0-9]+$/.test(value)) continue;
			const number = Number(value);
			if (Number.isSafeInteger(number) && number > 0) return number;
		}
	}
	return undefined;
}

export function readSourceTrackOrder(
	tags: Record<string, readonly string[]> = {},
): TrackOrder {
	const normalized = new Map<string, string[]>();
	for (const [rawKey, values] of Object.entries(tags)) {
		const key = rawKey.trim().toUpperCase();
		normalized.set(key, [...(normalized.get(key) ?? []), ...values]);
	}
	return {
		discNumber: readNumber(normalized, discTags),
		trackNumber: readNumber(normalized, trackTags),
	};
}

export function compareSourceFiles(
	a: SourceFileOrder,
	b: SourceFileOrder,
	mode: SourceFileSort,
	collator: Intl.Collator,
) {
	if (mode === "track") {
		if (a.trackNumber !== undefined && b.trackNumber !== undefined) {
			const positionOrder =
				(a.discNumber ?? 1) - (b.discNumber ?? 1) ||
				a.trackNumber - b.trackNumber;
			if (positionOrder) return positionOrder;
		} else if (a.trackNumber !== undefined || b.trackNumber !== undefined) {
			return a.trackNumber === undefined ? 1 : -1;
		}
	}
	return collator.compare(a.name, b.name);
}
