import { useRef, useState } from "react";
import {
	formatLocalDuration,
	isOutcomeUncertain,
	type LocalReleaseArtist,
	type LocalReleaseDraft,
	type LocalReleaseEditionDraft,
	type LocalReleaseGenre,
	type LocalReleaseRecording,
	type LocalReleaseTrack,
	parseLocalDuration,
	validLocalReleaseDate,
} from "./localReleaseFormModel";

export type LocalReleaseTrackInput = LocalReleaseTrack & {
	durationInput: string;
};

/** Optional display duration; the form derives it from `durationOverrideMs`. */
export type LocalReleaseTrackInitial = LocalReleaseTrack & {
	durationInput?: string;
};

export type LocalReleaseFormInitialValues = {
	/** The release-group/title field shown at the top of the form. */
	title?: string;
	/** An explicit edition title; empty when the edition matches the title. */
	edition?: string;
	artist?: string;
	artistIds?: string[];
	primaryType?: string;
	secondaryTypes?: string[] | string;
	language?: string;
	script?: string;
	editionDetails?: LocalReleaseEditionDraft;
	genres?: LocalReleaseGenre[];
	tracks?: LocalReleaseTrackInitial[];
};

type Options = {
	initialValues?: LocalReleaseFormInitialValues;
	onSave: (draft: LocalReleaseDraft) => void | Promise<void>;
};

function initialTracks(
	tracks: readonly LocalReleaseTrackInitial[] | undefined,
): LocalReleaseTrackInput[] {
	return (tracks ?? []).map((track) => ({
		...track,
		durationInput:
			track.durationInput ?? formatLocalDuration(track.durationOverrideMs),
	}));
}

