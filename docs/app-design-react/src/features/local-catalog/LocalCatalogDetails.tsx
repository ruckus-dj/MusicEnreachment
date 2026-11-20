import { Button } from "react-aria-components";
import type {
	LocalProviderRecord,
	LocalProviderState,
} from "../../localProviderModel";
import { formatLocalDuration } from "../local-release/localReleaseFormModel";
import { muted, panel, primary, rows } from "../local-release/localReleaseUi";
import { LocalCatalogEditorShell } from "./LocalCatalogEditorShell";
import {
	type LocalCatalogMessage,
	localCatalogMessage,
} from "./localCatalogMessage";
import {
	type LocalCatalogType,
	localCatalogArtistNames,
	localCatalogGenreNames,
	localCatalogRecordingTitle,
	localCatalogReleaseTitle,
} from "./localCatalogModel";

function Fact({ label, value }: { label: string; value: string }) {
	return (
		<div className="border-b border-[var(--line)] py-[9px]">
			<dt className="text-[var(--muted)]">{label}</dt>
			<dd className="m-0 wrap-anywhere">{value || "—"}</dd>
		</div>
	);
}

export function LocalCatalogDetails({
	locale,
	state,
	type,
	record,
	onBack,
	onEdit,
}: {
	locale: "ru" | "en";
	state: LocalProviderState;
	type: LocalCatalogType;
	record: LocalProviderRecord;
	onBack: () => void;
	onEdit: () => void;
}) {
	const m = (key: LocalCatalogMessage) => localCatalogMessage(locale, key);
	const title =
		type === "artists"
			? record.name || "—"
			: type === "recordings"
				? localCatalogRecordingTitle(record) || "—"
				: localCatalogReleaseTitle(record) || "—";
	const genres = localCatalogGenreNames(record).join(", ");
	const artists = localCatalogArtistNames(state, record.artistIds).join(", ");
	const relatedReleases = state.items.releases.filter((release) =>
		release.artistIds?.includes(record.id),
	);
	const usedInReleases = state.items.releases.filter((release) =>
		release.tracks?.some((track) => track.recordingId === record.id),
	);
	return (
		<LocalCatalogEditorShell
			eyebrow={m("currentDataNoHistory")}
			title={title}
			backLabel={m("back")}
			onBack={onBack}
			actions={
				<Button className={primary} onPress={onEdit}>
					{m("edit")}
				</Button>
			}
		>
			<section className={`${panel} grid gap-3`}>
				<dl className="m-0 grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-3">
					<Fact label={m("providerKey")} value={record.key} />
					{type === "artists" && (
						<>
							<Fact label={m("name")} value={record.name ?? ""} />
							<Fact label={m("sortName")} value={record.sortName ?? ""} />
							<Fact
								label={m("relatedReleases")}
								value={
									relatedReleases.length
										? relatedReleases
												.map((release) => localCatalogReleaseTitle(release))
												.join(", ")
										: m("noLinkedReleases")
								}
							/>
						</>
					)}
					{type === "recordings" && (
						<>
							<Fact label={m("recordingArtists")} value={artists} />
							<Fact
								label={m("duration")}
								value={formatLocalDuration(record.duration_ms)}
							/>
							<Fact label={m("explicit")} value={record.explicit ?? ""} />
							<Fact label={m("isrc")} value={record.isrcs ?? ""} />
							<Fact
								label={m("recordingUsedIn")}
								value={
									usedInReleases
										.map((release) => localCatalogReleaseTitle(release))
										.join(", ") || "—"
								}
							/>
						</>
					)}
					{type === "releases" && (
						<>
							<Fact
								label={m("releaseGroup")}
								value={record.release_group_title ?? ""}
							/>
							<Fact label={m("releaseArtists")} value={artists} />
							<Fact label={m("primaryType")} value={record.primaryType ?? ""} />
							<Fact label={m("language")} value={record.language ?? ""} />
							<Fact label={m("script")} value={record.script ?? ""} />
							<Fact
								label={m("releaseDate")}
								value={record.release_date ?? ""}
							/>
							<Fact label={m("country")} value={record.country_code ?? ""} />
							<Fact label={m("format")} value={record.format ?? ""} />
							<Fact label={m("label")} value={record.label ?? ""} />
							<Fact
								label={m("catalogNumber")}
								value={record.catalog_number ?? ""}
							/>
						</>
					)}
					<Fact
						label={m("disambiguation")}
						value={record.disambiguation ?? ""}
					/>
					<Fact label={m("genres")} value={genres} />
					<Fact label={m("tags")} value={record.tags ?? ""} />
				</dl>
				{type === "releases" && (
					<div>
						<div className="flex flex-wrap items-center justify-between gap-2 border-b border-[var(--line)] pb-2">
							<h2 className="m-0 text-base font-semibold">
								{m("tracklist")} · {m("positions")}
							</h2>
						</div>
						{record.tracks?.length ? (
							<ol className={`${rows} mt-2`}>
								{record.tracks.map((track, index) => {
									const recording = state.items.recordings.find(
										(item) => item.id === track.recordingId,
									);
									const trackTitle =
										track.titleOverride ||
										(recording
											? localCatalogRecordingTitle(recording)
											: m("recordingNotSelected"));
									const duration =
										track.durationOverrideMs === "" ||
										track.durationOverrideMs == null
											? formatLocalDuration(recording?.duration_ms)
											: formatLocalDuration(track.durationOverrideMs);
									return (
										<li
											className="flex min-w-0 items-baseline gap-2 py-2"
											key={track.id || `${index}`}
										>
											<span className="w-6 shrink-0 text-right text-xs text-[var(--muted)] tabular-nums">
												{index + 1}.
											</span>
											<div className="min-w-0">
												<strong>{trackTitle}</strong>
												<p className={muted}>
													{[recording?.provider, duration]
														.filter(Boolean)
														.join(" · ")}
												</p>
											</div>
										</li>
									);
								})}
							</ol>
						) : (
							<p className={muted}>{m("noPositions")}</p>
						)}
					</div>
				)}
			</section>
		</LocalCatalogEditorShell>
	);
}
