import { useEffect, useId, useRef, useState } from "react";
import {
	Button,
	Checkbox,
	Dialog,
	Heading,
	Modal,
	ModalOverlay,
} from "react-aria-components";
import { LocalRecordingArtistField } from "./LocalRecordingArtistField";
import {
	buildLocalRecordingCredits,
	type FindLocalArtists,
	formatLocalCredits,
	formatLocalDuration,
	isOutcomeUncertain,
	type LocalRecordingCreditSelection,
	type LocalRecordingDraft,
	type LocalReleaseArtist,
	type LocalReleaseDraft,
	type LocalReleaseRecording,
	type LocalReleaseTrack,
	parseLocalDuration,
	type SaveLocalArtist,
} from "./localReleaseFormModel";
import {
	type LocalReleaseCopy,
	localReleaseMessages,
} from "./localReleaseMessages";
import {
	actions,
	errorClass,
	Field,
	LookupStatus,
	muted,
	panel,
	primary,
	row,
	rows,
	secondary,
	useLookup,
} from "./localReleaseUi";

export type ReleaseCheckHit = {
	id: string;
	title: string;
	artist: string;
	provider: string;
	details?: string;
};

export type LocalReleaseEditorProps = {
	locale: "ru" | "en";
	suggestedArtist: string;
	suggestedTitle: string;
	incomingLabel: string;
	artists: readonly LocalReleaseArtist[];
	recordings: readonly LocalReleaseRecording[];
	checkRelease: (
		artist: string,
		title: string,
		signal?: AbortSignal,
	) => Promise<readonly ReleaseCheckHit[]>;
	findRecordings: (
		query: string,
		signal?: AbortSignal,
	) => Promise<readonly LocalReleaseRecording[]>;
	saveRecording: (
		data: LocalRecordingDraft,
	) => LocalReleaseRecording | Promise<LocalReleaseRecording>;
	findArtists: FindLocalArtists;
	saveArtist: SaveLocalArtist;
	onUseRelease: (id: string) => void;
	onSave: (data: LocalReleaseDraft) => void | Promise<void>;
	onCancel: () => void;
};