export function useLocalReleaseForm({ initialValues, onSave }: Options) {
	const [artist, setArtist] = useState(initialValues?.artist ?? "");
	const [title, setTitle] = useState(initialValues?.title ?? "");
	const [artistIds, setArtistIds] = useState<string[]>(
		initialValues?.artistIds ?? [],
	);
	const [edition, setEdition] = useState(initialValues?.edition ?? "");
	const [primaryType, setPrimaryType] = useState(
		initialValues?.primaryType ?? "",
	);
	const [secondaryTypes, setSecondaryTypes] = useState(
		Array.isArray(initialValues?.secondaryTypes)
			? initialValues.secondaryTypes.join(", ")
			: (initialValues?.secondaryTypes ?? ""),
	);
	const [language, setLanguage] = useState(initialValues?.language ?? "");
	const [script, setScript] = useState(initialValues?.script ?? "");
	const [releaseDate, setReleaseDateValue] = useState(
		initialValues?.editionDetails?.release_date ?? "",
	);
	const [editionDateIssue, setEditionDateIssue] = useState(false);
	const [country, setCountry] = useState(
		initialValues?.editionDetails?.country ?? "",
	);
	const [format, setFormat] = useState(
		initialValues?.editionDetails?.format ?? "",
	);
	const [label, setLabel] = useState(
		initialValues?.editionDetails?.label ?? "",
	);
	const [catalogNumber, setCatalogNumber] = useState(
		initialValues?.editionDetails?.catalog_number ?? "",
	);
	const [disambiguation, setDisambiguation] = useState(
		initialValues?.editionDetails?.disambiguation ?? "",
	);
	const [genres, setGenres] = useState<LocalReleaseGenre[]>(
		initialValues?.genres ?? [],
	);
	const [positions, setPositions] = useState<LocalReleaseTrackInput[]>(() =>
		initialTracks(initialValues?.tracks),
	);
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
	const [writingCatalogRecord, setWritingCatalogRecordState] = useState(false);
	const [saveReleaseError, setSaveReleaseError] = useState(false);
	const [releaseUncertain, setReleaseUncertain] = useState(false);
	const savingReleaseRef = useRef(false);
	const writingCatalogRecordRef = useRef(false);

	function setReleaseDate(value: string) {
		setReleaseDateValue(value);
		setEditionDateIssue(false);
	}

	/** Keep release saving fenced while a picker is writing catalog records. */
	function markCatalogWrite(pending: boolean) {
		writingCatalogRecordRef.current = pending;
		setWritingCatalogRecordState(pending);
	}

	function splitList(value: string) {
		return value
			.split(",")
			.map((item) => item.trim())
			.filter(Boolean);
	}

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

	function change(id: string, patch: Partial<LocalReleaseTrackInput>) {
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

	function hasDraft() {
		const initial = initialValues ?? {};
		const initialSecondaryTypes = Array.isArray(initial.secondaryTypes)
			? initial.secondaryTypes.join(", ")
			: (initial.secondaryTypes ?? "");
		const initialEdition = initial.editionDetails ?? {};
		const positionSignature = (position: LocalReleaseTrackInput) =>
			[
				position.id,
				position.recordingId,
				position.titleOverride,
				position.durationInput,
				position.durationOverrideMs ?? "",
			].join("|");
		const genreSignature = (items: readonly LocalReleaseGenre[]) =>
			items
				.map((item) => `${item.id}\u0000${item.name}\u0000${item.source_name}`)
				.join("|");
		return Boolean(
			savedRecordings.length ||
				savedArtists.length ||
				pickedRecordings.length ||
				artist !== (initial.artist ?? "") ||
				title !== (initial.title ?? "") ||
				edition !== (initial.edition ?? "") ||
				primaryType !== (initial.primaryType ?? "") ||
				secondaryTypes !== initialSecondaryTypes ||
				language !== (initial.language ?? "") ||
				script !== (initial.script ?? "") ||
				releaseDate !== (initialEdition.release_date ?? "") ||
				country !== (initialEdition.country ?? "") ||
				format !== (initialEdition.format ?? "") ||
				label !== (initialEdition.label ?? "") ||
				catalogNumber !== (initialEdition.catalog_number ?? "") ||
				disambiguation !== (initialEdition.disambiguation ?? "") ||
				JSON.stringify(artistIds) !== JSON.stringify(initial.artistIds ?? []) ||
				genreSignature(genres) !== genreSignature(initial.genres ?? []) ||
				JSON.stringify(positions.map(positionSignature)) !==
					JSON.stringify(initialTracks(initial.tracks).map(positionSignature)),
		);
	}

	async function save() {
		if (
			savingReleaseRef.current ||
			writingCatalogRecordRef.current ||
			releaseUncertain
		)
			return;
		if (!title.trim()) return;
		const date = releaseDate.trim();
		if (date && !validLocalReleaseDate(date)) {
			setEditionDateIssue(true);
			return;
		}
		const invalidPosition = positions.find((position) =>
			Number.isNaN(parseLocalDuration(position.durationInput)),
		);
		if (invalidPosition) {
			setEditId(invalidPosition.id);
			setBadPosition(invalidPosition.id);
			return;
		}
		const editionDetails: LocalReleaseEditionDraft = {
			release_date: date || undefined,
			country: country.trim() || undefined,
			format: format.trim() || undefined,
			label: label.trim() || undefined,
			catalog_number: catalogNumber.trim() || undefined,
			disambiguation: disambiguation.trim() || undefined,
		};
		savingReleaseRef.current = true;
		setSavingRelease(true);
		setSaveReleaseError(false);
		try {
			await onSave({
				title: edition.trim() || title.trim(),
				release_group_title: title.trim(),
				artistIds,
				primaryType: primaryType || undefined,
				secondaryTypes: splitList(secondaryTypes),
				language: language.trim() || undefined,
				script: script.trim() || undefined,
				edition: Object.values(editionDetails).some(Boolean)
					? editionDetails
					: undefined,
				genres,
				tracks: positions.map(({ durationInput, ...position }) => {
					const originalDuration = position.durationOverrideMs;
					return {
						...position,
						titleOverride: position.titleOverride.trim(),
						durationOverrideMs:
							durationInput === formatLocalDuration(originalDuration)
								? originalDuration
								: parseLocalDuration(durationInput),
					};
				}),
			});
		} catch (error) {
			if (isOutcomeUncertain(error)) setReleaseUncertain(true);
			else setSaveReleaseError(true);
		} finally {
			savingReleaseRef.current = false;
			setSavingRelease(false);
		}
	}

	return {
		artist,
		setArtist,
		title,
		setTitle,
		artistIds,
		setArtistIds,
		edition,
		setEdition,
		primaryType,
		setPrimaryType,
		secondaryTypes,
		setSecondaryTypes,
		language,
		setLanguage,
		script,
		setScript,
		releaseDate,
		setReleaseDate,
		editionDateIssue,
		country,
		setCountry,
		format,
		setFormat,
		label,
		setLabel,
		catalogNumber,
		setCatalogNumber,
		disambiguation,
		setDisambiguation,
		genres,
		setGenres,
		positions,
		setPositions,
		editId,
		setEditId,
		badPosition,
		picker,
		setPicker,
		savedRecordings,
		setSavedRecordings,
		savedArtists,
		setSavedArtists,
		pickedRecordings,
		cancel,
		setCancel,
		savingRelease,
		writingCatalogRecord,
		markCatalogWrite,
		saveReleaseError,
		releaseUncertain,
		remember,
		add,
		change,
		move,
		hasDraft,
		save,
	};
}

export type LocalReleaseFormController = ReturnType<typeof useLocalReleaseForm>;
