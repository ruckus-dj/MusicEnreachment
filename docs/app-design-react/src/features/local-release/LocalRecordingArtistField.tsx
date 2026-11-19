import { useId, useRef, useState } from "react";
import { Button } from "react-aria-components";
import {
	buildLocalRecordingCredits,
	type FindLocalArtists,
	formatLocalCredits,
	isOutcomeUncertain,
	type LocalRecordingCreditSelection,
	type LocalReleaseArtist,
	type SaveLocalArtist,
} from "./localReleaseFormModel";
import type { LocalReleaseCopy } from "./localReleaseMessages";
import {
	actions,
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

type CreditControl = "up" | "down" | "remove";

function formatArtistName(artist: LocalReleaseArtist): string {
	return artist.name?.trim() || artist.id;
}

export function LocalRecordingArtistField({
	copy,
	findArtists,
	saveArtist,
	credits,
	saved,
	busy,
	onCreditsChange,
	onSavingChange,
	onSaved,
}: {
	copy: LocalReleaseCopy;
	findArtists: FindLocalArtists;
	saveArtist: SaveLocalArtist;
	credits: readonly LocalRecordingCreditSelection[];
	saved: readonly LocalReleaseArtist[];
	busy: boolean;
	onCreditsChange: (credits: LocalRecordingCreditSelection[]) => void;
	onSavingChange: (saving: boolean) => void;
	onSaved: (artist: LocalReleaseArtist) => void;
}) {
	const lookup = useLookup<LocalReleaseArtist>();
	const [query, setQuery] = useState("");
	const [create, setCreate] = useState(false);
	const [saving, setSaving] = useState(false);
	const [saveError, setSaveError] = useState(false);
	const [uncertain, setUncertain] = useState(false);
	const savingRef = useRef(false);
	const searchForm = useRef<HTMLFormElement>(null);
	const controlRefs = useRef(
		new Map<string, Partial<Record<CreditControl, HTMLButtonElement>>>(),
	);
	const descriptionBase = useId();
	const trimQuery = query.trim();
	const isBusy = busy || saving;
	function clearSaveError() {
		if (saveError) setSaveError(false);
	}
	function focusSearch() {
		searchForm.current?.querySelector<HTMLInputElement>("input")?.focus();
	}
	function setControl(
		artistId: string,
		control: CreditControl,
		node: HTMLButtonElement | null,
	) {
		const row = controlRefs.current.get(artistId) ?? {};
		if (node) row[control] = node;
		else delete row[control];
		controlRefs.current.set(artistId, row);
	}
	function focusControl(artistId: string, order: readonly CreditControl[]) {
		requestAnimationFrame(() => {
			const row = controlRefs.current.get(artistId);
			for (const control of order) {
				const button = row?.[control];
				if (
					button &&
					!button.disabled &&
					button.getAttribute("aria-disabled") !== "true"
				) {
					button.focus();
					return;
				}
			}
			focusSearch();
		});
	}
	function moveCreditRow(index: number, by: number) {
		const credit = credits[index];
		const target = index + by;
		if (!credit || target < 0 || target >= credits.length) return;
		const next = [...credits];
		next[index] = next[target];
		next[target] = credit;
		onCreditsChange(next);
		focusControl(
			credit.artistId,
			by < 0 ? ["up", "down", "remove"] : ["down", "up", "remove"],
		);
	}
	function removeCreditRow(index: number) {
		const next = credits.filter((_, item) => item !== index);
		onCreditsChange(next);
		const target = next[Math.min(index, next.length - 1)];
		if (!target) {
			focusSearch();
			return;
		}
		focusControl(target.artistId, ["remove", "up", "down"]);
	}
	function addCredit(artist: LocalReleaseArtist) {
		if (credits.some((credit) => credit.artistId === artist.id)) return;
		clearSaveError();
		onCreditsChange([
			...credits,
			{ artistId: artist.id, creditName: artist.name ?? "" },
		]);
		focusSearch();
	}
	async function saveNewArtist() {
		if (savingRef.current || uncertain) return;
		if (!trimQuery || lookup.result.kind !== "ready" || !create) return;
		savingRef.current = true;
		setSaving(true);
		onSavingChange(true);
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
			onSavingChange(false);
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
		const isAdded = credits.some((credit) => credit.artistId === artist.id);
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
					aria-label={`${copy.addArtist}: ${formatArtistName(artist)} · ${artist.provider}${savedRow ? ` · ${copy.savedArtists}` : ""}`}
					aria-describedby={descriptionId}
					isDisabled={isBusy || isAdded}
					onPress={() => addCredit(artist)}
				>
					{copy.addArtist}
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
			{credits.length > 0 ? (
				<div
					role="group"
					aria-label={copy.recordingCredits}
					className="grid gap-1 rounded border border-[var(--accent)] bg-[var(--accent-soft)] p-2"
				>
					<p className="m-0 min-w-0 text-xs wrap-anywhere">
						<span className="font-medium">{copy.creditsPreview}:</span>{" "}
						<span className="text-sm font-semibold whitespace-pre-wrap wrap-anywhere">
							{formatLocalCredits(buildLocalRecordingCredits(credits))}
						</span>
					</p>
					<ol className={rows} aria-label={copy.creditsOrder}>
						{credits.map((credit, index) => {
							const name = credit.creditName || credit.artistId;
							const last = index + 1 >= credits.length;
							return (
								<li key={credit.artistId} className="grid gap-2 py-2">
									<div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
										<p className="m-0 min-w-0 text-sm wrap-anywhere">
											<span className="mr-1 text-xs opacity-60 tabular-nums">
												{index + 1}.
											</span>
											<strong>{name}</strong>
										</p>
										<div className={actions}>
											<Button
												className={secondary}
												ref={(node) => setControl(credit.artistId, "up", node)}
												aria-label={`${copy.artistUp} ${index + 1}`}
												isDisabled={isBusy || index === 0}
												onPress={() => moveCreditRow(index, -1)}
											>
												{copy.up}
											</Button>
											<Button
												className={secondary}
												ref={(node) =>
													setControl(credit.artistId, "down", node)
												}
												aria-label={`${copy.artistDown} ${index + 1}`}
												isDisabled={isBusy || last}
												onPress={() => moveCreditRow(index, 1)}
											>
												{copy.down}
											</Button>
											<Button
												className={secondary}
												ref={(node) =>
													setControl(credit.artistId, "remove", node)
												}
												aria-label={`${copy.removeArtist} ${index + 1}`}
												isDisabled={isBusy}
												onPress={() => removeCreditRow(index)}
											>
												{copy.removeArtist}
											</Button>
										</div>
									</div>
								</li>
							);
						})}
					</ol>
				</div>
			) : (
				<p className={muted}>
					{copy.noArtistSelected} {copy.recordingArtistOptional}
				</p>
			)}
			<form
				ref={searchForm}
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
