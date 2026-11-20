import { useRef, useState } from "react";
import {
	type MetadataValidationIssue,
	validateMetadataIssues,
} from "../../metadata/projection";
import {
	formatLocalDuration,
	isOutcomeUncertain,
	type LocalReleaseArtist,
	type LocalReleaseDraft,
	type LocalReleaseGenre,
	type LocalReleaseRecording,
	type LocalReleaseTrack,
	parseLocalDuration,
} from "./localReleaseFormModel";

export type LocalReleaseTrackInput = LocalReleaseTrack & {
	durationInput: string;
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
	release_date?: string;
	country_code?: string;
	format?: string;
	label?: string;
	catalog_number?: string;
	disambiguation?: string;
	genres?: LocalReleaseGenre[];
	tracks?: LocalReleaseTrackInput[];
};

type Options = {
	initialValues?: LocalReleaseFormInitialValues;
	onSave: (draft: LocalReleaseDraft) => void | Promise<void>;
};

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
		initialValues?.release_date ?? "",
	);
	const [editionDateIssue, setEditionDateIssue] =
		useState<MetadataValidationIssue | null>(null);
	const [country, setCountry] = useState(initialValues?.country_code ?? "");
	const [format, setFormat] = useState(initialValues?.format ?? "");
	const [label, setLabel] = useState(initialValues?.label ?? "");
	const [catalogNumber, setCatalogNumber] = useState(
		initialValues?.catalog_number ?? "",
	);
	const [disambiguation, setDisambiguation] = useState(
		initialValues?.disambiguation ?? "",
	);
	const [genres, setGenres] = useState<LocalReleaseGenre[]>(
		initialValues?.genres ?? [],
	);
	const [positions, setPositions] = useState<LocalReleaseTrackInput[]>(
		() => initialValues?.tracks ?? [],
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
	const [saveReleaseError, setSaveReleaseError] = useState(false);
	const [releaseUncertain, setReleaseUncertain] = useState(false);
	const savingReleaseRef = useRef(false);

	function setReleaseDate(value: string) {
		setReleaseDateValue(value);
		setEditionDateIssue(null);
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
		const positionSignature = (position: LocalReleaseTrackInput) =>
			[
				position.id,
				position.recordingId,
				position.titleOverride,
				position.durationInput,
				position.durationOverrideMs ?? "",
			].join("|");
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
				releaseDate !== (initial.release_date ?? "") ||
				country !== (initial.country_code ?? "") ||
				format !== (initial.format ?? "") ||
				label !== (initial.label ?? "") ||
				catalogNumber !== (initial.catalog_number ?? "") ||
				disambiguation !== (initial.disambiguation ?? "") ||
				JSON.stringify(artistIds) !== JSON.stringify(initial.artistIds ?? []) ||
				JSON.stringify(genres) !== JSON.stringify(initial.genres ?? []) ||
				JSON.stringify(positions.map(positionSignature)) !==
					JSON.stringify((initial.tracks ?? []).map(positionSignature)),
		);
	}

	async function save() {
		if (savingReleaseRef.current || releaseUncertain) return;
		if (!title.trim()) return;
		const date = releaseDate.trim();
		if (date) {
			const [issue] = validateMetadataIssues(
				{ releasedate: [date] },
				"release",
			);
			if (issue) {
				setEditionDateIssue(issue);
				return;
			}
		}
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
			await onSave({
				title: edition.trim() || title.trim(),
				release_group_title: title.trim(),
				artistIds,
				primaryType: primaryType || undefined,
				secondaryTypes: splitList(secondaryTypes),
				language: language.trim() || undefined,
				script: script.trim() || undefined,
				release_date: releaseDate.trim() || undefined,
				country_code: country.trim() || undefined,
				format: format.trim() || undefined,
				label: label.trim() || undefined,
				catalog_number: catalogNumber.trim() || undefined,
				disambiguation: disambiguation.trim() || undefined,
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
