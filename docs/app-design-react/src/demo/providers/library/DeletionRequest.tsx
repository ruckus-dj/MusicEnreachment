import { useState } from "react";
import { useI18n } from "../../../app/i18n/host";
import {
	type CatalogueMessage,
	catalogueMessage,
} from "../../../ui/features/library/catalogueMessages";
import {
	type LibraryDeletionCoartistWarningView,
	LibraryDeletionControlView,
	type LibraryDeletionDialogModel,
	type LibraryDeletionOutputRowView,
	type LibraryDeletionPreviewModel,
} from "../../../ui/features/library/LibraryDeletionControlView";
import type { Locale } from "../../../ui/i18n/types";
import type { OriginalRelease, OriginalTrack } from "./catalogData";

export interface DeletionOperation {
	kind?: "library" | "publication" | "artist";
	target: string;
	scope: string;
	publications: number;
	paths: string[];
	libraryReleaseIds?: string[];
	libraryTrackIds?: string[];
	libraryArtistId?: string;
}

type DeletionPreviewInput = {
	locale: Locale;
	title: string;
	positions: number;
	files: number;
	recordings?: number;
	outputs: readonly LibraryDeletionOutputRowView[];
	positionRows?: { id: string; label: string }[];
	artistReleases?: LibraryDeletionPreviewModel["artistReleases"];
	coartistWarnings?: LibraryDeletionPreviewModel["coartistWarnings"];
};

/** Shared demo preview projection for the library deletion control. */
export function buildDeletionPreview({
	locale,
	title,
	positions,
	files,
	recordings,
	outputs,
	positionRows,
	artistReleases,
	coartistWarnings,
}: DeletionPreviewInput): LibraryDeletionPreviewModel {
	const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
		catalogueMessage(locale, key, values);
	return {
		title,
		artistReleases,
		coartistWarnings,
		positions: positionRows?.length
			? { title: m("deletionPositions"), items: positionRows }
			: undefined,
		facts: [
			{
				id: "positions",
				label: m("deletionPositions"),
				value: positions.toLocaleString(locale),
			},
			{
				id: "files",
				label: m("deletionFiles"),
				value: files.toLocaleString(locale),
			},
			...(recordings === undefined
				? []
				: [
						{
							id: "recordings",
							label: m("deletionRecordings"),
							value: recordings.toLocaleString(locale),
						},
					]),
		],
		outputs,
		frozen: m("deletionPreviewFrozen"),
	};
}

/** Shared demo dialog actions; confirmation dispatches the frozen operation. */
export function buildDeletionDialog({
	locale,
	targetLabel,
	scope,
	open,
	onOpenChange,
	onConfirm,
	preview,
}: {
	locale: Locale;
	targetLabel: string;
	scope: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
	onConfirm: () => void;
	preview?: LibraryDeletionPreviewModel;
}): LibraryDeletionDialogModel {
	const m = (key: CatalogueMessage) => catalogueMessage(locale, key);
	return {
		targetLabel,
		open,
		onOpenChange,
		dismissable: true,
		actions: [
			{
				id: "cancel",
				label: m("cancel"),
				onPress: () => onOpenChange(false),
			},
			{
				id: "confirm",
				label: m("confirm"),
				danger: true,
				onPress: onConfirm,
			},
		],
		scope,
		preview,
	};
}

export function artistCoartistWarnings(
	artistId: string,
	artistName: string,
	releases: OriginalRelease[],
	artists: { id: string; name: string }[],
): LibraryDeletionCoartistWarningView[] {
	const nameFor = (id: string | null | undefined) =>
		id ? artists.find((artist) => artist.id === id)?.name : undefined;
	return releases.flatMap((release) => {
		const names = new Set<string>();
		for (const credit of release.credits ?? []) {
			if (credit.artistId === artistId) continue;
			const name =
				credit.credit || credit.name || nameFor(credit.artistId) || undefined;
			if (!name || name === artistName) continue;
			names.add(name);
		}
		for (const track of release.tracks) {
			if (!track.artistId || track.artistId === artistId) continue;
			const name = nameFor(track.artistId);
			if (name) names.add(name);
		}
		if (!names.size) return [];
		return [
			{
				id: release.id,
				releaseTitle: release.title,
				artistNames: [...names],
			},
		];
	});
}

export function trackLabel(release: OriginalRelease, track: OriginalTrack) {
	return `${release.title} · ${String(track.position).padStart(2, "0")} · ${track.title}`;
}

