import { type ReactNode, useEffect, useRef, useState } from "react";
import { Button, Checkbox } from "react-aria-components";
import { LocalRecordingArtistField } from "./LocalRecordingArtistField";
import { LocalReleaseRecordingPicker } from "./LocalReleaseRecordingPicker";
import {
	type FindLocalArtists,
	formatLocalCredits,
	formatLocalDuration,
	type LocalRecordingDraft,
	type LocalReleaseArtist,
	type LocalReleaseGenre,
	type LocalReleaseRecording,
	parseLocalDuration,
	type SaveLocalArtist,
} from "./localReleaseFormModel";
import { localReleaseMessages } from "./localReleaseMessages";
import {
	actions,
	errorClass,
	Field,
	muted,
	panel,
	primary,
	row,
	rows,
	secondary,
} from "./localReleaseUi";
import type { LocalReleaseFormController } from "./useLocalReleaseForm";

export type LocalReleaseGenreFieldProps = {
	locale: "ru" | "en";
	value: readonly LocalReleaseGenre[];
	onChange: (next: LocalReleaseGenre[]) => void;
	disabled?: boolean;
};

export type RenderLocalReleaseGenreField = (
	props: LocalReleaseGenreFieldProps,
) => ReactNode;

export type LocalReleaseFormProps = {
	locale: "ru" | "en";
	form: LocalReleaseFormController;
	heading: string;
	titleReadOnly?: boolean;
	onRecheck?: () => void;
	suggestedArtist?: string;
	artists: readonly LocalReleaseArtist[];
	recordings: readonly LocalReleaseRecording[];
	findRecordings: (
		query: string,
		signal?: AbortSignal,
	) => Promise<readonly LocalReleaseRecording[]>;
	saveRecording: (
		data: LocalRecordingDraft,
	) => LocalReleaseRecording | Promise<LocalReleaseRecording>;
	findArtists: FindLocalArtists;
	saveArtist: SaveLocalArtist;
	renderGenreField: RenderLocalReleaseGenreField;
	saveLabel?: string;
};