function RecordingPicker({
	copy,
	findRecordings,
	saveRecording,
	findArtists,
	saveArtist,
	savedArtists,
	busy,
	onWritingChange,
	onSaved,
	onArtistSaved,
	onAdd,
	onClose,
}: {
	copy: LocalReleaseCopy;
	findRecordings: LocalReleaseEditorProps["findRecordings"];
	saveRecording: LocalReleaseEditorProps["saveRecording"];
	findArtists: FindLocalArtists;
	saveArtist: SaveLocalArtist;
	savedArtists: readonly LocalReleaseArtist[];
	busy: boolean;
	onWritingChange: (pending: boolean) => void;
	onSaved: (recording: LocalReleaseRecording) => void;
	onArtistSaved: (artist: LocalReleaseArtist) => void;
	onAdd: (recording: LocalReleaseRecording) => void;
	onClose: () => void;
}) {
	const lookup = useLookup<LocalReleaseRecording>();
	const [query, setQuery] = useState("");
	const [create, setCreate] = useState(false);
	const [duration, setDuration] = useState("");
	const [credits, setCredits] = useState<LocalRecordingCreditSelection[]>([]);
	const [invalid, setInvalid] = useState(false);
	const [saved, setSaved] = useState<LocalReleaseRecording>();
	const [saving, setSaving] = useState(false);
	const [artistSaving, setArtistSaving] = useState(false);
	const [saveError, setSaveError] = useState(false);
	const [uncertain, setUncertain] = useState(false);
	const savingRef = useRef(false);
	const titleRef = useRef<HTMLHeadingElement>(null);
	const descriptionBase = useId();
	useEffect(() => titleRef.current?.focus(), []);
	const trimQuery = query.trim();
	const isBusy = busy || saving || artistSaving;
	function clearSaveError() {
		if (saveError) setSaveError(false);
	}
	async function saveNewRecording() {
		if (savingRef.current || uncertain || artistSaving) return;
		if (!trimQuery || lookup.result.kind !== "ready" || !create) return;
		const durationMs = parseLocalDuration(duration);
		if (Number.isNaN(durationMs)) {
			setInvalid(true);
			return;
		}
		savingRef.current = true;
		setSaving(true);
		onWritingChange(true);
		setSaveError(false);
		try {
			const recording = await saveRecording({
				title: trimQuery,
				duration_ms: durationMs ?? undefined,
				...(credits.length > 0
					? {
							credits: buildLocalRecordingCredits(credits),
						}
					: {}),
			});
			onSaved(recording);
			setSaved(recording);
			setCreate(false);
		} catch (error) {
			if (isOutcomeUncertain(error)) setUncertain(true);
			else setSaveError(true);
		} finally {
			savingRef.current = false;
			setSaving(false);
			onWritingChange(false);
		}
	}
	const resultRow = (recording: LocalReleaseRecording, index: number) => {
		const descriptionId = `${descriptionBase}-recording-${index}`;
		const credits = formatLocalCredits(recording.credits);
		return (
			<li key={recording.id} className={row}>
				<div className="min-w-0 wrap-anywhere">
					<strong>{recording.title || "—"}</strong>
					<p id={descriptionId} className={muted}>
						{[
							recording.provider,
							formatLocalDuration(recording.duration_ms),
							credits,
						]
							.filter(Boolean)
							.join(" · ")}
					</p>
				</div>
				<Button
					className={secondary}
					aria-label={`${copy.add}: ${recording.title}`}
					aria-describedby={descriptionId}
					isDisabled={isBusy}
					onPress={() => onAdd(recording)}
				>
					{copy.add}
				</Button>
			</li>
		);
	};
	return (
		<section
			aria-labelledby="local-recording-picker"
			className={`${panel} grid gap-3`}
		>
			<div className={row}>
				<h3
					ref={titleRef}
					tabIndex={-1}
					id="local-recording-picker"
					className="m-0 text-sm font-semibold"
				>
					{copy.addTrack}
				</h3>
				<Button className={secondary} isDisabled={isBusy} onPress={onClose}>
					{copy.closePicker}
				</Button>
			</div>
			<form
				aria-label={copy.recordingQuery}
				className="grid items-end gap-2 sm:grid-cols-[minmax(0,1fr)_auto]"
				onSubmit={(event) => {
					event.preventDefault();
					if (!trimQuery || lookup.result.kind === "pending" || isBusy) return;
					setSaved(undefined);
					setCreate(false);
					clearSaveError();
					void lookup.search((signal) => findRecordings(trimQuery, signal));
				}}
			>
				<Field
					label={copy.recordingQuery}
					value={query}
					required
					disabled={isBusy}
					onChange={(value) => {
						setQuery(value);
						lookup.reset();
						setCreate(false);
						setSaved(undefined);
						clearSaveError();
					}}
				/>
				<Button
					className={secondary}
					type="submit"
					isDisabled={!trimQuery || lookup.result.kind === "pending" || isBusy}
				>
					{copy.search}
				</Button>
			</form>
			{saved ? (
				<ul className={rows} aria-label={copy.savedTracks}>
					{resultRow(saved, 0)}
				</ul>
			) : (
				<LookupStatus
					result={lookup.result}
					copy={copy}
					empty={copy.noRecordings}
				>
					{(hits) => (
						<>
							{hits.length > 0 && (
								<ul className={rows} aria-label={copy.recordingResults}>
									{hits.map((hit, index) => resultRow(hit, index))}
								</ul>
							)}
							{!create && (
								<Button
									className={`${secondary} w-fit`}
									isDisabled={isBusy}
									onPress={() => {
										setDuration("");
										setCredits([]);
										setInvalid(false);
										clearSaveError();
										setCreate(true);
									}}
								>
									{copy.createTrack}
								</Button>
							)}
						</>
					)}
				</LookupStatus>
			)}
			{create && lookup.result.kind === "ready" && (
				<section
					aria-label={copy.newRecording}
					className="grid gap-2 border-t border-[var(--line)] pt-3"
				>
					<h4 className="m-0 text-sm font-semibold">{copy.newRecording}</h4>
					<div className="grid gap-2 sm:grid-cols-2">
						<p className="m-0 self-center font-medium wrap-anywhere">
							{trimQuery}
						</p>
						<Field
							label={copy.duration}
							value={duration}
							disabled={isBusy}
							onChange={(value) => {
								setDuration(value);
								setInvalid(false);
								clearSaveError();
							}}
							placeholder="3:24"
						/>
					</div>
					<LocalRecordingArtistField
						copy={copy}
						findArtists={findArtists}
						saveArtist={saveArtist}
						credits={credits}
						saved={savedArtists}
						busy={isBusy}
						onCreditsChange={setCredits}
						onSavingChange={(pending) => {
							setArtistSaving(pending);
							onWritingChange(pending);
						}}
						onSaved={onArtistSaved}
					/>
					{invalid && (
						<p className={errorClass} role="alert">
							{copy.badDuration}
						</p>
					)}
					{saving && (
						<p className={muted} role="status">
							{copy.savingRecording}
						</p>
					)}
					{saveError && (
						<p className={errorClass} role="alert">
							{copy.saveRecordingError}
						</p>
					)}
					{uncertain && (
						<p className={errorClass} role="alert">
							{copy.saveRecordingUncertain}
						</p>
					)}
					<Button
						className={`${primary} w-fit`}
						isDisabled={isBusy || uncertain}
						onPress={() => void saveNewRecording()}
					>
						{copy.saveTrack}
					</Button>
				</section>
			)}
		</section>
	);
}

