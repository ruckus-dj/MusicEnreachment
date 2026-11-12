import type { Locale } from "../i18n/types";
import {
	type MetadataMessageKey,
	metadataFieldMessageKey,
	metadataMessage,
	metadataMessageKeyFor,
} from "./messages";

export type MetadataScope = "release" | "recording" | "track" | "audio";

export type MetadataKind =
	| "text"
	| "list"
	| "date"
	| "integer"
	| "number"
	| "boolean"
	| "advisory"
	| "lyrics"
	| "identity"
	| "credits"
	| "contributors"
	| "works"
	| "movement"
	| "genres";

export interface MetadataField {
	key: string;
	label: string;
	scope: MetadataScope;
	kind: MetadataKind;
	tag: string;
	group: string;
	derived?: boolean;
	role?: string;
}

type Definition = [
	key: string,
	label: string,
	scope: MetadataScope,
	kind: MetadataKind,
	tag?: string,
	group?: string,
	role?: string,
	derived?: boolean,
];

const definitions: Definition[] = [
	["title", "Название", "recording", "text"],
	["titlesort", "Название для сортировки", "recording", "text"],
	[
		"artist",
		"Исполнители",
		"recording",
		"credits",
		"ARTIST",
		"recording artists",
	],
	[
		"artists",
		"Исполнители по отдельности",
		"recording",
		"credits",
		"ARTISTS",
		"recording artists",
	],
	[
		"artistsort",
		"Сортировка исполнителей",
		"recording",
		"list",
		"ARTISTSORT",
		"recording artists",
		undefined,
		true,
	],
	[
		"artistssort",
		"Сортировка исполнителей по отдельности",
		"recording",
		"list",
		"ARTISTSSORT",
		"recording artists",
		undefined,
		true,
	],
	[
		"albumartist",
		"Исполнители издания",
		"release",
		"credits",
		"ALBUMARTIST",
		"release artists",
	],
	[
		"albumartists",
		"Исполнители издания по отдельности",
		"release",
		"credits",
		"ALBUMARTISTS",
		"release artists",
	],
	[
		"albumartistsort",
		"Сортировка исполнителей издания",
		"release",
		"list",
		"ALBUMARTISTSORT",
		"release artists",
		undefined,
		true,
	],
	[
		"albumartistssort",
		"Сортировка исполнителей издания по отдельности",
		"release",
		"list",
		"ALBUMARTISTSSORT",
		"release artists",
		undefined,
		true,
	],
	["album", "Издание", "release", "text"],
	["albumsort", "Издание для сортировки", "release", "text"],
	[
		"arranger",
		"Аранжировщик",
		"recording",
		"contributors",
		"ARRANGER",
		"roles",
		"arranger",
	],
	[
		"composer",
		"Композитор",
		"recording",
		"contributors",
		"COMPOSER",
		"roles",
		"composer",
	],
	[
		"composersort",
		"Сортировка композиторов",
		"recording",
		"list",
		"COMPOSERSORT",
		"roles",
		"composer",
	],
	[
		"lyricist",
		"Автор текста",
		"recording",
		"contributors",
		"LYRICIST",
		"roles",
		"lyricist",
	],
	[
		"lyricistsort",
		"Сортировка авторов текста",
		"recording",
		"list",
		"LYRICISTSORT",
		"roles",
		"lyricist",
	],
	[
		"conductor",
		"Дирижёр",
		"recording",
		"contributors",
		"CONDUCTOR",
		"roles",
		"conductor",
	],
	[
		"director",
		"Режиссёр",
		"recording",
		"contributors",
		"DIRECTOR",
		"roles",
		"director",
	],
	[
		"djmixer",
		"DJ-микшер",
		"recording",
		"contributors",
		"DJMIXER",
		"roles",
		"djmixer",
	],
	[
		"mixer",
		"Звукорежиссёр сведения",
		"recording",
		"contributors",
		"MIXER",
		"roles",
		"mixer",
	],
	[
		"engineer",
		"Инженер",
		"recording",
		"contributors",
		"ENGINEER",
		"roles",
		"engineer",
	],
	[
		"producer",
		"Продюсер",
		"recording",
		"contributors",
		"PRODUCER",
		"roles",
		"producer",
	],
	[
		"remixer",
		"Ремиксер",
		"recording",
		"contributors",
		"REMIXER",
		"roles",
		"remixer",
	],
	[
		"performer",
		"Исполнитель / инструмент",
		"recording",
		"contributors",
		"PERFORMER",
		"roles",
		"performer",
	],
	["albumversion", "Версия издания", "release", "text"],
	["genre", "Жанры", "recording", "genres"],
	["mood", "Настроение", "recording", "list"],
	["compilation", "Сборник", "release", "boolean"],
	["track", "Номер позиции", "track", "integer", "TRACKNUMBER"],
	["tracktotal", "Всего позиций", "release", "integer"],
	["disc", "Номер носителя", "track", "integer", "DISCNUMBER"],
	["disctotal", "Всего носителей", "release", "integer"],
	["discsubtitle", "Название носителя", "release", "text"],
	["bpm", "Темп", "recording", "number"],
	["lyrics", "Текст песни", "recording", "lyrics"],
	["comment", "Комментарий", "recording", "text"],
	[
		"originaldate",
		"Дата первоначального выпуска",
		"recording",
		"date",
		"ORIGINALDATE",
	],
	["recordingdate", "Дата записи", "recording", "date", "DATE"],
	["releasedate", "Дата выпуска", "release", "date", "RELEASEDATE"],
	["catalognumber", "Каталожный номер", "release", "list"],
	["releasetype", "Тип выпуска", "release", "list"],
	[
		"musicbrainz_artistid",
		"MBID исполнителей",
		"recording",
		"identity",
		"MUSICBRAINZ_ARTISTID",
		"recording artists",
	],
	[
		"musicbrainz_albumartistid",
		"MBID исполнителей издания",
		"release",
		"identity",
		"MUSICBRAINZ_ALBUMARTISTID",
		"release artists",
	],
	[
		"musicbrainz_recordingid",
		"MBID записи",
		"recording",
		"identity",
		"MUSICBRAINZ_TRACKID",
	],
	[
		"musicbrainz_trackid",
		"MBID позиции выпуска",
		"track",
		"identity",
		"MUSICBRAINZ_RELEASETRACKID",
	],
	["musicbrainz_albumid", "MBID издания", "release", "identity"],
	["musicbrainz_releasegroupid", "MBID группы выпусков", "release", "identity"],
	[
		"musicbrainz_composerid",
		"MBID композиторов",
		"recording",
		"identity",
		"MUSICBRAINZ_COMPOSERID",
		"roles",
		"composer",
	],
	[
		"musicbrainz_lyricistid",
		"MBID авторов текста",
		"recording",
		"identity",
		"MUSICBRAINZ_LYRICISTID",
		"roles",
		"lyricist",
	],
	[
		"musicbrainz_directorid",
		"MBID режиссёров",
		"recording",
		"identity",
		"MUSICBRAINZ_DIRECTORID",
		"roles",
		"director",
	],
	[
		"musicbrainz_producerid",
		"MBID продюсеров",
		"recording",
		"identity",
		"MUSICBRAINZ_PRODUCERID",
		"roles",
		"producer",
	],
	[
		"musicbrainz_engineerid",
		"MBID инженеров",
		"recording",
		"identity",
		"MUSICBRAINZ_ENGINEERID",
		"roles",
		"engineer",
	],
	[
		"musicbrainz_mixerid",
		"MBID микшеров",
		"recording",
		"identity",
		"MUSICBRAINZ_MIXERID",
		"roles",
		"mixer",
	],
	[
		"musicbrainz_remixerid",
		"MBID ремиксеров",
		"recording",
		"identity",
		"MUSICBRAINZ_REMIXERID",
		"roles",
		"remixer",
	],
	[
		"musicbrainz_djmixerid",
		"MBID DJ-микшеров",
		"recording",
		"identity",
		"MUSICBRAINZ_DJMIXERID",
		"roles",
		"djmixer",
	],
	[
		"musicbrainz_conductorid",
		"MBID дирижёров",
		"recording",
		"identity",
		"MUSICBRAINZ_CONDUCTORID",
		"roles",
		"conductor",
	],
	[
		"musicbrainz_arrangerid",
		"MBID аранжировщиков",
		"recording",
		"identity",
		"MUSICBRAINZ_ARRANGERID",
		"roles",
		"arranger",
	],
	[
		"musicbrainz_performerid",
		"MBID исполнителей",
		"recording",
		"identity",
		"MUSICBRAINZ_PERFORMERID",
		"roles",
		"performer",
	],
	["replaygain_album_gain", "ReplayGain альбома", "audio", "number"],
	["replaygain_album_peak", "Пик ReplayGain альбома", "audio", "number"],
	["replaygain_track_gain", "ReplayGain трека", "audio", "number"],
	["replaygain_track_peak", "Пик ReplayGain трека", "audio", "number"],
	["r128_album_gain", "R128 альбома", "audio", "number"],
	["r128_track_gain", "R128 трека", "audio", "number"],
	[
		"explicitstatus",
		"Explicit / clean",
		"recording",
		"advisory",
		"ITUNESADVISORY",
	],
	["asin", "ASIN", "release", "identity"],
	["barcode", "Штрихкод", "release", "text"],
	["copyright", "Авторские права", "release", "text"],
	["grouping", "Группировка", "recording", "text"],
	["key", "Тональность", "recording", "text"],
	["isrc", "ISRC", "recording", "list"],
	["language", "Язык", "recording", "list"],
	["license", "Лицензия", "recording", "text"],
	["media", "Носитель издания", "release", "list"],
	[
		"movementname",
		"Название части",
		"recording",
		"movement",
		"MOVEMENTNAME",
		"movement",
	],
	[
		"movementtotal",
		"Всего частей",
		"recording",
		"movement",
		"MOVEMENTTOTAL",
		"movement",
	],
	["movement", "Номер части", "recording", "movement", "MOVEMENT", "movement"],
	["recordlabel", "Лейбл", "release", "text", "LABEL"],
	[
		"musicbrainz_workid",
		"MBID произведения",
		"recording",
		"works",
		"MUSICBRAINZ_WORKID",
		"works",
	],
	["releasecountry", "Страна выпуска", "release", "text", "RELEASECOUNTRY"],
	["releasestatus", "Статус выпуска", "release", "text"],
	["script", "Система письма", "release", "text"],
	["subtitle", "Подзаголовок", "recording", "text"],
	["website", "Веб-сайт", "recording", "list"],
	["work", "Произведения", "recording", "works", "WORK", "works"],
];

export const navidromeFields: MetadataField[] = definitions.map(
	([key, label, scope, kind, tag, group, role, derived]) => ({
		key,
		label,
		scope,
		kind,
		tag: tag ?? key.toUpperCase(),
		group: group ?? scope,
		...(role ? { role } : {}),
		...(derived ? { derived } : {}),
	}),
);

const byKey = new Map(navidromeFields.map((field) => [field.key, field]));

export function fieldsForScope(scope: MetadataScope): MetadataField[] {
	return navidromeFields.filter((field) => field.scope === scope);
}

export function fieldByKey(key: string): MetadataField | undefined {
	return byKey.get(key);
}

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
