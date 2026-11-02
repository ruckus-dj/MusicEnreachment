import { useState } from "react";
import { Button, Dialog, Heading, Modal } from "react-aria-components";
import type {
	CatalogArtist,
	OriginalRelease,
	OriginalTrack,
} from "./catalogData";
import type { DeletionOperation } from "./DeletionRequest";

type DependencySection = { title: string; items: string[] };

type ArtistDeletionPlan = DeletionOperation & {
	dependencies: DependencySection[];
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
			title: "Релизы (будут удалены)",
			items: relatedReleases.map(
				(release) => release.title || "Релиз без названия",
			),
		},
		{
			title:
				"Композиции (напрямую связанные с артистом будут удалены; прочие сохраняются)",
			items: recordings.map(
				(track) =>
					`${track.title} · ${track.recordingId} · ${directRecordingIds.has(track.recordingId ?? "") ? "будет удалён" : "сохранится"}`,
			),
		},
		...relatedReleases.map((release) => ({
			title: `Позиции релиза «${release.title}» (будут удалены; включая неопубликованные)`,
			items: release.tracks.map(
				(track) =>
					`${String(track.position).padStart(2, "0")} · ${track.title}${track.published ? " · опубликована" : " · без публикации"}`,
			),
		})),
		{
			title:
				"Позиции удаляемых композиций в сохраняемых релизах (будут удалены)",
			items: affected
				.filter(
					({ release, track }) =>
						!relatedReleases.includes(release) &&
						directRecordingIds.has(track.recordingId ?? ""),
				)
				.map(
					({ release, track }) =>
						`${release.title} · ${String(track.position).padStart(2, "0")} · ${track.title}${track.published ? " · опубликована" : " · без публикации"}`,
				),
		},
	].filter((section) => section.items.length);

	return {
		target: artist.name,
		scope: `${relatedReleases.length} связанных релизов; ${directRecordingIds.size} напрямую связанных композиций; ${affectedTracks.size} позиций (включая позиции этих композиций в сохраняемых релизах). Остальные сущности медиатеки и все записи провайдера сохраняются.`,
		publications: publications.length,
		paths: publications.flatMap(({ track }) =>
			track.published ? [track.published.path] : [],
		),
		dependencies,
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
	const plan = artistDeletionPlan(artistId, artists, releases);
	if (!plan) return null;

	return (
		<>
			<Button onPress={() => setOpen(true)}>Удалить артиста</Button>
			<Modal isOpen={open} onOpenChange={setOpen} isDismissable>
				<Dialog aria-labelledby="artist-deletion-title">
					<Heading slot="title" id="artist-deletion-title">
						Удалить: {plan.target}?
					</Heading>
					<p>{plan.scope}</p>
					<p>
						Перед удалением строк будут безопасно удалены только файлы
						публикаций, принадлежащие приложению ({plan.publications}). После
						этого удаляются строки публикаций и указанные сущности медиатеки.
						Записи провайдера сохраняются; чужие/общие файлы не затрагиваются.
					</p>
					{plan.dependencies.map((section) => (
						<section key={section.title}>
							<h3>{section.title}</h3>
							<ul>
								{section.items.map((item) => (
									<li key={item}>{item}</li>
								))}
							</ul>
						</section>
					))}
					{plan.paths.length ? (
						<ul>
							{plan.paths.map((path) => (
								<li key={path}>
									<code>{path}</code>
								</li>
							))}
						</ul>
					) : (
						<p>Текущих публикаций в этой области нет.</p>
					)}
					<p>
						Асинхронная операция. Прототип только ставит демонстрационный запрос
						в список операций: реальные файлы и демонстрационные записи не
						удаляются.
					</p>
					<Button onPress={() => setOpen(false)}>Отмена</Button>
					<Button
						onPress={() => {
							setOpen(false);
							onQueue({
								target: plan.target,
								scope: plan.scope,
								publications: plan.publications,
								paths: plan.paths,
							});
						}}
					>
						Подтвердить
					</Button>
				</Dialog>
			</Modal>
		</>
	);
}
