import {
	type KeyboardEvent,
	useEffect,
	useId,
	useMemo,
	useRef,
	useState,
} from "react";
import { useI18n } from "../../../../app/i18n";
import type {
	MetadataEditorArtistsView,
	MetadataEditorBasicsView,
	MetadataEditorContributorView,
	MetadataEditorCreditView,
	MetadataEditorExtrasView,
	MetadataEditorLabels,
	MetadataEditorRolesView,
	MetadataEditorRoleView,
	MetadataEditorSectionView,
	MetadataEditorViewProps,
	MetadataEditorWorksMovementView,
} from "../../../../ui/features/library/MetadataEditorView";
import {
	type MetadataFieldDescriptor,
	type MetadataFieldMode,
	type MetadataFieldState,
	metadataFilledSummary,
	metadataListActions,
} from "../../../../ui/features/library/MetadataFieldPresentation";
import {
	focusMetadataAction,
	focusMetadataKey,
} from "../../../../ui/features/library/metadata/metadataEditorFocus";
import {
	type MetadataMessageKey,
	metadataExtraFieldLabel,
	metadataMessage,
} from "../../../../ui/features/library/metadata/metadataFieldMessages";
import { MetadataEditorNoticesView } from "../../../../ui/features/library/metadata/MetadataEditorNoticesView";
import {
	libraryMetadataScalarChoices,
	releaseMetadataEditorMessages,
	validLibraryMetadataPartialDate,
	validLibraryMetadataPositiveNumber,
} from "../../../../ui/features/library/metadata/releaseMetadataEditorMessages";
import type { WorkRowDraft } from "../../../../ui/features/library/WorkRowsView";
import { audioMeasurementKeys } from "./audioMeasurements";
import {
	essentialMetadataFields,
	libraryReleaseEditorFields,
	metadataSummary,
} from "./editorLayout";
import { GenrePicker, type GenreReference } from "./GenrePicker";
import {
	type MetadataContributor,
	type MetadataCredit,
	readMetadataContributors,
	readMetadataCredits,
	readMetadataWorks,
	writeMetadataContributors,
	writeMetadataCredits,
	writeMetadataWorks,
} from "./model";
import {
	type MetadataValidationIssue,
	type MetadataValues,
	metadataValidationMessage,
	validateMetadataIssues,
} from "./projection";
import {
	fieldByKey,
	fieldsForScope,
	type MetadataField,
	type MetadataScope,
	metadataFieldLabel,
	metadataGroupLabel,
} from "./registry";

type ArtistOption = {
	id: string;
	name: string;
	sortName?: string;
	mbid?: string;
};
type Genre = { id?: string; name?: string };
type ExtraField = { key: string; label: string; value: string };
type ContributorDraft = MetadataContributor & {
	uiKey: string;
	isNew?: boolean;
};
export type SaveDetails = {
	credits?: MetadataCredit[];
	genres?: Genre[];
	extraValues?: Record<string, string>;
	fieldStates?: Partial<Record<string, MetadataFieldState>>;
};

export interface MetadataEditorProps {
	label: string;
	scope: MetadataScope;
	values: MetadataValues;
	onSave: (
		values: MetadataValues,
		details?: SaveDetails,
	) => void | Promise<void>;
	artists?: ArtistOption[];
	credits?: MetadataCredit[];
	genres?: Genre[];
	providerGenreBaseline?: { genres: GenreReference[] } | null;
	genresLoaded?: boolean;
	onLoadGenres?: () => void | Promise<void>;
	readOnly?: boolean;
	/** Library scope: save only changed fields, and use the library notices. */
	libraryMetadata?: boolean;
	editing?: boolean;
	hideActions?: boolean;
	onDraftChange?: (values: MetadataValues, details?: SaveDetails) => void;
	requiredKeys?: string[];
	extraFields?: ExtraField[];
	/** Fields that expose the explicit absent/value/explicit_empty selector. */
	fieldStates?: Partial<Record<string, MetadataFieldState>>;
	fieldStateMode?: MetadataFieldMode;
	fieldLabels?: Readonly<Record<string, string>>;
	fieldKinds?: Readonly<Record<string, MetadataFieldDescriptor["kind"]>>;
	fieldHints?: Readonly<Record<string, string>>;
	fieldBaselines?: Readonly<
		Record<
			string,
			{
				display: string;
				notice: string;
				populated: boolean;
			}
		>
	>;
	/** Explicit editor fields for scopes whose shared registry identity differs. */
	fields?: readonly MetadataField[];
	fieldStateDisplay?: {
		absent: string;
		explicitEmpty: string;
		emptyValue: string;
	};
}

const audioMeasurementKeySet = new Set<string>(audioMeasurementKeys);
const linkedArtistKeys = (scope: MetadataScope) =>
	scope === "release"
		? [
				"albumartist",
				"albumartists",
				"musicbrainz_albumartistid",
				"albumartistsort",
				"albumartistssort",
			]
		: [
				"artist",
				"artists",
				"musicbrainz_artistid",
				"artistsort",
				"artistssort",
			];

function rows(values: MetadataValues, key: string) {
	return values[key] ?? [];
}

