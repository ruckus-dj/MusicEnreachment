import { useId, useRef, useState } from "react";
import { Button } from "react-aria-components";
import {
	type FindLocalArtists,
	isOutcomeUncertain,
	type LocalReleaseArtist,
	type SaveLocalArtist,
} from "./localReleaseFormModel";
import type { LocalReleaseCopy } from "./localReleaseMessages";
import {
	errorClass,
	Field,
	LookupStatus,
	muted,
	primary,
	row,
	rows,
	secondary,
	useLookup,
} from "./localReleaseUi";

function formatArtistName(artist: LocalReleaseArtist): string {
	return artist.name?.trim() || artist.id;
}

export function LocalRecordingArtistField({
	copy,
	findArtists,
	saveArtist,
	selected,
	saved,
	busy,
	onSelect,
	onSaved,
}: {
	copy: LocalReleaseCopy;
	findArtists: FindLocalArtists;
	saveArtist: SaveLocalArtist;
	selected?: LocalReleaseArtist;
	saved: readonly LocalReleaseArtist[];
	busy: boolean;
	onSelect: (artist: LocalReleaseArtist | undefined) => void;
	onSaved: (artist: LocalReleaseArtist) => void;
}) {
	const lookup = useLookup<LocalReleaseArtist>();
	const [query, setQuery] = useState("");
	const [create, setCreate] = useState(false);
	const [saving, setSaving] = useState(false);
	const [saveError, setSaveError] = useState(false);
	const [uncertain, setUncertain] = useState(false);
	const savingRef = useRef(false);
	const descriptionBase = useId();
	const trimQuery = query.trim();
	const isBusy = busy || saving;
	function clearSaveError() {
		if (saveError) setSaveError(false);
	}
	async function saveNewArtist() {
		if (savingRef.current || uncertain) return;
		if (!trimQuery || lookup.result.kind !== "ready" || !create) return;
		savingRef.current = true;
		setSaving(true);
		setSaveError(false);
		try {
			const artist = await saveArtist({ name: trimQuery });
			onSaved(artist);
			setCreate(false);
		} catch (error) {
			if (isOutcomeUncertain(error)) setUncertain(true);
			else setSaveError(true);
		} finally {
			savingRef.current = false;
			setSaving(false);
		}
	}
	function artistRow(
		artist: LocalReleaseArtist,
		index: number,
		savedRow = false,
	) {
		const descriptionId = `${descriptionBase}-${savedRow ? "saved" : "result"}-${index}`;
		const description = [artist.provider, savedRow ? copy.savedArtists : ""]
			.filter(Boolean)
			.join(" · ");
		const isSelected = selected?.id === artist.id;
		return (
			<li key={`${savedRow ? "saved" : "result"}-${artist.id}`} className={row}>
				<div className="min-w-0 wrap-anywhere">
					<strong>{formatArtistName(artist)}</strong>
					<p id={descriptionId} className={muted}>
						{description}
					</p>
				</div>
				<Button
					className={secondary}
					aria-label={`${copy.selectArtist}: ${formatArtistName(artist)} · ${artist.provider}${savedRow ? ` · ${copy.savedArtists}` : ""}`}
					aria-describedby={descriptionId}
					isDisabled={isBusy || isSelected}
					onPress={() => onSelect(artist)}
				>
					{copy.selectArtist}
				</Button>
			</li>
		);
	}
	return (
		<section
			aria-label={copy.recordingArtist}
			className="grid gap-2 rounded border border-[var(--line)] p-2"
		>
			<div className="grid gap-1">
				<h4 className="m-0 text-sm font-semibold">{copy.recordingArtist}</h4>
				<p className={muted}>{copy.recordingArtistHelp}</p>
			</div>
			{selected ? (
				<div
					role="group"
					aria-label={copy.selectedArtist}
					className="grid gap-1 rounded border border-[var(--accent)] bg-[var(--accent-soft)] p-2"
				>
					<p className="m-0 text-xs font-semibold">{copy.selectedArtist}</p>
					<p className="m-0 text-sm wrap-anywhere">
						<strong>{formatArtistName(selected)}</strong> · {selected.provider}
					</p>
					<Button
						className={`${secondary} w-fit`}
						isDisabled={isBusy}
						onPress={() => onSelect(undefined)}
					>
						{copy.clearArtist}
					</Button>
				</div>
			) : (
				<p className={muted}>
					{copy.noArtistSelected} {copy.recordingArtistOptional}
				</p>
			)}
			<form
				aria-label={copy.artistQuery}
				className="grid items-end gap-2 sm:grid-cols-[minmax(0,1fr)_auto]"
				onSubmit={(event) => {
					event.preventDefault();
					if (!trimQuery || lookup.result.kind === "pending" || isBusy) return;
					setCreate(false);
					clearSaveError();
					void lookup.search((signal) => findArtists(trimQuery, signal));
				}}
			>
				<Field
					label={copy.artistQuery}
					value={query}
					required
					disabled={isBusy}
					onChange={(value) => {
						setQuery(value);
						lookup.reset();
						setCreate(false);
						clearSaveError();
					}}
				/>
				<Button
					className={secondary}
					type="submit"
					isDisabled={!trimQuery || lookup.result.kind === "pending" || isBusy}
				>
					{copy.findArtist}
				</Button>
			</form>
			<LookupStatus result={lookup.result} copy={copy} empty={copy.noArtists}>
				{(hits) => (
					<>
						{hits.length > 0 && (
							<ul className={rows} aria-label={copy.artistResults}>
								{hits.map((artist, index) => artistRow(artist, index))}
							</ul>
						)}
						{!create && (
							<Button
								className={`${secondary} w-fit`}
								isDisabled={isBusy}
								onPress={() => {
									clearSaveError();
									setCreate(true);
								}}
							>
								{copy.createArtist}
							</Button>
						)}
					</>
				)}
			</LookupStatus>
			{create && lookup.result.kind === "ready" && (
				<section
					aria-label={copy.newArtist}
					className="grid gap-2 border-t border-[var(--line)] pt-2"
				>
					<h5 className="m-0 text-sm font-semibold">{copy.newArtist}</h5>
					<p className="m-0 font-medium wrap-anywhere">{trimQuery}</p>
					{saving && (
						<p className={muted} role="status">
							{copy.savingArtist}
						</p>
					)}
					{saveError && (
						<p className={errorClass} role="alert">
							{copy.saveArtistError}
						</p>
					)}
					{uncertain && (
						<p className={errorClass} role="alert">
							{copy.saveArtistUncertain}
						</p>
					)}
					<Button
						className={`${primary} w-fit`}
						isDisabled={isBusy || uncertain}
						onPress={() => void saveNewArtist()}
					>
						{copy.saveArtist}
					</Button>
				</section>
			)}
			{saved.length > 0 && (
				<section
					aria-label={copy.savedArtists}
					className="grid gap-1 border-t border-[var(--line)] pt-2"
				>
					<h5 className="m-0 text-sm font-semibold">{copy.savedArtists}</h5>
					<ul className={rows}>
						{saved.map((artist, index) => artistRow(artist, index, true))}
					</ul>
					<p className={muted}>{copy.savedArtistHelp}</p>
				</section>
			)}
		</section>
	);
}
