import { useState } from "react";
import { Button, Dialog, Heading, Modal } from "./ui";
import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";
import { deletionDialog, deletionFooter } from "./matchingStyles";
import { useI18n } from "./i18n/I18nProvider";
import { catalogueMessage, type CatalogueMessage } from "./catalogueMessages";

type DependencySection = {
	title: CatalogueMessage;
	releaseTitle?: string;
	items: {
		label: string;
		position?: number;
		published?: boolean;
		state?: "deleted" | "kept";
	}[];
};

type ArtistDeletionPlan = DeletionOperation & {
	dependencies: DependencySection[];
	summary: { releases: number; recordings: number; positions: number };
};

export function artistDeletionPlan(
	artistId: string,
	artists: CatalogArtist[],
	releases: OriginalRelease[],
): ArtistDeletionPlan | null {
	const artist = artists.find((item) => item.id === artistId);
	if (!artist) return null;

	const relatedReleases = releases.filter(
		(release) =>
			release.artistId === artistId ||
			release.credits?.some((credit) => credit.artistId === artistId),
	);
	const allTracks = releases.flatMap((release) =>
		release.tracks.map((track) => ({ release, track })),
	);
	const directRecordingIds = new Set(
		allTracks
			.filter(({ track }) => track.artistId === artistId)
			.map(({ track }) => track.recordingId)
			.filter((id): id is string => Boolean(id)),
	);
	const affectedTracks = new Map<
		string,
		{ release: OriginalRelease; track: OriginalTrack }
	>();
	for (const release of relatedReleases)
		for (const track of release.tracks)
			affectedTracks.set(track.id, { release, track });
	for (const item of allTracks)
		if (
			item.track.recordingId &&
			directRecordingIds.has(item.track.recordingId)
		)
			affectedTracks.set(item.track.id, item);

	const affected = [...affectedTracks.values()];
	const publications = affected.filter(({ track }) => track.published);
	const recordings = [
		...new Map(
			affected
				.filter(({ track }) => track.recordingId)
				.map(({ track }) => [track.recordingId, track] as const),
		).values(),
	];
	const dependencies: DependencySection[] = [
		{
			title: "artistDeletionRelatedReleases",
			items: relatedReleases.map((release) => ({ label: release.title })),
		},
		{
			title: "artistDeletionRecordings",
			items: recordings.map((track) => ({
				label: `${track.title} · ${track.recordingId}`,
				state: directRecordingIds.has(track.recordingId ?? "")
					? "deleted"
					: "kept",
			})),
		},
		...relatedReleases.map((release) => ({
			title: "releasePositionsWillBeDeleted" as const,
			releaseTitle: release.title,
			items: release.tracks.map((track) => ({
				label: track.title,
				position: track.position,
				published: Boolean(track.published),
			})),
		})),
		{
			title: "artistDeletionPositionsInSavedReleases",
			items: affected
				.filter(
					({ release, track }) =>
						!relatedReleases.includes(release) &&
						directRecordingIds.has(track.recordingId ?? ""),
				)
				.map(({ release, track }) => ({
					label: `${release.title} · ${track.title}`,
					position: track.position,
					published: Boolean(track.published),
				})),
		},
	] satisfies DependencySection[];
	const visibleDependencies = dependencies.filter(
		(section) => section.items.length,
	);

	return {
		kind: "artist",
		libraryArtistId: artistId,
		libraryReleaseIds: relatedReleases.map((release) => release.id),
		libraryTrackIds: affected.map(({ track }) => track.id),
		target: artist.name,
		scope: `${relatedReleases.length} связанных релизов; ${directRecordingIds.size} напрямую связанных композиций; ${affectedTracks.size} позиций (включая позиции этих композиций в сохраняемых релизах). Остальные сущности медиатеки и все записи провайдера сохраняются.`,
		publications: publications.length,
		paths: publications.flatMap(({ track }) =>
			track.published ? [track.published.path] : [],
		),
		dependencies: visibleDependencies,
		summary: {
			releases: relatedReleases.length,
			recordings: directRecordingIds.size,
			positions: affectedTracks.size,
		},
	};
}

export function ArtistDeletionRequest({
	artistId,
	artists,
	releases,
	onQueue,
}: {
	artistId: string;
	artists: CatalogArtist[];
	releases: OriginalRelease[];
	onQueue: (operation: DeletionOperation) => void;
}) {
	const [open, setOpen] = useState(false);
	const { locale } = useI18n();
	const m = (key: CatalogueMessage, values?: Record<string, string | number>) =>
		catalogueMessage(locale, key, values);
	const plan = artistDeletionPlan(artistId, artists, releases);
	if (!plan) return null;

	return (
		<>
			<Button
				className="border-[color-mix(in_srgb,var(--error)_45%,var(--control))]! bg-transparent! text-[var(--error)]!"
				onPress={() => setOpen(true)}
			>
				{m("deleteArtist")}
			</Button>
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog
					aria-labelledby="artist-deletion-title"
					className={deletionDialog}
				>
					<Heading slot="title" id="artist-deletion-title">
						{m("deleteQuestion", { target: plan.target })}
					</Heading>
					<p>
						{m("artistDeletionSummary", {
							releases: plan.summary.releases,
							recordings: plan.summary.recordings,
							positions: plan.summary.positions,
						})}
					</p>
					<p>{m("artistDeletionSafety", { count: plan.publications })}</p>
					{plan.dependencies.map((section) => (
						<section key={`${section.title}:${section.releaseTitle ?? ""}`}>
							<h3 className="mt-1.5 mb-0 text-xs">
								{m(section.title)}
								{section.releaseTitle ? ` · ${section.releaseTitle}` : ""}
							</h3>
							<ul className="m-0 pl-5">
								{section.items.map((item, index) => (
									<li
										className="my-[3px] [overflow-wrap:anywhere]"
										key={`${item.label}:${index}`}
									>
										{item.position === undefined
											? item.label || m("unnamedRelease")
											: `${item.position.toString().padStart(2, "0")} · ${item.label}`}
										{item.published !== undefined &&
											` · ${m(item.published ? "published" : "unpublished")}`}
										{item.state &&
											` · ${m(item.state === "deleted" ? "willBeDeleted" : "willBeKept")}`}
									</li>
								))}
							</ul>
						</section>
					))}
					{plan.paths.length ? (
						<ul className="m-0 pl-5">
							{plan.paths.map((path) => (
								<li className="my-[3px] [overflow-wrap:anywhere]" key={path}>
									<code>{path}</code>
								</li>
							))}
						</ul>
					) : (
						<p>{m("noCurrentPublications")}</p>
					)}
					<p>{m("deletionRequestNotice")}</p>
					<div className={deletionFooter}>
						<Button onPress={() => setOpen(false)}>{m("cancel")}</Button>
						<Button
							className="border-[var(--error)]! text-[var(--error)]!"
							onPress={() => {
								setOpen(false);
								onQueue({
									libraryArtistId: plan.libraryArtistId,
									libraryReleaseIds: plan.libraryReleaseIds,
									libraryTrackIds: plan.libraryTrackIds,
									kind: "artist",
									target: plan.target,
									scope: plan.scope,
									publications: plan.publications,
									paths: plan.paths,
								});
							}}
						>
							{m("confirm")}
						</Button>
					</div>
				</Dialog>
			</Modal>
		</>
	);
}