export function LocalReleaseForm({
	locale,
	form,
	heading,
	titleReadOnly = false,
	onRecheck,
	suggestedArtist = "",
	artists,
	recordings,
	findRecordings,
	saveRecording,
	findArtists,
	saveArtist,
	renderGenreField,
	saveLabel,
}: LocalReleaseFormProps) {
	const copy = localReleaseMessages[locale];
	const headingRef = useRef<HTMLHeadingElement>(null);
	const addButton = useRef<HTMLButtonElement>(null);
	const replaceButton = useRef<HTMLButtonElement | null>(null);
	const [replaceId, setReplaceId] = useState<string>();
	const [savingArtist, setSavingArtist] = useState(false);
	const pickerOpen = Boolean(form.picker || replaceId);
	const rememberArtist = (artist: LocalReleaseArtist) =>
		form.setSavedArtists((current) =>
			current.some((item) => item.id === artist.id)
				? current
				: [...current, artist],
		);
	const closePicker = () => {
		const trigger = replaceId ? replaceButton.current : addButton.current;
		setReplaceId(undefined);
		form.setPicker(false);
		requestAnimationFrame(() => trigger?.focus());
	};
	useEffect(() => headingRef.current?.focus(), []);
	const rosterArtists = [
		...artists,
		...form.savedArtists.filter(
			(artist) => !artists.some((item) => item.id === artist.id),
		),
	];
	const suggested = suggestedArtist.trim();
	const rosterIncludesSuggested =
		!suggested ||
		rosterArtists.some(
			(item) =>
				(item.name ?? "").trim().toLowerCase() === suggested.toLowerCase(),
		);
	return (
		<>
			<section
				aria-labelledby="local-release-edit"
				className={`${panel} grid gap-3`}
			>
				<h2
					id="local-release-edit"
					ref={headingRef}
					tabIndex={-1}
					className="m-0 text-base font-semibold"
				>
					{heading}
				</h2>
				<div className="flex flex-wrap items-end gap-2">
					<div className="min-w-0 flex-1">
						<Field
							label={copy.release}
							value={form.title}
							onChange={form.setTitle}
							required={!titleReadOnly}
							readOnly={titleReadOnly}
							disabled={form.savingRelease}
						/>
					</div>
					{onRecheck && (
						<Button
							className={secondary}
							isDisabled={form.savingRelease || form.writingCatalogRecord}
							onPress={onRecheck}
						>
							{copy.recheck}
						</Button>
					)}
				</div>
				{!titleReadOnly ? (
					<LocalRecordingArtistField
						copy={copy}
						scope="release"
						findArtists={findArtists}
						saveArtist={saveArtist}
						credits={form.artistIds.map((artistId) => ({
							artistId,
							creditName:
								rosterArtists.find((artist) => artist.id === artistId)?.name ??
								artistId,
						}))}
						saved={form.savedArtists}
						busy={form.savingRelease}
						onCreditsChange={(credits) =>
							form.setArtistIds(credits.map((credit) => credit.artistId))
						}
						onSavingChange={setSavingArtist}
						onSaved={rememberArtist}
					/>
				) : (
					<fieldset className="m-0 grid min-w-0 gap-1 border-0 p-0">
						<legend className="mb-1 p-0 text-xs font-semibold">
							{copy.artists}
						</legend>
						<p className={muted}>{copy.artistHelp}</p>
						{suggested && !rosterIncludesSuggested && (
							<p className={muted}>
								{copy.suggestedArtistContext} <strong>{suggested}</strong>
							</p>
						)}
						<div className="flex flex-wrap gap-2">
							{rosterArtists.map((item) => (
								<Checkbox
									className="flex cursor-pointer items-center gap-1.5 rounded border border-[var(--control)] px-2 py-1 text-xs wrap-anywhere data-[selected]:border-[var(--accent)] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]"
									key={item.id}
									isSelected={form.artistIds.includes(item.id)}
									isDisabled={form.savingRelease}
									onChange={(selected) =>
										form.setArtistIds((current) =>
											selected
												? [...current, item.id]
												: current.filter((id) => id !== item.id),
										)
									}
								>
									{item.name} · {item.provider}
								</Checkbox>
							))}
						</div>
					</fieldset>
				)}
				<details className="border-t border-[var(--line)] pt-2">
					<summary className="min-h-8 w-fit cursor-pointer py-1 text-sm font-semibold">
						{copy.details}
					</summary>
					<div className="grid gap-2 pt-2 sm:grid-cols-2">
						<Field
							label={copy.edition}
							value={form.edition}
							onChange={form.setEdition}
							placeholder={form.title}
							disabled={form.savingRelease}
						/>
						<label className="grid gap-1 text-xs font-medium">
							{copy.type}
							<select
								className="min-h-8 rounded border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)]"
								value={form.primaryType}
								disabled={form.savingRelease}
								onChange={(event) => form.setPrimaryType(event.target.value)}
							>
								<option value="">—</option>
								{["Album", "Single", "EP", "Broadcast", "Other"].map((type) => (
									<option key={type}>{type}</option>
								))}
							</select>
						</label>
						<Field
							label={copy.secondaryTypesCommaSeparated}
							value={form.secondaryTypes}
							onChange={form.setSecondaryTypes}
							disabled={form.savingRelease}
						/>
						<Field
							label={copy.language}
							value={form.language}
							onChange={form.setLanguage}
							placeholder="eng"
							disabled={form.savingRelease}
						/>
						<Field
							label={copy.script}
							value={form.script}
							onChange={form.setScript}
							placeholder="Latn"
							disabled={form.savingRelease}
						/>
						<Field
							label={copy.editionDate}
							value={form.releaseDate}
							onChange={form.setReleaseDate}
							placeholder="2024-05-17"
							disabled={form.savingRelease}
						/>
						{form.editionDateIssue && (
							<p className={`${errorClass} sm:col-span-2`} role="alert">
								{copy.editionDateInvalid}
							</p>
						)}
						<Field
							label={copy.country}
							value={form.country}
							onChange={form.setCountry}
							disabled={form.savingRelease}
						/>
						<Field
							label={copy.format}
							value={form.format}
							onChange={form.setFormat}
							disabled={form.savingRelease}
						/>
						<Field
							label={copy.label}
							value={form.label}
							onChange={form.setLabel}
							disabled={form.savingRelease}
						/>
						<Field
							label={copy.catalogNumber}
							value={form.catalogNumber}
							onChange={form.setCatalogNumber}
							disabled={form.savingRelease}
						/>
						<Field
							label={copy.versionDisambiguation}
							value={form.disambiguation}
							onChange={form.setDisambiguation}
							disabled={form.savingRelease}
						/>
						<p className={`${muted} sm:col-span-2`}>{copy.editionHelp}</p>
					</div>
				</details>
				<div className="grid gap-2 border-t border-[var(--line)] pt-2">
					{renderGenreField({
						locale,
						value: form.genres,
						onChange: form.setGenres,
						disabled: form.savingRelease,
					})}
					<small>{copy.releaseGenreExplanation}</small>
				</div>
			</section>
			<section
				className={`${panel} grid gap-2`}
				aria-labelledby="local-release-tracks"
			>
				<div className={row}>
					<h2 id="local-release-tracks" className="m-0 text-base font-semibold">
						{copy.tracklist}
					</h2>
					<Button
						ref={addButton}
						className={secondary}
						isDisabled={pickerOpen || form.savingRelease}
						onPress={() => form.setPicker(true)}
					>
						{copy.addTrack}
					</Button>
				</div>
				{form.positions.length === 0 && (
					<p className={muted}>{copy.noTracks}</p>
				)}
				{form.positions.length > 0 && (
					<ol className={rows} aria-label={copy.tracklist}>
						{form.positions.map((position, index) => {
							const recording =
								form.pickedRecordings.find(
									(record) => record.id === position.recordingId,
								) ??
								recordings.find(
									(record) => record.id === position.recordingId,
								) ??
								form.savedRecordings.find(
									(record) => record.id === position.recordingId,
								);
							const name =
								position.titleOverride.trim() || recording?.title || "—";
							const duration = parseLocalDuration(position.durationInput);
							const credits = recording
								? (recording.artistCredit ??
									formatLocalCredits(recording.credits))
								: "";
							const editing = form.editId === position.id;
							return (
								<li key={position.id} className="py-2">
									<div className={`${row} py-0`}>
										<div className="flex min-w-0 flex-1 items-baseline gap-2 wrap-anywhere">
											<span className="w-6 shrink-0 text-right text-xs text-[var(--muted)] tabular-nums">
												{index + 1}.
											</span>
											<div className="min-w-0">
												<strong>{name}</strong>
												<p className={muted}>
													{[
														recording?.provider,
														Number.isFinite(duration)
															? formatLocalDuration(duration)
															: formatLocalDuration(recording?.duration_ms),
													]
														.filter(Boolean)
														.join(" · ")}
												</p>
												{credits && (
													<p className={muted}>
														{copy.recordingCredits}: {credits}
													</p>
												)}
											</div>
										</div>
										<Button
											className={secondary}
											aria-expanded={editing}
											isDisabled={form.savingRelease}
											aria-label={`${editing ? copy.closeRow : copy.editRow} ${index + 1}`}
											onPress={() =>
												form.setEditId(editing ? undefined : position.id)
											}
										>
											{editing ? copy.closeRow : copy.editRow}
										</Button>
									</div>
									{editing && (
										<div className="mt-2 grid gap-2 rounded bg-[var(--raised)] p-2">
											<div className="grid gap-2 sm:grid-cols-2">
												<Field
													label={copy.positionTitle}
													value={position.titleOverride}
													disabled={form.savingRelease}
													onChange={(value) =>
														form.change(position.id, { titleOverride: value })
													}
												/>
												<Field
													label={copy.positionDuration}
													value={position.durationInput}
													disabled={form.savingRelease}
													onChange={(value) =>
														form.change(position.id, { durationInput: value })
													}
												/>
											</div>
											<p className={muted}>{copy.positionHelp}</p>
											{form.badPosition === position.id && (
												<p className={errorClass} role="alert">
													{copy.badDuration}
												</p>
											)}
											<div className={actions}>
												{!titleReadOnly && (
													<Button
														className={secondary}
														isDisabled={form.savingRelease || pickerOpen}
														onPress={(event) => {
															replaceButton.current =
																event.target as HTMLButtonElement;
															setReplaceId(position.id);
														}}
													>
														{copy.replaceRecording}
													</Button>
												)}
												<Button
													className={secondary}
													isDisabled={form.savingRelease || index === 0}
													onPress={() => form.move(index, -1)}
												>
													{copy.up}
												</Button>
												<Button
													className={secondary}
													isDisabled={
														form.savingRelease ||
														index === form.positions.length - 1
													}
													onPress={() => form.move(index, 1)}
												>
													{copy.down}
												</Button>
												<Button
													className={secondary}
													isDisabled={form.savingRelease}
													onPress={() =>
														form.setPositions((current) =>
															current.filter((item) => item.id !== position.id),
														)
													}
												>
													{copy.remove}
												</Button>
											</div>
										</div>
									)}
								</li>
							);
						})}
					</ol>
				)}
				{pickerOpen && (
					<LocalReleaseRecordingPicker
						locale={locale}
						replacing={Boolean(replaceId)}
						findRecordings={findRecordings}
						saveRecording={saveRecording}
						findArtists={findArtists}
						saveArtist={saveArtist}
						savedArtists={form.savedArtists}
						busy={form.savingRelease}
						onWritingChange={form.markCatalogWrite}
						onSaved={(recording) =>
							form.setSavedRecordings((current) =>
								current.some((item) => item.id === recording.id)
									? current
									: [...current, recording],
							)
						}
						onArtistSaved={rememberArtist}
						onAdd={(recording) => {
							if (replaceId) {
								form.remember(recording);
								form.change(replaceId, { recordingId: recording.id });
							} else form.add(recording);
							closePicker();
						}}
						onClose={closePicker}
					/>
				)}
				{form.savedRecordings.length > 0 && (
					<div
						className="rounded border border-[var(--line)] p-2 text-xs"
						role="status"
					>
						<strong>{copy.savedTracks}: </strong>
						{form.savedRecordings
							.map((recording) => recording.title)
							.join(", ")}
						<p className={muted}>{copy.savedHelp}</p>
					</div>
				)}
				{form.savedArtists.length > 0 && (
					<div
						className="rounded border border-[var(--line)] p-2 text-xs"
						role="status"
					>
						<strong>{copy.savedArtists}: </strong>
						{form.savedArtists
							.map((item) => item.name?.trim() || item.id)
							.join(", ")}
						<p className={muted}>{copy.savedArtistHelp}</p>
					</div>
				)}
			</section>
			<footer className={`${panel} grid gap-2`}>
				<p className={muted}>{copy.saveHelp}</p>
				{form.savingRelease && (
					<p className={muted} role="status">
						{copy.savingRelease}
					</p>
				)}
				{form.saveReleaseError && (
					<p className={errorClass} role="alert">
						{copy.saveReleaseError}
					</p>
				)}
				{form.releaseUncertain && (
					<p className={errorClass} role="alert">
						{copy.saveReleaseUncertain}
					</p>
				)}
				<div className={actions}>
					<Button
						className={primary}
						isDisabled={
							!form.title.trim() ||
							form.savingRelease ||
							form.writingCatalogRecord ||
							form.releaseUncertain ||
							savingArtist
						}
						onPress={() => void form.save()}
					>
						{saveLabel ?? copy.save}
					</Button>
					<Button
						className={secondary}
						isDisabled={form.savingRelease || form.writingCatalogRecord}
						onPress={() => form.setCancel(true)}
					>
						{copy.cancel}
					</Button>
				</div>
			</footer>
		</>
	);
}
