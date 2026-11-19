import { type ReactNode, useEffect, useRef, useState } from "react";
import {
	Button,
	Checkbox,
	Dialog,
	Heading,
	Modal,
	ModalOverlay,
} from "react-aria-components";
import {
	formatLocalDuration,
	type LocalProviderRecord,
	type LocalProviderTrack,
	parseLocalDuration,
} from "../../localProviderModel";
import { localReleaseMessages } from "./localReleaseMessages";

export type ReleaseCheckHit = {
	id: string;
	title: string;
	artist: string;
	provider: string;
	details?: string;
};

type NewRecord = Omit<LocalProviderRecord, "id" | "key" | "provider">;

export type LocalReleaseEditorProps = {
	locale: "ru" | "en";
	suggestedArtist: string;
	suggestedTitle: string;
	incomingLabel: string;
	artists: readonly LocalProviderRecord[];
	recordings: readonly LocalProviderRecord[];
	checkRelease: (
		artist: string,
		title: string,
	) => Promise<readonly ReleaseCheckHit[]>;
	findRecordings: (query: string) => Promise<readonly LocalProviderRecord[]>;
	saveRecording: (data: {
		title: string;
		duration_ms?: number;
	}) => LocalProviderRecord;
	onUseRelease: (id: string) => void;
	onSave: (data: NewRecord) => void;
	onCancel: () => void;
};

type Copy = (typeof localReleaseMessages)[keyof typeof localReleaseMessages];
type SearchResult<T> =
	| { kind: "idle" | "pending" | "error" }
	| { kind: "ready"; hits: readonly T[] };

function useLookup<T>() {
	const [result, setResult] = useState<SearchResult<T>>({ kind: "idle" });
	const generation = useRef(0);
	function reset() {
		generation.current += 1;
		setResult({ kind: "idle" });
	}
	async function search(load: () => Promise<readonly T[]>) {
		const request = ++generation.current;
		setResult({ kind: "pending" });
		try {
			const hits = await load();
			if (request === generation.current) setResult({ kind: "ready", hits });
		} catch {
			if (request === generation.current) setResult({ kind: "error" });
		}
	}
	return { result, reset, search };
}

const panel =
	"min-w-0 rounded-md border border-[var(--line)] bg-[var(--surface)] p-3 sm:p-4";
const actions = "flex flex-wrap items-center gap-2";
const secondary = "demo-button max-w-full";
const primary = `${secondary} font-semibold border-[var(--accent)] bg-[var(--accent-soft)]`;
const muted = "m-0 text-xs text-[var(--muted)] wrap-anywhere";
const rows = "m-0 list-none divide-y divide-[var(--line)] p-0";
const row = "flex min-w-0 flex-wrap items-center justify-between gap-2 py-2.5";
const errorClass = "m-0 text-xs font-medium text-[var(--error)]";

function Field({
	label,
	value,
	onChange,
	required,
	placeholder,
	readOnly,
}: {
	label: string;
	value: string;
	onChange: (value: string) => void;
	required?: boolean;
	placeholder?: string;
	readOnly?: boolean;
}) {
	return (
		<label className="grid min-w-0 gap-1 text-xs font-medium">
			{label}
			<input
				className="min-h-8 w-full min-w-0 rounded border border-[var(--control)] bg-[var(--surface)] px-2 py-1 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--focus)]"
				value={value}
				onChange={(event) => onChange(event.target.value)}
				required={required}
				placeholder={placeholder}
				readOnly={readOnly}
			/>
		</label>
	);
}

function LookupStatus<T>({
	result,
	copy,
	empty,
	children,
}: {
	result: SearchResult<T>;
	copy: Copy;
	empty: string;
	children: (hits: readonly T[]) => ReactNode;
}) {
	if (result.kind === "pending")
		return (
			<p className={muted} role="status">
				{copy.checking}
			</p>
		);
	if (result.kind === "error")
		return (
			<p className={errorClass} role="alert">
				{copy.checkError}
			</p>
		);
	if (result.kind !== "ready") return null;
	return (
		<>
			{result.hits.length === 0 && (
				<p className={muted} role="status">
					{empty}
				</p>
			)}
			{children(result.hits)}
		</>
	);
}