type Position = LocalReleaseTrack & { durationInput: string };

export function LocalReleaseEditor(props: LocalReleaseEditorProps) {
	const { locale, incomingLabel, artists, recordings, onCancel } = props;
	const copy = localReleaseMessages[locale];
	const lookup = useLookup<ReleaseCheckHit>();
	const [step, setStep] = useState<"check" | "edit">("check");
	const [artist, setArtist] = useState(props.suggestedArtist);
	const [title, setTitle] = useState(props.suggestedTitle);
	const [artistIds, setArtistIds] = useState<string[]>([]);
	const [edition, setEdition] = useState("");
	const [primaryType, setPrimaryType] = useState("");
	const [language, setLanguage] = useState("");
	const [script, setScript] = useState("");
	const [positions, setPositions] = useState<Position[]>([]);
	const [editId, setEditId] = useState<string>();
	const [badPosition, setBadPosition] = useState<string>();
	const [picker, setPicker] = useState(false);
	const [savedRecordings, setSavedRecordings] = useState<
		LocalReleaseRecording[]
	>([]);
	const [savedArtists, setSavedArtists] = useState<LocalReleaseArtist[]>([]);
	const [pickedRecordings, setPickedRecordings] = useState<
		LocalReleaseRecording[]
	>([]);
	const [cancel, setCancel] = useState(false);
	const [savingRelease, setSavingRelease] = useState(false);
	const [writingCatalogRecord, setWritingCatalogRecord] = useState(false);
	const [saveReleaseError, setSaveReleaseError] = useState(false);
	const [releaseUncertain, setReleaseUncertain] = useState(false);
	const savingReleaseRef = useRef(false);
	const writingCatalogRecordRef = useRef(false);
	const heading = useRef<HTMLHeadingElement>(null);
	const editorHeading = useRef<HTMLHeadingElement>(null);
	const addButton = useRef<HTMLButtonElement>(null);
	const suggestedArtist = props.suggestedArtist.trim();
	const rosterArtists = [
		...artists,
		...savedArtists.filter(
			(artist) => !artists.some((item) => item.id === artist.id),
		),
	];
	const rosterIncludesSuggested =
		!suggestedArtist ||
		rosterArtists.some(
			(item) =>
				(item.name ?? "").trim().toLowerCase() ===
				suggestedArtist.toLowerCase(),
		);
	useEffect(() => heading.current?.focus(), []);
	useEffect(() => {
		if (step === "edit") editorHeading.current?.focus();
	}, [step]);

	function remember(recording: LocalReleaseRecording) {
		setPickedRecordings((current) =>
			current.some((item) => item.id === recording.id)
				? current
				: [...current, recording],
		);
	}

	function add(recording: LocalReleaseRecording) {
		remember(recording);
		setPositions((current) => [
			...current,
			{
				id: crypto.randomUUID(),
				recordingId: recording.id,
				titleOverride: "",
				durationOverrideMs: null,
				durationInput: "",
			},
		]);
		setPicker(false);
	}

	function change(id: string, patch: Partial<Position>) {
		setPositions((current) =>
			current.map((position) =>
				position.id === id ? { ...position, ...patch } : position,
			),
		);
		if (badPosition === id) setBadPosition(undefined);
	}

	function move(index: number, by: number) {
		const target = index + by;
		if (target < 0 || target >= positions.length) return;
		setPositions((current) => {
			const next = [...current];
			[next[index], next[target]] = [next[target], next[index]];
			return next;
		});
	}

	async function save() {
		if (
			savingReleaseRef.current ||
			writingCatalogRecordRef.current ||
			releaseUncertain
		)
			return;
		if (!title.trim()) return;
		const invalidPosition = positions.find((position) =>
			Number.isNaN(parseLocalDuration(position.durationInput)),
		);
		if (invalidPosition) {
			setEditId(invalidPosition.id);
			setBadPosition(invalidPosition.id);
			return;
		}
		savingReleaseRef.current = true;
		setSavingRelease(true);
		setSaveReleaseError(false);
		try {
			await props.onSave({
				title: edition.trim() || title.trim(),
				release_group_title: title.trim(),
				artistIds,
				primaryType: primaryType || undefined,
				language: language.trim() || undefined,
				script: script.trim() || undefined,
				tracks: positions.map(({ durationInput, ...position }) => ({
					...position,
					titleOverride: position.titleOverride.trim(),
					durationOverrideMs: parseLocalDuration(durationInput),
				})),
			});
		} catch (error) {
			if (isOutcomeUncertain(error)) setReleaseUncertain(true);
			else setSaveReleaseError(true);
		} finally {
			savingReleaseRef.current = false;
			setSavingRelease(false);
		}
	}

	return (
		<section className="mx-auto grid w-full min-w-0 max-w-4xl gap-3 py-3 text-sm">
			<header className="min-w-0 border-b border-[var(--line)] pb-3">
				<p className={muted}>
					{copy.from} · {incomingLabel}
				</p>
				<h1 ref={heading} tabIndex={-1} className="my-1 text-xl font-semibold">
					{copy.title}
				</h1>
			</header>
			{step === "check" ? (
				<section
					className={`${panel} grid gap-3`}
					aria-labelledby="local-release-check"
				>
					<h2 id="local-release-check" className="m-0 text-base font-semibold">
						{copy.checkStep}
					</h2>
					<p className={muted}>{copy.checkHelp}</p>
					<form
						aria-label={copy.checkStep}
						className="grid items-end gap-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]"
						onSubmit={(event) => {
							event.preventDefault();
							if (!title.trim() || lookup.result.kind === "pending") return;
							void lookup.search((signal) =>
								props.checkRelease(artist.trim(), title.trim(), signal),
							);
						}}
					>
						<Field
							label={copy.artist}
							value={artist}
							onChange={(value) => {
								setArtist(value);
								lookup.reset();
							}}
						/>
						<Field
							label={copy.release}
							value={title}
							required
							onChange={(value) => {
								setTitle(value);
								lookup.reset();
							}}
						/>
						<Button
							className={primary}
							type="submit"
							isDisabled={!title.trim() || lookup.result.kind === "pending"}
						>
							{copy.check}
						</Button>
					</form>
					<LookupStatus
						result={lookup.result}
						copy={copy}
						empty={copy.noEditions}
					>
						{(hits) => (
							<>
								{hits.length > 0 && (
									<ul className={rows} aria-label={copy.editionResults}>
										{hits.map((hit) => (
											<li className={row} key={hit.id}>
												<div className="min-w-0 wrap-anywhere">
													<strong>{hit.title}</strong>
													<p className={muted}>
														{[hit.artist, hit.provider, hit.details]
															.filter(Boolean)
															.join(" · ")}
													</p>
												</div>
												<Button
													className={secondary}
													onPress={() => props.onUseRelease(hit.id)}
												>
													{copy.useEdition}
												</Button>
											</li>
										))}
									</ul>
								)}
								<Button
									className={`${primary} w-fit`}
									onPress={() => setStep("edit")}
								>
									{copy.next}
								</Button>
							</>
						)}
					</LookupStatus>
					<Button
						className={`${secondary} w-fit`}
						onPress={() => {
							if (
								positions.length ||
								artistIds.length ||
								savedRecordings.length ||
								savedArtists.length ||
								edition ||
								primaryType ||
								language ||
								script
							)
								setCancel(true);
							else onCancel();
						}}
					>
						{copy.cancel}
					</Button>
				</section>
			) : (
				<>
					<section
						aria-labelledby="local-release-edit"
						className={`${panel} grid gap-3`}
					>
						<h2
							id="local-release-edit"
							ref={editorHeading}
							tabIndex={-1}
							className="m-0 text-base font-semibold"
						>
							{copy.editStep}
						</h2>
						<div className="flex flex-wrap items-end gap-2">
							<div className="min-w-0 flex-1">
								<Field
									label={copy.release}
									value={title}
									onChange={setTitle}
									readOnly
								/>
							</div>
							<Button
								className={secondary}
								isDisabled={savingRelease || writingCatalogRecord}
								onPress={() => {
									if (!writingCatalogRecordRef.current) setStep("check");
								}}
							>
								{copy.recheck}
							</Button>
						</div>
						<fieldset className="m-0 grid min-w-0 gap-1 border-0 p-0">
							<legend className="mb-1 p-0 text-xs font-semibold">
								{copy.artists}
							</legend>
							<p className={muted}>{copy.artistHelp}</p>
							{suggestedArtist && !rosterIncludesSuggested && (
								<p className={muted}>
									{copy.suggestedArtistContext}{" "}
									<strong>{suggestedArtist}</strong>
								</p>
							)}
							<div className="flex flex-wrap gap-2">
								{rosterArtists.map((item) => (
									<Checkbox
										className="flex cursor-pointer items-center gap-1.5 rounded border border-[var(--control)] px-2 py-1 text-xs wrap-anywhere data-[selected]:border-[var(--accent)] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]"
										key={item.id}
										isSelected={artistIds.includes(item.id)}
										isDisabled={savingRelease}
										onChange={(selected) =>
											setArtistIds((current) =>
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
						<details className="border-t border-[var(--line)] pt-2">
							<summary className="min-h-8 w-fit cursor-pointer py-1 text-sm font-semibold">
								{copy.details}
							</summary>
							<div className="grid gap-2 pt-2 sm:grid-cols-2">
								<Field
									label={copy.edition}
									value={edition}
									onChange={setEdition}
									placeholder={title}
									disabled={savingRelease}
								/>
								<label className="grid gap-1 text-xs font-medium">
									{copy.type}
									<select
										className="min-h-8 rounded border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)]"
										value={primaryType}
										disabled={savingRelease}
										onChange={(event) => setPrimaryType(event.target.value)}
									>
										<option value="">—</option>
										{["Album", "Single", "EP", "Broadcast", "Other"].map(
											(type) => (
												<option key={type}>{type}</option>
											),
										)}
									</select>
								</label>
								<Field
									label={copy.language}
									value={language}
									onChange={setLanguage}
									placeholder="eng"
									disabled={savingRelease}
								/>
								<Field
									label={copy.script}
									value={script}
									onChange={setScript}
									placeholder="Latn"
									disabled={savingRelease}
								/>
								<p className={`${muted} sm:col-span-2`}>{copy.editionHelp}</p>
							</div>
						</details>
					</section>
					<section
						className={`${panel} grid gap-2`}
						aria-labelledby="local-release-tracks"
					>
						<div className={row}>
							<h2
								id="local-release-tracks"
								className="m-0 text-base font-semibold"
							>
								{copy.tracklist}
							</h2>
							<Button
								ref={addButton}
								className={secondary}
								isDisabled={picker || savingRelease}
								onPress={() => setPicker(true)}
							>
								{copy.addTrack}
							</Button>
						</div>
						{positions.length === 0 && <p className={muted}>{copy.noTracks}</p>}
						{positions.length > 0 && (
							<ol className={rows} aria-label={copy.tracklist}>
								{positions.map((position, index) => {
									const recording =
										recordings.find(
											(record) => record.id === position.recordingId,
										) ??
										pickedRecordings.find(
											(record) => record.id === position.recordingId,
										) ??
										savedRecordings.find(
											(record) => record.id === position.recordingId,
										);
									const name =
										position.titleOverride.trim() || recording?.title || "—";
									const duration = parseLocalDuration(position.durationInput);
									const credits = formatLocalCredits(recording?.credits);
									const editing = editId === position.id;
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
													isDisabled={savingRelease}
													aria-label={`${editing ? copy.closeRow : copy.editRow} ${index + 1}`}
													onPress={() =>
														setEditId(editing ? undefined : position.id)
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
															disabled={savingRelease}
															onChange={(value) =>
																change(position.id, { titleOverride: value })
															}
														/>
														<Field
															label={copy.positionDuration}
															value={position.durationInput}
															disabled={savingRelease}
															onChange={(value) =>
																change(position.id, { durationInput: value })
															}
														/>
													</div>
													<p className={muted}>{copy.positionHelp}</p>
													{badPosition === position.id && (
														<p className={errorClass} role="alert">
															{copy.badDuration}
														</p>
													)}
													<div className={actions}>
														<Button
															className={secondary}
															isDisabled={savingRelease || index === 0}
															onPress={() => move(index, -1)}
														>
															{copy.up}
														</Button>
														<Button
															className={secondary}
															isDisabled={
																savingRelease || index === positions.length - 1
															}
															onPress={() => move(index, 1)}
														>
															{copy.down}
														</Button>
														<Button
															className={secondary}
															isDisabled={savingRelease}
															onPress={() =>
																setPositions((current) =>
																	current.filter(
																		(item) => item.id !== position.id,
																	),
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
						{picker && (
							<RecordingPicker
								copy={copy}
								findRecordings={props.findRecordings}
								saveRecording={props.saveRecording}
								findArtists={props.findArtists}
								saveArtist={props.saveArtist}
								savedArtists={savedArtists}
								busy={savingRelease}
								onWritingChange={(pending) => {
									writingCatalogRecordRef.current = pending;
									setWritingCatalogRecord(pending);
								}}
								onSaved={(recording) =>
									setSavedRecordings((current) =>
										current.some((item) => item.id === recording.id)
											? current
											: [...current, recording],
									)
								}
								onArtistSaved={(artist) =>
									setSavedArtists((current) =>
										current.some((item) => item.id === artist.id)
											? current
											: [...current, artist],
									)
								}
								onAdd={(recording) => {
									add(recording);
									requestAnimationFrame(() => addButton.current?.focus());
								}}
								onClose={() => {
									setPicker(false);
									requestAnimationFrame(() => addButton.current?.focus());
								}}
							/>
						)}
						{savedRecordings.length > 0 && (
							<div
								className="rounded border border-[var(--line)] p-2 text-xs"
								role="status"
							>
								<strong>{copy.savedTracks}: </strong>
								{savedRecordings.map((recording) => recording.title).join(", ")}
								<p className={muted}>{copy.savedHelp}</p>
							</div>
						)}
						{savedArtists.length > 0 && (
							<div
								className="rounded border border-[var(--line)] p-2 text-xs"
								role="status"
							>
								<strong>{copy.savedArtists}: </strong>
								{savedArtists
									.map((item) => item.name?.trim() || item.id)
									.join(", ")}
								<p className={muted}>{copy.savedArtistHelp}</p>
							</div>
						)}
					</section>
					<footer className={`${panel} grid gap-2`}>
						<p className={muted}>{copy.saveHelp}</p>
						{savingRelease && (
							<p className={muted} role="status">
								{copy.savingRelease}
							</p>
						)}
						{saveReleaseError && (
							<p className={errorClass} role="alert">
								{copy.saveReleaseError}
							</p>
						)}
						{releaseUncertain && (
							<p className={errorClass} role="alert">
								{copy.saveReleaseUncertain}
							</p>
						)}
						<div className={actions}>
							<Button
								className={primary}
								isDisabled={
									savingRelease || writingCatalogRecord || releaseUncertain
								}
								onPress={() => void save()}
							>
								{copy.save}
							</Button>
							<Button
								className={secondary}
								isDisabled={savingRelease || writingCatalogRecord}
								onPress={() => {
									if (!writingCatalogRecordRef.current) setCancel(true);
								}}
							>
								{copy.cancel}
							</Button>
						</div>
					</footer>
				</>
			)}
			<ModalOverlay
				isOpen={cancel}
				onOpenChange={setCancel}
				className="fixed inset-0 z-50 grid place-items-center overflow-auto overscroll-contain bg-black/40 p-4"
			>
				<Modal className="w-full max-w-[420px]">
					<Dialog className={`${panel} grid gap-3 outline-none`}>
						<Heading slot="title" className="m-0 text-base font-semibold">
							{copy.cancelQuestion}
						</Heading>
						<p className="m-0 text-sm">{copy.cancelHelp}</p>
						{savedRecordings.length > 0 && (
							<p className={muted}>
								{copy.savedHelp}{" "}
								{savedRecordings.map((item) => item.title).join(", ")}.
							</p>
						)}
						{savedArtists.length > 0 && (
							<p className={muted}>
								{copy.savedArtistHelp}{" "}
								{savedArtists
									.map((item) => item.name?.trim() || item.id)
									.join(", ")}
								.
							</p>
						)}
						<div className={actions}>
							<Button className={primary} onPress={() => setCancel(false)}>
								{copy.keep}
							</Button>
							<Button className={secondary} onPress={onCancel}>
								{copy.discard}
							</Button>
						</div>
					</Dialog>
				</Modal>
			</ModalOverlay>
		</section>
	);
}