export function publicationOutputs(
	releases: OriginalRelease[],
	paths: readonly string[],
	trackId?: string,
): LibraryDeletionOutputRowView[] {
	return releases.flatMap((release) =>
		release.tracks.flatMap((track) =>
			(trackId === undefined || track.id === trackId) &&
			track.published &&
			paths.includes(track.published.path)
				? [
						{
							id: `${release.id}:${track.id}:${track.published.path}`,
							path: track.published.path,
							label: trackLabel(release, track),
						},
					]
				: [],
		),
	);
}

export function publicationSelectionPlan(
	tracks: OriginalTrack[],
	releases: OriginalRelease[],
): DeletionOperation {
	const titles = tracks.map((track) => {
		const release = releases.find((item) =>
			item.tracks.some((candidate) => candidate.id === track.id),
		);
		return `${release?.title ?? "Релиз не найден"} · ${String(track.position).padStart(2, "0")} · ${track.title}`;
	});
	return {
		kind: "publication",
		libraryTrackIds: tracks
			.filter((track) => track.published)
			.map((track) => track.id),
		target: `${tracks.length} выбранных публикаций`,
		scope: `Только текущие публикации выбранных позиций (${titles.join("; ")}); позиции медиатеки, композиции, записи провайдера, исходники и другие публикации сохраняются.`,
		publications: tracks.filter((track) => track.published).length,
		paths: tracks.flatMap((track) =>
			track.published ? [track.published.path] : [],
		),
	};
}

export function deletionPlan(
	release: OriginalRelease,
	track?: OriginalTrack,
): DeletionOperation {
	const tracks = track ? [track] : release.tracks;
	return {
		kind: track ? "publication" : "library",
		libraryReleaseIds: track ? [] : [release.id],
		libraryTrackIds: tracks.map((item) => item.id),
		target: track
			? `${release.title} · ${String(track.position).padStart(2, "0")} · ${track.title}`
			: release.title || "Релиз без названия",
		scope: track
			? "Только выбранная публикация; позиция, издание, композиция, записи провайдера и другие публикации сохраняются."
			: `${release.tracks.length} позиций релиза и композиции, потерявшие последнюю связь с релизом. Композиции и публикации других релизов, записи провайдера и исходники сохраняются.`,
		publications: tracks.filter((item) => item.published).length,
		paths: tracks.flatMap((item) =>
			item.published ? [item.published.path] : [],
		),
	};
}

type FrozenDeletion = {
	plan: DeletionOperation;
	release: OriginalRelease;
	track?: OriginalTrack;
};

export function DeletionRequest({
	release,
	track,
	onQueue,
	iconOnly,
	triggerLabel,
}: {
	release: OriginalRelease;
	track?: OriginalTrack;
	onQueue: (operation: DeletionOperation) => void;
	iconOnly?: boolean;
	triggerLabel?: string;
}) {
	const { locale } = useI18n();
	const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
		catalogueMessage(locale, key, values);
	const [frozen, setFrozen] = useState<FrozenDeletion | null>(null);
	const open = frozen !== null;
	const shownRelease = frozen?.release ?? release;
	const shownTrack = frozen?.track ?? track;
	const displayedTarget = shownTrack
		? `${shownRelease.title} · ${String(shownTrack.position).padStart(2, "0")} · ${shownTrack.title}`
		: shownRelease.title || m("unnamedRelease");
	const displayedScope = shownTrack
		? m("deletePublicationScope")
		: m("deleteReleaseScope", { count: shownRelease.tracks.length });
	const outputs =
		frozen === null
			? []
			: publicationOutputs([shownRelease], frozen.plan.paths, shownTrack?.id);
	const preview =
		frozen === null
			? undefined
			: buildDeletionPreview({
					locale,
					title: frozen.plan.target,
					positions: shownTrack ? 1 : shownRelease.tracks.length,
					files: outputs.length,
					outputs,
					positionRows: shownTrack
						? undefined
						: shownRelease.tracks.map((item) => ({
								id: item.id,
								label: trackLabel(shownRelease, item),
							})),
				});
	const dialog = buildDeletionDialog({
		locale,
		targetLabel: displayedTarget,
		scope: displayedScope,
		open,
		onOpenChange: (nextOpen) => {
			if (!nextOpen) setFrozen(null);
		},
		onConfirm: () => {
			const confirmed = frozen;
			setFrozen(null);
			if (confirmed) onQueue(confirmed.plan);
		},
		preview,
	});
	return (
		<LibraryDeletionControlView
			locale={locale}
			kind={track ? "publication" : "release"}
			trigger={{
				label: m(track ? "deletePublication" : "deleteAlbum"),
				iconLabel: iconOnly ? (triggerLabel ?? m("deleteAlbum")) : undefined,
				onPress: () =>
					setFrozen({
						plan: deletionPlan(release, track),
						release,
						track,
					}),
			}}
			dialog={dialog}
		/>
	);
}