function RecordingPicker({
	copy,
	findRecordings,
	saveRecording,
	onSaved,
	onAdd,
	onClose,
}: {
	copy: Copy;
	findRecordings: LocalReleaseEditorProps["findRecordings"];
	saveRecording: LocalReleaseEditorProps["saveRecording"];
	onSaved: (recording: LocalProviderRecord) => void;
	onAdd: (recording: LocalProviderRecord) => void;
	onClose: () => void;
}) {
	const lookup = useLookup<LocalProviderRecord>();
	const [query, setQuery] = useState("");
	const [create, setCreate] = useState(false);
	const [duration, setDuration] = useState("");
	const [invalid, setInvalid] = useState(false);
	const [saved, setSaved] = useState<LocalProviderRecord>();
	const titleRef = useRef<HTMLHeadingElement>(null);
	useEffect(() => titleRef.current?.focus(), []);
	const trimQuery = query.trim();
	function saveNewRecording() {
		if (!trimQuery || lookup.result.kind !== "ready" || !create) return;
		const durationMs = parseLocalDuration(duration);
		if (Number.isNaN(durationMs)) {
			setInvalid(true);
			return;
		}
		const recording = saveRecording({
			title: trimQuery,
			duration_ms: durationMs ?? undefined,
		});
		onSaved(recording);
		setSaved(recording);
		setCreate(false);
	}
	const resultRow = (recording: LocalProviderRecord) => (
		<li key={recording.id} className={row}>
			<div className="min-w-0 wrap-anywhere">
				<strong>{recording.title}</strong>
				<p className={muted}>
					{[recording.provider, formatLocalDuration(recording.duration_ms)]
						.filter(Boolean)
						.join(" · ")}
				</p>
			</div>
			<Button
				className={secondary}
				aria-label={`${copy.add}: ${recording.title}`}
				onPress={() => onAdd(recording)}
			>
				{copy.add}
			</Button>
		</li>
	);
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
				<Button className={secondary} onPress={onClose}>
					{copy.closePicker}
				</Button>
			</div>
			<form
				aria-label={copy.recordingQuery}
				className="grid items-end gap-2 sm:grid-cols-[minmax(0,1fr)_auto]"
				onSubmit={(event) => {
					event.preventDefault();
					if (!trimQuery || lookup.result.kind === "pending") return;
					setSaved(undefined);
					setCreate(false);
					void lookup.search(() => findRecordings(trimQuery));
				}}
			>
				<Field
					label={copy.recordingQuery}
					value={query}
					required
					onChange={(value) => {
						setQuery(value);
						lookup.reset();
						setCreate(false);
						setSaved(undefined);
					}}
				/>
				<Button
					className={secondary}
					type="submit"
					isDisabled={!trimQuery || lookup.result.kind === "pending"}
				>
					{copy.search}
				</Button>
			</form>
			{saved ? (
				<ul className={rows} aria-label={copy.savedTracks}>
					{resultRow(saved)}
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
									{hits.map(resultRow)}
								</ul>
							)}
							{!create && (
								<Button
									className={`${secondary} w-fit`}
									onPress={() => {
										setDuration("");
										setInvalid(false);
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
							onChange={(value) => {
								setDuration(value);
								setInvalid(false);
							}}
							placeholder="3:24"
						/>
					</div>
					{invalid && (
						<p className={errorClass} role="alert">
							{copy.badDuration}
						</p>
					)}
					<Button className={`${primary} w-fit`} onPress={saveNewRecording}>
						{copy.saveTrack}
					</Button>
				</section>
			)}
		</section>
	);
}

type Position = LocalProviderTrack & { durationInput: string };

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
	const [savedRecordings, setSavedRecordings] = useState<LocalProviderRecord[]>(
		[],
	);
	const [cancel, setCancel] = useState(false);
	const heading = useRef<HTMLHeadingElement>(null);
	const editorHeading = useRef<HTMLHeadingElement>(null);
	const addButton = useRef<HTMLButtonElement>(null);
	useEffect(() => heading.current?.focus(), []);
	useEffect(() => {
		if (step === "edit") editorHeading.current?.focus();
	}, [step]);

	function add(recording: LocalProviderRecord) {
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

	function save() {
		if (!title.trim()) return;
		const invalidPosition = positions.find((position) =>
			Number.isNaN(parseLocalDuration(position.durationInput)),
		);
		if (invalidPosition) {
			setEditId(invalidPosition.id);
			setBadPosition(invalidPosition.id);
			return;
		}
		props.onSave({
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
							void lookup.search(() =>
								props.checkRelease(artist.trim(), title.trim()),
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
							<Button className={secondary} onPress={() => setStep("check")}>
								{copy.recheck}
							</Button>
						</div>
						<fieldset className="m-0 grid min-w-0 gap-1 border-0 p-0">
							<legend className="mb-1 p-0 text-xs font-semibold">
								{copy.artists}
							</legend>
							<p className={muted}>{copy.artistHelp}</p>
							<div className="flex flex-wrap gap-2">
								{artists.map((item) => (
									<Checkbox
										className="flex cursor-pointer items-center gap-1.5 rounded border border-[var(--control)] px-2 py-1 text-xs wrap-anywhere data-[selected]:border-[var(--accent)] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)]"
										key={item.id}
										isSelected={artistIds.includes(item.id)}
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
								/>
								<label className="grid gap-1 text-xs font-medium">
									{copy.type}
									<select
										className="min-h-8 rounded border border-[var(--control)] bg-[var(--surface)] px-2 text-[var(--ink)]"
										value={primaryType}
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
								/>
								<Field
									label={copy.script}
									value={script}
									onChange={setScript}
									placeholder="Latn"
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
								isDisabled={picker}
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
										savedRecordings.find(
											(record) => record.id === position.recordingId,
										);
									const name =
										position.titleOverride.trim() || recording?.title || "—";
									const duration = parseLocalDuration(position.durationInput);
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
													</div>
												</div>
												<Button
													className={secondary}
													aria-expanded={editing}
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
															onChange={(value) =>
																change(position.id, { titleOverride: value })
															}
														/>
														<Field
															label={copy.positionDuration}
															value={position.durationInput}
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
															isDisabled={index === 0}
															onPress={() => move(index, -1)}
														>
															{copy.up}
														</Button>
														<Button
															className={secondary}
															isDisabled={index === positions.length - 1}
															onPress={() => move(index, 1)}
														>
															{copy.down}
														</Button>
														<Button
															className={secondary}
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
								onSaved={(recording) =>
									setSavedRecordings((current) => [...current, recording])
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
					</section>
					<footer className={`${panel} grid gap-2`}>
						<p className={muted}>{copy.saveHelp}</p>
						<div className={actions}>
							<Button className={primary} onPress={save}>
								{copy.save}
							</Button>
							<Button className={secondary} onPress={() => setCancel(true)}>
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
