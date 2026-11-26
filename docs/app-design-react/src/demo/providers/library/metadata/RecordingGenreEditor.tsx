import { type ReactNode, useState } from "react";
import { useI18n } from "../../demoPreferences";
import {
	GenreMetadataEditorDataBinder,
	type GenreMetadataEditorModel,
} from "../../../../ui/features/library/GenreMetadataEditorOutlet";
import type { GenreMetadataState } from "../../../../ui/features/library/GenreMetadataEditorView";
import { genreMessages } from "../../../../ui/features/library/genreMessages";
import type { OriginalTrack } from "../catalogData";
import type { MetadataGenre } from "./projection";
import {
	type GenreReference as DemoGenreReference,
	useGenrePickerDemoProvider,
} from "./useGenrePickerDemoProvider";

type SavedRecordingGenre = {
	state: GenreMetadataState;
	value: DemoGenreReference[];
};

function savedRecordingGenre(track: OriginalTrack): SavedRecordingGenre {
	const override = track.recordingMetadata?.genre;
	if (override !== undefined) {
		if (!override.some((name) => name.trim()))
			return { state: "explicit_empty", value: [] };
		const alignedRefs =
			track.recordingGenreRefs?.length === override.length
				? track.recordingGenreRefs
				: undefined;
		const value = override.flatMap((name, index) => {
			if (!name.trim()) return [];
			const reference = alignedRefs?.[index];
			if (reference?.name === name) {
				const mapped = metadataGenreReference(reference);
				if (mapped) return [mapped];
			}
			return [{ name }];
		});
		return { state: "value", value };
	}
	const references = track.recordingGenreRefs?.length
		? track.recordingGenreRefs
		: (track.recordingGenres ?? []).map((name) => ({ name }));
	const value = references.flatMap((genre) => {
		const mapped = metadataGenreReference(genre);
		return mapped ? [mapped] : [];
	});
	return value.length
		? { state: "value", value }
		: { state: "absent", value: [] };
}

function metadataGenreReference(
	genre: MetadataGenre,
): DemoGenreReference | undefined {
	if (!genre.name?.trim()) return undefined;
	return genre.id ? { id: genre.id, name: genre.name } : { name: genre.name };
}

function withRecordingGenre(
	current: OriginalTrack,
	state: GenreMetadataState,
	value: readonly DemoGenreReference[],
): OriginalTrack {
	const metadata = { ...(current.recordingMetadata ?? {}) };
	const comparison = { ...(current.recordingComparisonOverrides ?? {}) };
	const names = value.flatMap((genre) =>
		genre.name.trim() ? [genre.name] : [],
	);
	if (state === "absent") {
		delete metadata.genre;
		delete comparison.genre;
	} else {
		metadata.genre = state === "value" ? names : [];
		comparison.genre = state === "value" ? names : [];
	}
	const updated: OriginalTrack = { ...current };
	if (Object.keys(metadata).length) updated.recordingMetadata = metadata;
	else delete updated.recordingMetadata;
	if (Object.keys(comparison).length)
		updated.recordingComparisonOverrides = comparison;
	else delete updated.recordingComparisonOverrides;
	if (state === "value") {
		updated.recordingGenres = names;
		updated.recordingGenreRefs = value.flatMap((genre) =>
			genre.name.trim()
				? [genre.id ? { id: genre.id, name: genre.name } : { name: genre.name }]
				: [],
		);
	} else {
		delete updated.recordingGenres;
		delete updated.recordingGenreRefs;
	}
	return updated;
}

type RecordingGenreEditorProps = {
	track: OriginalTrack;
	genresLoaded: boolean;
	onLoadGenres?: () => void | Promise<void>;
	onRecordingChange: (
		update: (current: OriginalTrack) => OriginalTrack,
	) => void;
};

function useRecordingGenreEditorDemoProvider({
	track,
	genresLoaded,
	onLoadGenres,
	onRecordingChange,
}: RecordingGenreEditorProps): GenreMetadataEditorModel {
	const { locale } = useI18n();
	const saved = savedRecordingGenre(track);
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState<DemoGenreReference[]>(saved.value);
	const [draftState, setDraftState] = useState<GenreMetadataState>(saved.state);
	const [draftDirty, setDraftDirty] = useState(false);

	const displayed = editing ? draft : saved.value;
	const {
		value: selection,
		onChange,
		editing: _editing,
		label: _label,
		copy: _copy,
		disabled: _disabled,
		...picker
	} = useGenrePickerDemoProvider({
		value: displayed,
		onChange: changeDraft,
		readOnly: !editing,
		loaded: genresLoaded,
		onLoad: onLoadGenres,
		label: genreMessages[locale].label.recording,
		lifetime: editing ? "editing" : "saved",
	});
	function changeDraft(next: DemoGenreReference[]) {
		setDraft(next);
		setDraftState(next.length ? "value" : "explicit_empty");
		setDraftDirty(true);
	}

	function beginEdit() {
		setDraft(saved.value);
		setDraftState(saved.state);
		setDraftDirty(false);
		setEditing(true);
	}

	function cancelEdit() {
		setDraft(saved.value);
		setDraftState(saved.state);
		setDraftDirty(false);
		setEditing(false);
	}

	function saveDraft() {
		if (!draftDirty || (draftState === "value" && draft.length === 0)) return;
		onRecordingChange((current) =>
			withRecordingGenre(current, draftState, draft),
		);
		setDraftDirty(false);
		setEditing(false);
	}

	return {
		owner: "recording",
		locale,
		editing,
		draftDirty,
		draft: selection,
		selected: selection,
		draftState: editing ? draftState : saved.state,
		savedState: saved.state,
		loadingSaved: false,
		saving: false,
		readError: "",
		saveError: "",
		refreshConflict: false,
		providerGenreBaseline: null,
		picker,
		onChange,
		onEditOrSave: () => (editing ? saveDraft() : beginEdit()),
		onCancel: cancelEdit,
		onClear: () => {
			setDraft([]);
			setDraftState("explicit_empty");
			setDraftDirty(true);
		},
		onInherit: () => {
			setDraft([]);
			setDraftState("absent");
			setDraftDirty(true);
		},
	};
}

/**
 * Demo controller for the independent recording genre card. It owns the local
 * draft and uses the exact recording mutation supplied by the host, so one
 * recording's genre edit reaches every position that shares its identity.
 */
export function RecordingGenreEditor({
	children,
	...props
}: RecordingGenreEditorProps & { children: ReactNode }) {
	const model = useRecordingGenreEditorDemoProvider(props);
	return (
		<GenreMetadataEditorDataBinder model={model}>
			{children}
		</GenreMetadataEditorDataBinder>
	);
}
