import {
	type MetadataMessageKey,
	metadataFieldMessageKey,
	metadataMessage,
	metadataMessageKeyFor,
} from "../../../../ui/features/library/metadata/metadataFieldMessages";
import type { Locale } from "../../../../ui/i18n/types";

export type {
	MetadataField,
	MetadataKind,
	MetadataScope,
} from "../../../../ui/features/library/libraryTagComparisonFields";
export {
	fieldByKey,
	fieldsForScope,
	navidromeFields,
} from "../../../../ui/features/library/libraryTagComparisonFields";

/** Registry labels are presentation only; stable field keys and tag identities never vary by locale. */
export function metadataFieldLabel(key: string, locale: Locale): string {
	const messageKey = metadataFieldMessageKey(key);
	return messageKey ? metadataMessage(locale, messageKey) : key;
}

const groupMessageKeys = {
	release: "group.release",
	recording: "group.recording",
	track: "group.track",
	audio: "group.audio",
	"recording artists": "group.recordingArtists",
	"release artists": "group.releaseArtists",
	roles: "group.roles",
	identifiers: "group.identifiers",
	works: "group.works",
	movement: "group.movement",
} as const satisfies Record<string, MetadataMessageKey>;

export function metadataGroupLabel(group: string, locale: Locale): string {
	const key = metadataMessageKeyFor(groupMessageKeys, group);
	return key ? metadataMessage(locale, key) : group;
}