function savedContributors(
	values: MetadataValues,
	role: string,
	sortKey?: string,
): ContributorDraft[] {
	return readMetadataContributors(values, role).map((row, index) => ({
		...row,
		uiKey: `${role}-${index}`,
		sortName: sortKey ? rows(values, sortKey)[index] : undefined,
	}));
}

function sameContributors(
	baseline: MetadataContributor[],
	draftRows: MetadataContributor[],
) {
	return (
		baseline.length === draftRows.length &&
		baseline.every((row, index) => {
			const draft = draftRows[index];
			return (
				row.name === draft.name &&
				(row.mbid ?? "") === (draft.mbid ?? "") &&
				(row.sortName ?? "") === (draft.sortName ?? "") &&
				(row.instrument ?? "") === (draft.instrument ?? "")
			);
		})
	);
}

function creditsWithMbids(
	credits: MetadataCredit[],
	values: MetadataValues,
	context: "artist" | "albumartist",
): MetadataCredit[] {
	const ids = rows(values, `musicbrainz_${context}id`);
	return credits.map((credit, index) => ({
		...credit,
		...(credit.mbid || !ids[index] ? {} : { mbid: ids[index] }),
	}));
}

function move<T>(items: T[], index: number, delta: number): T[] {
	const next = [...items];
	const target = index + delta;
	[next[index], next[target]] = [next[target], next[index]];
	return next;
}

function creditRows(
	credits: MetadataCredit[],
	artists: ArtistOption[],
	noArtist: string,
	rowKeys: readonly string[],
): MetadataEditorCreditView[] {
	return credits.map((credit, index) => {
		const linked = artists.find((artist) => artist.id === credit.artistId);
		return {
			uiKey: rowKeys[index] ?? `credit-${index}`,
			artistId: credit.artistId,
			creditName: credit.creditName,
			joinPhrase: credit.joinPhrase,
			mbid: credit.mbid ?? "",
			displayName: credit.creditName || linked?.name || noArtist,
			sortName: linked?.sortName ?? "",
		};
	});
}

/**
 * Metadata editor control, draft and normalisation provider.
 *
 * It preserves the controlled/uncontrolled editing contract, form draft,
 * credits/genres/extras/roles/works writes, validation, save/cancel and focus
 * restoration. The companion view owns every piece of markup.
 */
export function useMetadataEditorProvider({
	label,
	scope,
	values,
	onSave,
	artists = [],
	credits: initialCredits,
	genres,
	providerGenreBaseline,
	genresLoaded = true,
	onLoadGenres,
	readOnly = false,
	libraryMetadata = false,
	editing: controlledEditing,
	hideActions = false,
	onDraftChange,
	requiredKeys = [],
	extraFields = [],
	fieldStates,
	fieldStateMode,
	fieldLabels,
	fieldKinds,
	fieldHints,
	fieldBaselines,
	fields: suppliedFields,
	fieldStateDisplay,
}: MetadataEditorProps): MetadataEditorViewProps {
	const { locale } = useI18n();
	const m = (key: MetadataMessageKey) => metadataMessage(locale, key);
	const labels: MetadataEditorLabels = {
		foreignFields: m("label.foreignFields"),
		noCredits: m("state.noCredits"),
		linkedArtist: m("label.linkedArtist"),
		unlinked: m("label.unlinked"),
		creditName: m("label.creditName"),
		artistMusicBrainz: m("label.artistMusicBrainz"),
		joinPhrase: m("label.joinPhrase"),
		artistSort: m("label.artistSort"),
		moveUp: m("action.moveUp"),
		moveDown: m("action.moveDown"),
		remove: m("action.remove"),
		addArtist: m("action.addArtist"),
		contributorName: m("label.contributorName"),
		contributorMusicBrainz: m("label.contributorMusicBrainz"),
		instrument: m("label.instrument"),
		addContributor: m("action.addContributor"),
		addRole: m("label.addRole"),
		selectRole: m("label.selectRole"),
		nameRequired: m("error.required").replace(
			"{field}",
			m("label.contributorName"),
		),
	};
	const [internalEditing, setInternalEditing] = useState(false);
	const editing = controlledEditing ?? internalEditing;
	const controlled = controlledEditing !== undefined;
	const [draft, setDraft] = useState<MetadataValues>({});
	const [draftStates, setDraftStates] = useState<
		Partial<Record<string, MetadataFieldState>>
	>({});
	const [dirty, setDirty] = useState<Set<string>>(new Set());
	const [draftCredits, setDraftCredits] = useState<MetadataCredit[]>([]);
	const [draftCreditKeys, setDraftCreditKeys] = useState<string[]>();
	const nextCreditKey = useRef(0);
	const [draftGenres, setDraftGenres] = useState<Genre[]>([]);
	const [dirtyGenres, setDirtyGenres] = useState(false);
	const [draftExtras, setDraftExtras] = useState<Record<string, string>>({});
	const [dirtyExtras, setDirtyExtras] = useState<Set<string>>(new Set());
	const [error, setError] = useState<
		| {
				kind: "validation";
				issues: MetadataValidationIssue[];
				requiredField?: string;
		  }
		| { kind: "save" }
		| { kind: "external"; message: string }
	>();
	const [saving, setSaving] = useState(false);
	const [contributorDrafts, setContributorDrafts] = useState<
		Record<string, ContributorDraft[]>
	>({});
	const contributorBaseline = useRef(values);
	const contributorIdBase = useId();
	const nextContributor = useRef(0);
	const contributorInputId = (key: string) =>
		`${contributorIdBase}-contributor-${key}`;
	const [workRowKeys, setWorkRowKeys] = useState<string[]>([]);
	const nextAddedWork = useRef(0);
	useEffect(() => {
		if (!editing) {
			setContributorDrafts({});
			setDraftCreditKeys(undefined);
			setWorkRowKeys([]);
			setDraftStates({});
		}
	}, [editing]);
	const actionRef = useRef<HTMLDivElement>(null);
	const editorRef = useRef<HTMLElement>(null);
	const scopedRelease = libraryMetadata && scope === "release";
	const fields = useMemo(
		() =>
			suppliedFields ??
			(scopedRelease ? libraryReleaseEditorFields : fieldsForScope(scope)),
		[suppliedFields, scope, scopedRelease],
	);
	const editableFields = fields.filter(
		(field) => !audioMeasurementKeySet.has(field.key),
	);
	const current = controlled || !editing ? values : draft;
	const controlledValues = useRef(values);
	controlledValues.current = values;
	const stateControlled = (key: string) =>
		Boolean(fieldStates && Object.hasOwn(fieldStates, key));
	const stateFor = (key: string): MetadataFieldState | undefined =>
		(editing ? draftStates[key] : undefined) ?? fieldStates?.[key];
	const changeFieldState = (key: string, next: MetadataFieldState) => {
		setDraftStates((state) => ({ ...state, [key]: next }));
		setError(undefined);
		if (controlled) {
			onDraftChange?.(controlledValues.current, {
				fieldStates: { [key]: next },
			});
			return;
		}
		setDirty((state) => new Set(state).add(key));
	};
	const simpleFieldDescriptor = (
		field: MetadataField,
		readOnlyField: boolean,
	): MetadataFieldDescriptor => {
		const fieldRows = rows(current, field.key);
		const registryList = field.kind === "list" || field.kind === "identity";
		const fieldLabel =
			fieldLabels?.[field.key] ?? metadataFieldLabel(field.key, locale);
		const kind: MetadataFieldDescriptor["kind"] =
			fieldKinds?.[field.key] ??
			(field.kind === "lyrics"
				? "lyrics"
				: field.kind === "integer" ||
						field.kind === "number" ||
						field.kind === "boolean" ||
						field.kind === "advisory" ||
						field.kind === "date"
					? field.kind
					: registryList
						? "list"
						: "text");
		const list = kind === "list";
		const hasState = stateControlled(field.key);
		const libraryScalar = libraryMetadata && hasState;
		const fieldState = hasState ? (stateFor(field.key) ?? "absent") : undefined;
		const baseline =
			fieldState === "absent" ? fieldBaselines?.[field.key] : undefined;
		const displayValue = fieldState
			? fieldState === "absent"
				? (baseline?.display ?? fieldStateDisplay?.absent)
				: fieldState === "explicit_empty"
					? fieldStateDisplay?.explicitEmpty
					: (list ? fieldRows.join("\n") : (fieldRows[0] ?? "")) ||
						fieldStateDisplay?.emptyValue
			: undefined;
		return {
			key: field.key,
			label: fieldLabel,
			kind,
			value: list ? fieldRows : (fieldRows[0] ?? ""),
			editing: !readOnlyField,
			presentation: hasState ? undefined : "control",
			displayValue,
			notices: baseline ? [baseline.notice] : undefined,
			state: fieldState,
			mode: hasState ? fieldStateMode : undefined,
			onStateChange: hasState
				? (next) => changeFieldState(field.key, next)
				: undefined,
			dataKeys: [field.key],
			emptyChoiceLabel:
				!libraryScalar &&
				(field.kind === "boolean" || field.kind === "advisory")
					? m("state.unspecified")
					: undefined,
			choices: libraryScalar
				? libraryMetadataScalarChoices(locale, kind)
				: field.kind === "boolean"
					? [
							{ value: "1", label: m("value.yes") },
							{ value: "0", label: m("value.no") },
						]
					: field.kind === "advisory"
						? [
								{ value: "0", label: m("value.unrated") },
								{ value: "2", label: m("value.clean") },
								{ value: "1", label: m("value.explicit") },
								{ value: "4", label: m("value.explicitLegacy") },
							]
						: undefined,
			hint:
				fieldHints?.[field.key] ??
				(field.kind === "date" ? m("hint.datePrecision") : undefined),
			placeholder:
				field.kind === "date" && !fieldHints?.[field.key]
					? "YYYY[-MM[-DD]]"
					: undefined,
			nativeType:
				!libraryScalar && (field.kind === "integer" || field.kind === "number")
					? "number"
					: "text",
			inputMode:
				libraryScalar && kind === "integer"
					? "numeric"
					: libraryScalar && kind === "number"
						? "decimal"
						: field.kind === "date"
							? "text"
							: undefined,
			listActions: list ? metadataListActions(locale, fieldLabel) : undefined,
			onChange: (next) => {
				const values = Array.isArray(next)
					? next
					: hasState
						? [String(next)]
						: next
							? [next]
							: [];
				setValue(field.key, values, {
					preserveEmpty: hasState && fieldState === "value",
				});
			},
		};
	};
	const setValue = (
		key: string,
		next: string[],
		options?: { preserveEmpty?: boolean },
	) => {
		if (!options?.preserveEmpty && !next.some(Boolean)) next = [];
		if (controlled) {
			controlledValues.current = { ...controlledValues.current, [key]: next };
			onDraftChange?.(controlledValues.current);
			setError(undefined);
			return;
		}
		setDraft((state) => ({ ...state, [key]: next }));
		setDirty((state) => new Set(state).add(key));
		setError(undefined);
	};
	const begin = () => {
		if (controlled) return;
		setContributorDrafts({});
		contributorBaseline.current = values;
		setDraft({ ...values });
		const context = scope === "release" ? "albumartist" : "artist";
		setDraftCredits(
			creditsWithMbids(
				initialCredits ?? readMetadataCredits(values, context),
				values,
				context,
			),
		);
		setDraftGenres(genres ?? rows(values, "genre").map((name) => ({ name })));
		setDirtyGenres(false);
		setDraftExtras(
			Object.fromEntries(extraFields.map(({ key, value }) => [key, value])),
		);
		setDirtyExtras(new Set());
		setDirty(new Set());
		setDraftStates({});
		setError(undefined);
		setInternalEditing(true);
	};
	const cancel = () => {
		if (controlled) return;
		setInternalEditing(false);
		setError(undefined);
		setDraft({});
		setDirty(new Set());
		setDraftStates({});
		setDraftCredits([]);
		setDraftGenres([]);
		setDirtyGenres(false);
		setDraftExtras({});
		setDirtyExtras(new Set());
		setContributorDrafts({});
		focusMetadataAction(actionRef);
	};
	const complete = async (next: MetadataValues, details?: SaveDetails) => {
		setSaving(true);
		setError(undefined);
		try {
			if (details) await onSave(next, details);
			else await onSave(next);
			setInternalEditing(false);
			setDraft({});
			setDirty(new Set());
			setDraftStates({});
			setDraftCredits([]);
			setDraftGenres([]);
			setDirtyGenres(false);
			setDraftExtras({});
			setDirtyExtras(new Set());
		} catch (reason) {
			setError(
				reason instanceof Error
					? { kind: "external", message: reason.message }
					: { kind: "save" },
			);
		} finally {
			setSaving(false);
		}
	};
	const save = () => {
		const missingName = roles
			.flatMap((field) => contributorDrafts[field.role ?? field.key] ?? [])
			.find((row) => row.isNew && !row.name.trim());
		if (missingName) {
			focusMetadataKey(editorRef, contributorInputId(missingName.uiKey));
			return;
		}
		const validatesControlledValue = (key: string) => {
			if (!fieldStates || !Object.hasOwn(fieldStates, key)) return true;
			return (draftStates[key] ?? fieldStates[key]) === "value";
		};
		const changedValues = Object.fromEntries(
			[...dirty]
				.filter((key) => fieldByKey(key)?.scope === scope)
				.filter((key) => !stateControlled(key))
				.filter(validatesControlledValue)
				.map((key) => [key, draft[key] ?? []]),
		) as MetadataValues;
		const errors = validateMetadataIssues(changedValues, scope);
		if (libraryMetadata) {
			for (const key of dirty) {
				if (!stateControlled(key) || !validatesControlledValue(key)) continue;
				const kind =
					fieldKinds?.[key] ?? fields.find((field) => field.key === key)?.kind;
				const value = draft[key]?.[0] ?? "";
				if (
					(kind === "date" && !validLibraryMetadataPartialDate(value)) ||
					((kind === "integer" || kind === "number") &&
						!validLibraryMetadataPositiveNumber(kind, value))
				) {
					errors.unshift({ key, code: kind });
					break;
				}
			}
		}
		const missingRequired = requiredKeys.find(
			(key) => !(draft[key] ?? values[key] ?? []).some((value) => value.trim()),
		);
		if (errors.length || missingRequired) {
			setError({
				kind: "validation",
				issues: errors,
				requiredField: missingRequired,
			});
			const invalidKey = missingRequired ?? errors[0]?.key;
			focusMetadataKey(editorRef, invalidKey);
			return;
		}
		const update: MetadataValues = { ...values };
		for (const key of dirty) if (key in draft) update[key] = [...draft[key]];
		const changedGroups = new Set(
			fields
				.filter((field) => dirty.has(field.key))
				.map((field) => field.group),
		);
		const details: SaveDetails = {};
		const creditDirty = [...changedGroups].some(
			(group) => group === "recording artists" || group === "release artists",
		);
		if (creditDirty) details.credits = draftCredits;
		if (dirtyGenres) details.genres = draftGenres;
		if (dirtyExtras.size)
			details.extraValues = Object.fromEntries(
				[...dirtyExtras].map((key) => [key, draftExtras[key] ?? ""]),
			);
		const fieldStateChanges = Object.fromEntries(
			[...dirty]
				.filter((key) => fieldStates && Object.hasOwn(fieldStates, key))
				.map((key) => [
					key,
					draftStates[key] ?? fieldStates?.[key] ?? "absent",
				]),
		);
		if (Object.keys(fieldStateChanges).length)
			details.fieldStates = fieldStateChanges;
		void complete(update, Object.keys(details).length ? details : undefined);
	};
	const foreignFields = Object.keys(values).filter((key) => {
		if (stateControlled(key)) return false;
		const field = fieldByKey(key);
		return field && field.scope !== scope && values[key]?.some(Boolean);
	});
	const consumed = new Set<string>();
	const sections = new Map<string, MetadataField[]>();
	for (const field of editableFields) {
		if (
			field.kind === "identity" &&
			(field.key === "musicbrainz_artistid" ||
				field.key === "musicbrainz_albumartistid")
		)
			continue;
		if (field.group === "roles" && field.role) {
			if (field.kind === "contributors") {
				const keys = [
					field.key,
					`musicbrainz_${field.role}id`,
					...(field.role === "performer" ? ["performerinstrument"] : []),
				];
				for (const key of keys) consumed.add(key);
			}
			if (field.kind === "identity" || field.key.endsWith("sort"))
				consumed.add(field.key);
		}
		if (field.group === "works" && field.kind === "works") {
			consumed.add("work");
			consumed.add("musicbrainz_workid");
		}
		if (field.group === "movement") consumed.add(field.key);
		if (
			field.group === "release artists" ||
			field.group === "recording artists"
		)
			consumed.add(field.key);
		const sectionGroup =
			field.kind === "identity" && field.group !== "roles"
				? "identifiers"
				: field.group;
		const list = sections.get(sectionGroup) ?? [];
		list.push(field);
		sections.set(sectionGroup, list);
	}
	const artistGroup =
		scope === "release" ? "release artists" : "recording artists";
	const creditKeys = linkedArtistKeys(scope);
	const artistFields = editableFields.filter(
		(field) => field.group === artistGroup,
	);
	const roles = editableFields.filter(
		(field) => field.kind === "contributors" && field.role,
	);
	const remainingSections = [...sections.entries()].filter(
		([group]) =>
			group !== artistGroup &&
			group !== "roles" &&
			group !== "works" &&
			group !== "movement",
	);
	const basicFields = essentialMetadataFields(editableFields, scope);
	const basicKeys = new Set(basicFields.map((field) => field.key));
	for (const field of editableFields)
		if (field.kind === "genres") basicKeys.add(field.key);
	const worksMovementFields = fields.filter(
		(field) => field.group === "works" || field.group === "movement",
	);
	const worksFields = worksMovementFields.filter(
		(field) => field.group === "works",
	);
	const movementFields = worksMovementFields.filter(
		(field) => field.group === "movement",
	);
	const otherSections = remainingSections
		.map(
			([group, groupFields]) =>
				[
					group,
					groupFields.filter(
						(field) =>
							!basicKeys.has(field.key) &&
							field.kind !== "lyrics" &&
							!consumed.has(field.key),
					),
				] as [string, MetadataField[]],
		)
		.filter(([, groupFields]) => groupFields.length > 0);
	const contributorsForRole = (
		role: string,
		sortKey?: string,
	): ContributorDraft[] =>
		(editing ? contributorDrafts[role] : undefined) ??
		savedContributors(current, role, sortKey);
	const activeRoles = roles.filter((field) => {
		const role = field.role ?? field.key;
		return contributorsForRole(role).length > 0;
	});
	const roleOptions = roles.filter((field) => !activeRoles.includes(field));
	const newContributor = (): ContributorDraft => ({
		uiKey: `new-${++nextContributor.current}`,
		name: "",
		isNew: true,
	});
	const addContributor = (role: string, existing: ContributorDraft[]) => {
		const row = newContributor();
		setContributorDrafts((state) => ({ ...state, [role]: [...existing, row] }));
		focusMetadataKey(editorRef, contributorInputId(row.uiKey));
	};
	const editedCredits =
		editing && !controlled
			? draftCredits
			: creditsWithMbids(
					initialCredits ??
						readMetadataCredits(
							current,
							scope === "release" ? "albumartist" : "artist",
						),
					current,
					scope === "release" ? "albumartist" : "artist",
				);
	const creditRowKeys =
		(editing ? draftCreditKeys : undefined) ??
		editedCredits.map((_, index) => `credit-${index}`);
	const writeArtistCredits = (next: MetadataCredit[]) => {
		if (controlled) {
			const written = writeMetadataCredits(
				current,
				scope === "release" ? "albumartist" : "artist",
				next,
			);
			onDraftChange?.(written, { credits: next });
			return;
		}
		setDraftCredits(next);
		const written = writeMetadataCredits(
			current,
			scope === "release" ? "albumartist" : "artist",
			next,
		);
		for (const key of creditKeys.slice(0, 3)) setValue(key, written[key] ?? []);
	};
	const setCredit = (index: number, patch: Partial<MetadataCredit>) => {
		const next = [...editedCredits];
		next[index] = { ...next[index], ...patch };
		if (!editedCredits.length) setDraftCreditKeys(["credit-empty"]);
		writeArtistCredits(next);
	};
	const genresValue: GenreReference[] = (
		editing && !controlled
			? draftGenres
			: genres?.length
				? genres
				: rows(current, "genre").map((name): Genre => ({ name }))
	).map((genre) => ({ ...genre, name: genre.name ?? genre.id ?? "" }));
	const groupHeading = (group: string) => {
		if (group === "main")
			return releaseMetadataEditorMessages[locale].groups.main;
		if (
			libraryMetadata &&
			(group === "position" || group === "recording" || group === "additional")
		)
			return releaseMetadataEditorMessages[locale].groups[group];
		if (scopedRelease && (group === "release" || group === "additional"))
			return releaseMetadataEditorMessages[locale].groups[group];
		return metadataGroupLabel(group, locale);
	};
	const displayedError = error
		? error.kind === "validation"
			? [
					...error.issues.map((issue) =>
						libraryMetadata && issue.code === "date"
							? releaseMetadataEditorMessages[locale].invalidDate
							: libraryMetadata && issue.code === "integer"
								? releaseMetadataEditorMessages[locale].invalidPositiveInteger
								: libraryMetadata && issue.code === "number"
									? releaseMetadataEditorMessages[locale].invalidPositiveNumber
									: metadataValidationMessage(issue, locale),
					),
					...(error.requiredField
						? [
								metadataMessage(locale, "error.required", {
									field: metadataFieldLabel(error.requiredField, locale),
								}),
							]
						: []),
				].join(" ")
			: error.kind === "save"
				? m("error.save")
				: error.message
		: "";
	const roleKeys = fields
		.filter((field) => field.group === "roles")
		.map((field) => field.key);
	const works = readMetadataWorks(current);
	const displayedWorks = works.length || !editing ? works : [{ title: "" }];
	const workRows: WorkRowDraft[] = displayedWorks.map((work, index) => ({
		uiKey: (editing ? workRowKeys[index] : undefined) ?? `work-${index}`,
		title: work.title,
		...(work.mbid !== undefined ? { mbid: work.mbid } : {}),
	}));
	const writeWorks = (next: WorkRowDraft[]) => {
		const written = writeMetadataWorks(current, next);
		setWorkRowKeys(next.map(({ uiKey }) => uiKey));
		setValue("work", written.work);
		setValue("musicbrainz_workid", written.musicbrainz_workid);
	};

	const changeGenreField = (next: GenreReference[]) => {
		if (controlled) {
			onDraftChange?.(
				{
					...values,
					genre: next.map((item) => item.name),
				},
				{ genres: next },
			);
			return;
		}
		setDraftGenres(next);
		setDirtyGenres(true);
		setValue(
			"genre",
			next.map((item) => item.name),
		);
	};
	const changeGenreFallback = (next: GenreReference[]) => {
		if (controlled) {
			onDraftChange?.(values, { genres: next });
			return;
		}
		setDraftGenres(next);
		setDirtyGenres(true);
	};
	const genreField = editableFields.find((field) => field.kind === "genres");
	const showGenre = Boolean(genreField) || genres !== undefined;
	const showBasics = basicFields.length > 0 || showGenre;
	const basics: MetadataEditorBasicsView | undefined = showBasics
		? {
				fields: basicFields.map((field) =>
					simpleFieldDescriptor(
						field,
						!editing || (field.kind === "identity" && field.key !== "asin"),
					),
				),
				genreKey: genreField?.key,
				genreControl: showGenre ? (
					<GenrePicker
						value={genresValue}
						readOnly={!editing}
						loaded={genresLoaded}
						onLoad={onLoadGenres}
						onChange={genreField ? changeGenreField : changeGenreFallback}
						label={
							genreField
								? metadataFieldLabel(genreField.key, locale)
								: undefined
						}
						lifetime={genreField ? `field:${genreField.key}` : "fallback"}
					/>
				) : undefined,
			}
		: undefined;

	const summaryCredits = creditRows(
		editedCredits,
		artists,
		m("state.noArtist"),
		creditRowKeys,
	);
	const compositionCredits = creditRows(
		editedCredits.length
			? editedCredits
			: [{ artistId: "", creditName: "", joinPhrase: "" }],
		artists,
		m("state.noArtist"),
		editedCredits.length ? creditRowKeys : ["credit-empty"],
	);
	const changeCreditArtist = (index: number, selectedId: string) => {
		const credit = (editedCredits.length
			? editedCredits
			: [{ artistId: "", creditName: "", joinPhrase: "" }])[index] ?? {
			artistId: "",
			creditName: "",
			joinPhrase: "",
		};
		const selectedArtist = artists.find((artist) => artist.id === selectedId);
		setCredit(index, {
			artistId: selectedId,
			mbid:
				selectedArtist?.mbid ??
				(selectedId === credit.artistId ? (credit.mbid ?? "") : ""),
			creditName: selectedArtist?.name ?? "",
		});
	};
	const artistsView: MetadataEditorArtistsView | undefined =
		artistFields.length > 0
			? {
					groupLabel: groupHeading(artistGroup),
					compositionLabel: m("label.artistComposition"),
					hint: m("hint.artistIdentity"),
					creditKeys,
					sortFieldKey: scope === "release" ? "albumartistsort" : "artistsort",
					summaryCredits,
					compositionCredits,
					creditCount: editedCredits.length,
					artistOptions: artists.map(({ id, name }) => ({ id, name })),
					editing,
					readOnly,
					showRowActions: !readOnly && editing,
					showAdd: editing,
					onChangeArtist: changeCreditArtist,
					onChangeCreditName: (index, value) =>
						setCredit(index, { creditName: value }),
					onChangeJoinPhrase: (index, value) =>
						setCredit(index, { joinPhrase: value }),
					onMove: (index, delta) => {
						setDraftCreditKeys(move(creditRowKeys, index, delta));
						writeArtistCredits(move(editedCredits, index, delta));
					},
					onRemove: (index) => {
						setDraftCreditKeys(creditRowKeys.filter((_, row) => row !== index));
						writeArtistCredits(editedCredits.filter((_, row) => row !== index));
					},
					onAdd: () => {
						setDraftCreditKeys([
							...creditRowKeys,
							`added-credit-${++nextCreditKey.current}`,
						]);
						writeArtistCredits([
							...editedCredits,
							{ artistId: "", creditName: "", joinPhrase: "" },
						]);
					},
				}
			: undefined;

	const roleSections: MetadataEditorRoleView[] = activeRoles.map(
		(roleField) => {
			const role = roleField.role ?? roleField.key;
			const sortField = fields.find(
				(field) => field.role === role && field.key.endsWith("sort"),
			);
			const roleContributors = contributorsForRole(role, sortField?.key);
			const idKey = `musicbrainz_${role}id`;
			const writeRole = (next: ContributorDraft[]) => {
				setContributorDrafts((state) => ({ ...state, [role]: next }));
				const ready = next.filter((row) => !row.isNew || row.name.trim());
				const baseline = controlled ? values : contributorBaseline.current;
				const unchanged = sameContributors(
					savedContributors(baseline, role, sortField?.key),
					ready,
				);
				if (controlled && unchanged) return;

				const written = writeMetadataContributors(current, role, ready);
				if (sortField)
					written[sortField.key] = ready.map((item) => item.sortName ?? "");
				if (controlled) {
					if (role !== "performer" && current.performerinstrument !== undefined)
						written.performerinstrument = current.performerinstrument;
					controlledValues.current = written;
					onDraftChange?.(written);
					setError(undefined);
					return;
				}

				const keys = [
					roleField.key,
					idKey,
					...(sortField ? [sortField.key] : []),
				];
				setDraft((state) => {
					const updated = { ...state };
					for (const key of keys) {
						const next = unchanged ? baseline[key] : (written[key] ?? []);
						if (next === undefined) delete updated[key];
						else updated[key] = next;
					}
					if (role === "performer") {
						if (unchanged && baseline.performerinstrument !== undefined)
							updated.performerinstrument = baseline.performerinstrument;
						else delete updated.performerinstrument;
					}
					return updated;
				});
				setDirty((state) => {
					const updated = new Set(state);
					for (const key of keys) {
						if (unchanged) updated.delete(key);
						else updated.add(key);
					}
					return updated;
				});
				setError(undefined);
			};
			const idField = fields.find(
				(field) => field.role === role && field.kind === "identity",
			);
			const contributors: MetadataEditorContributorView[] =
				roleContributors.map((contributor) => ({
					uiKey: contributor.uiKey,
					name: contributor.name,
					nameInputId: contributorInputId(contributor.uiKey),
					nameRequired: Boolean(contributor.isNew),
					nameError:
						contributor.isNew && !contributor.name.trim()
							? labels.nameRequired
							: undefined,
					...(contributor.mbid !== undefined ? { mbid: contributor.mbid } : {}),
					...(contributor.sortName !== undefined
						? { sortName: contributor.sortName }
						: {}),
					...(contributor.instrument !== undefined
						? { instrument: contributor.instrument }
						: {}),
					nameKey: roleField.key,
					...(idField ? { mbidKey: idField.key } : {}),
					...(sortField ? { sortKey: sortField.key } : {}),
					...(sortField
						? { sortLabel: metadataFieldLabel(sortField.key, locale) }
						: {}),
					showInstrument: role === "performer",
				}));
			return {
				role,
				label: metadataFieldLabel(roleField.key, locale),
				contributors,
				contributorCount: roleContributors.length,
				editing,
				onChange: (index, patch) => {
					const next = [...roleContributors];
					next[index] = { ...next[index], ...patch };
					writeRole(next);
				},
				onMove: (index, delta) =>
					writeRole(move(roleContributors, index, delta)),
				onRemove: (index) =>
					writeRole(roleContributors.filter((_, row) => row !== index)),
				onAdd: () => addContributor(role, roleContributors),
			};
		},
	);
	const rolesView: MetadataEditorRolesView | undefined =
		roles.length > 0
			? {
					label: groupHeading("roles"),
					summary: metadataSummary(
						fields.filter((field) => field.group === "roles"),
						current,
						locale,
					),
					dataKeys: roleKeys,
					roles: roleSections,
					roleOptions: roleOptions.map((field) => ({
						role: field.role ?? field.key,
						label: metadataFieldLabel(field.key, locale),
					})),
					addRoleLabel: m("label.addRole"),
					selectRoleLabel: m("label.selectRole"),
					editing,
					onAddRole: (role) => addContributor(role, []),
				}
			: undefined;

	const worksMovement: MetadataEditorWorksMovementView | undefined =
		worksMovementFields.length > 0
			? {
					label: m("group.worksMovement"),
					summary: metadataSummary(worksMovementFields, current, locale),
					worksLabel: groupHeading("works"),
					movementLabel: groupHeading("movement"),
					worksDataKeys: worksFields.length
						? ["work", "musicbrainz_workid"]
						: undefined,
					movementDataKeys: movementFields.map((field) => field.key),
					works: worksFields.length
						? {
								rows: workRows,
								editing,
								disabled: saving,
								onChange: writeWorks,
								onAdd: () =>
									writeWorks([
										...(works.length ? workRows : []),
										{
											title: "",
											uiKey: `new-work-${++nextAddedWork.current}`,
										},
									]),
							}
						: undefined,
					movement: movementFields.length
						? {
								value: {
									name: rows(current, "movementname")[0] ?? "",
									count: rows(current, "movementtotal")[0] ?? "",
									number: rows(current, "movement")[0] ?? "",
								},
								editing,
								disabled: saving,
								onChange: (key, value) => {
									const tags = {
										name: "movementname",
										count: "movementtotal",
										number: "movement",
									} as const;
									setValue(tags[key], value ? [value] : []);
								},
							}
						: undefined,
				}
			: undefined;

	const sectionsView: MetadataEditorSectionView[] = otherSections.map(
		([group, sectionFields]) => ({
			key: group,
			label: groupHeading(group),
			summary: libraryMetadata
				? metadataFilledSummary(
						locale,
						sectionFields.filter(({ key }) => {
							if (!stateControlled(key)) return current[key]?.some(Boolean);
							const state = stateFor(key);
							return state === "value"
								? current[key]?.some(Boolean) ||
										(fieldKinds?.[key] === "boolean" && current[key]?.length)
								: state === "absent" && fieldBaselines?.[key]?.populated;
						}).length,
					)
				: metadataSummary(sectionFields, current, locale),
			fields: sectionFields
				.filter(
					(field) =>
						field.key !== "musicbrainz_artistid" &&
						field.key !== "musicbrainz_albumartistid",
				)
				.map((field) =>
					simpleFieldDescriptor(
						field,
						!editing || (field.kind === "identity" && field.key !== "asin"),
					),
				),
		}),
	);
	const lyricsView: MetadataEditorSectionView[] = fields
		.filter((field) => field.kind === "lyrics")
		.map((field) => ({
			key: field.key,
			label: m("group.lyrics"),
			summary: rows(current, field.key).some(Boolean)
				? m("state.filled")
				: m("state.empty"),
			fields: [simpleFieldDescriptor(field, !editing)],
		}));
	const extrasView: MetadataEditorExtrasView | undefined =
		extraFields.length > 0
			? {
					label: m("group.extraTags"),
					summary: `${extraFields.filter((field) => field.value).length} ${m("state.filled")}`,
					fields: extraFields.map(
						(field): MetadataFieldDescriptor => ({
							key: field.key,
							label: metadataExtraFieldLabel(field.key, field.label, locale),
							kind: "text",
							presentation: "control",
							value: editing ? (draftExtras[field.key] ?? "") : field.value,
							editing,
							onChange: (next) => {
								if (typeof next !== "string") return;
								setDraftExtras((state) => ({
									...state,
									[field.key]: next,
								}));
								setDirtyExtras((state) => new Set(state).add(field.key));
								setError(undefined);
							},
						}),
					),
				}
			: undefined;

	const onKeyDown = (event: KeyboardEvent<HTMLElement>) => {
		if (!controlled && editing && !saving && event.key === "Escape") {
			event.preventDefault();
			event.stopPropagation();
			cancel();
		}
	};

	return {
		locale,
		labels,
		label,
		editing,
		saving,
		readOnly,
		editOrSaveDisabled:
			libraryMetadata && editing
				? readOnly || (!dirty.size && !dirtyGenres && !dirtyExtras.size)
				: readOnly,
		hideActions,
		editorRef,
		actionsRef: actionRef,
		onEditOrSave: editing ? save : begin,
		onCancel: cancel,
		onKeyDown,
		showForeignFieldsNotice: foreignFields.length > 0,
		basics,
		providerGenreBaseline,
		artists: artistsView,
		roles: rolesView,
		worksMovement,
		sections: sectionsView,
		lyrics: lyricsView,
		extras: extrasView,
		error: libraryMetadata ? undefined : displayedError || undefined,
		notices: libraryMetadata ? (
			<MetadataEditorNoticesView
				locale={locale}
				loading={false}
				saving={saving}
				readError=""
				saveError={displayedError}
				refreshConflict={false}
				onRetry={() => {}}
			/>
		) : undefined,
	};
}
