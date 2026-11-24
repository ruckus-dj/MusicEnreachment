import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "../components/ui";
import {
	ContributorRoleSectionView,
	ContributorRowView,
} from "../features/library/ContributorRowView";
import { MetadataCardActions } from "../features/library/MetadataCardActions";
import {
	MetadataBasicsSection,
	type MetadataFieldDescriptor,
	MetadataFieldGroup,
	MetadataFieldsGrid,
	metadataListActions,
} from "../features/library/MetadataFieldPresentation";
import { MovementFieldsView } from "../features/library/MovementFieldsView";
import { ProviderGenreBaselineRow } from "../features/library/ProviderGenreBaselineRow";
import {
	type WorkRowDraft,
	WorkRowsView,
} from "../features/library/WorkRowsView";
import { WorksMovementSectionView } from "../features/library/WorksMovementSectionView";
import { useI18n } from "../i18n";
import { essentialMetadataFields, metadataSummary } from "./editorLayout";
import { GenrePicker, type GenreReference } from "./GenrePicker";
import {
	type MetadataMessageKey,
	metadataExtraFieldLabel,
	metadataMessage,
} from "./messages";
import { focusMetadataAction, focusMetadataKey } from "./metadataEditorFocus";
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
type SaveDetails = {
	credits?: MetadataCredit[];
	genres?: Genre[];
	extraValues?: Record<string, string>;
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
	editing?: boolean;
	hideActions?: boolean;
	onDraftChange?: (values: MetadataValues, details?: SaveDetails) => void;
	requiredKeys?: string[];
	extraFields?: ExtraField[];
}

import { audioMeasurementKeys } from "./audioMeasurements";

const inputClass =
	"h-8 min-w-0 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] outline-none focus-visible:ring-2 focus-visible:ring-[var(--focus)] read-only:border-transparent read-only:bg-transparent";
const disclosureSummaryClass =
	"w-full cursor-pointer rounded border border-[var(--line)] p-2 text-sm font-semibold hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";
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

function readRoleContributors(
	values: MetadataValues,
	role: string,
): MetadataContributor[] {
	return readMetadataContributors(values, role);
}

function writeRoleContributors(
	values: MetadataValues,
	role: string,
	contributors: MetadataContributor[],
): MetadataValues {
	return writeMetadataContributors(values, role, contributors);
}

function marker(keys: string[]) {
	return { "data-metadata-keys": keys.join(",") };
}

function move<T>(items: T[], index: number, delta: number): T[] {
	const next = [...items];
	const target = index + delta;
	[next[index], next[target]] = [next[target], next[index]];
	return next;
}

function KeyInput({
	fieldKey,
	label,
	value,
	readOnly,
	onChange,
	type = "text",
}: {
	fieldKey?: string;
	label: string;
	value: string;
	readOnly: boolean;
	onChange: (value: string) => void;
	type?: string;
}) {
	return (
		<label className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
			<span>{label}</span>
			<input
				{...(fieldKey ? marker([fieldKey]) : {})}
				aria-label={label}
				className={inputClass}
				readOnly={readOnly}
				type={type}
				value={value}
				onChange={(event) => onChange(event.target.value)}
			/>
		</label>
	);
}

export function MetadataEditor({
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
	editing: controlledEditing,
	hideActions = false,
	onDraftChange,
	requiredKeys = [],
	extraFields = [],
}: MetadataEditorProps) {
	const { locale } = useI18n();
	const m = (key: MetadataMessageKey) => metadataMessage(locale, key);
	const [internalEditing, setInternalEditing] = useState(false);
	const editing = controlledEditing ?? internalEditing;
	const controlled = controlledEditing !== undefined;
	const [draft, setDraft] = useState<MetadataValues>({});
	const [dirty, setDirty] = useState<Set<string>>(new Set());
	const [draftCredits, setDraftCredits] = useState<MetadataCredit[]>([]);
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
	const [addedRoles, setAddedRoles] = useState<string[]>([]);
	const [workRowKeys, setWorkRowKeys] = useState<string[]>([]);
	const nextAddedWork = useRef(0);
	useEffect(() => {
		if (!editing) {
			setAddedRoles([]);
			setWorkRowKeys([]);
		}
	}, [editing]);
	const actionRef = useRef<HTMLDivElement>(null);
	const editorRef = useRef<HTMLElement>(null);
	const fields = useMemo(() => fieldsForScope(scope), [scope]);
	const editableFields = fields.filter(
		(field) => !audioMeasurementKeySet.has(field.key),
	);
	const current = controlled || !editing ? values : draft;
	const controlledValues = useRef(values);
	controlledValues.current = values;
	const simpleFieldDescriptor = (
		field: MetadataField,
		readOnly: boolean,
	): MetadataFieldDescriptor => {
		const fieldRows = rows(current, field.key);
		const list = field.kind === "list" || field.kind === "identity";
		const label = metadataFieldLabel(field.key, locale);
		const kind: MetadataFieldDescriptor["kind"] =
			field.kind === "lyrics"
				? "lyrics"
				: field.kind === "integer" ||
						field.kind === "number" ||
						field.kind === "boolean" ||
						field.kind === "advisory" ||
						field.kind === "date"
					? field.kind
					: list
						? "list"
						: "text";
		return {
			key: field.key,
			label,
			kind,
			value: list ? fieldRows : (fieldRows[0] ?? ""),
			editing: !readOnly,
			presentation: "control",
			dataKeys: [field.key],
			emptyChoiceLabel:
				field.kind === "boolean" || field.kind === "advisory"
					? m("state.unspecified")
					: undefined,
			choices:
				field.kind === "boolean"
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
			hint: field.kind === "date" ? m("hint.datePrecision") : undefined,
			placeholder: field.kind === "date" ? "YYYY[-MM[-DD]]" : undefined,
			nativeType:
				field.kind === "integer" || field.kind === "number" ? "number" : "text",
			inputMode: field.kind === "date" ? "text" : undefined,
			listActions: list ? metadataListActions(locale, label) : undefined,
			onChange: (next) =>
				setValue(field.key, Array.isArray(next) ? next : next ? [next] : []),
		};
	};
	const setValue = (key: string, next: string[]) => {
		if (!next.some(Boolean)) next = [];
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
		setAddedRoles([]);
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
		setError(undefined);
		setInternalEditing(true);
	};
	const cancel = () => {
		if (controlled) return;
		setInternalEditing(false);
		setError(undefined);
		setDraft({});
		setDirty(new Set());
		setDraftCredits([]);
		setDraftGenres([]);
		setDirtyGenres(false);
		setDraftExtras({});
		setDirtyExtras(new Set());
		setAddedRoles([]);
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
		const changedValues = Object.fromEntries(
			[...dirty]
				.filter((key) => fieldByKey(key)?.scope === scope)
				.map((key) => [key, draft[key] ?? []]),
		) as MetadataValues;
		const errors = validateMetadataIssues(changedValues, scope);
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
		void complete(update, Object.keys(details).length ? details : undefined);
	};
	const foreignFields = Object.keys(values).filter((key) => {
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
				keys.forEach((key) => consumed.add(key));
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
	const roleOptions = roles.filter(
		(field) => !addedRoles.includes(field.role ?? field.key),
	);
	const activeRoles = roles.filter((field) => {
		const role = field.role ?? field.key;
		return (
			addedRoles.includes(role) ||
			readRoleContributors(current, role).length > 0
		);
	});
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
		writeArtistCredits(next);
	};
	const genresValue: GenreReference[] = (
		editing && !controlled
			? draftGenres
			: genres?.length
				? genres
				: rows(current, "genre").map((name): Genre => ({ name }))
	).map((genre) => ({ ...genre, name: genre.name ?? genre.id ?? "" }));
	const groupHeading = (group: string) => metadataGroupLabel(group, locale);
	const displayedError = error
		? error.kind === "validation"
			? [
					...error.issues.map((issue) =>
						metadataValidationMessage(issue, locale),
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
	const workRows = displayedWorks.map((work, index) => ({
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

	return (
		<section
			ref={editorRef}
			aria-label={label}
			className="grid min-w-0 gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3"
			onKeyDown={(event) => {
				if (!controlled && editing && !saving && event.key === "Escape") {
					event.preventDefault();
					event.stopPropagation();
					cancel();
				}
			}}
		>
			<MetadataCardActions
				locale={locale}
				title={label}
				editing={editing}
				saving={saving}
				editOrSaveDisabled={readOnly}
				hidden={hideActions}
				actionsRef={actionRef}
				onEditOrSave={editing ? save : begin}
				onCancel={cancel}
			/>
			{foreignFields.length > 0 && (
				<p className="m-0 text-xs text-[var(--muted)]">
					{m("label.foreignFields")}
				</p>
			)}
			{(basicFields.length > 0 ||
				editableFields.some((field) => field.kind === "genres") ||
				genres !== undefined) && (
				<MetadataBasicsSection
					locale={locale}
					fields={basicFields.map((field) =>
						simpleFieldDescriptor(
							field,
							!editing || (field.kind === "identity" && field.key !== "asin"),
						),
					)}
				>
					{editableFields
						.filter((field) => field.kind === "genres")
						.map((field) => (
							<div key={field.key} {...marker([field.key])}>
								<GenrePicker
									label={metadataFieldLabel(field.key, locale)}
									readOnly={!editing}
									loaded={genresLoaded}
									onLoad={onLoadGenres}
									value={genresValue}
									onChange={(next) => {
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
									}}
								/>
							</div>
						))}
					{genres !== undefined &&
						!editableFields.some((field) => field.kind === "genres") && (
							<GenrePicker
								readOnly={!editing}
								loaded={genresLoaded}
								onLoad={onLoadGenres}
								value={genresValue}
								onChange={(next) => {
									if (controlled) {
										onDraftChange?.(values, { genres: next });
										return;
									}
									setDraftGenres(next);
									setDirtyGenres(true);
								}}
							/>
						)}
				</MetadataBasicsSection>
			)}
			<ProviderGenreBaselineRow
				baseline={providerGenreBaseline}
				locale={locale}
			/>
			{artistFields.length > 0 && (
				<div className="grid gap-2 rounded border border-[var(--line)] p-2">
					<div className="flex flex-wrap items-center gap-2 text-sm">
						<strong>{groupHeading(artistGroup)}</strong>
						<span className="flex flex-wrap items-center text-[var(--muted)]">
							{editedCredits.length ? (
								editedCredits.map((credit, index) => {
									const linked = artists.find(
										(artist) => artist.id === credit.artistId,
									);
									return (
										<span key={`${credit.artistId}-${index}`}>
											{credit.creditName || linked?.name || m("state.noArtist")}
											{credit.joinPhrase}
										</span>
									);
								})
							) : (
								<span>{m("state.noCredits")}</span>
							)}
						</span>
					</div>
					<details>
						<summary className="cursor-pointer text-xs text-[var(--muted)]">
							{m("label.artistComposition")}
						</summary>
						<section
							{...marker(creditKeys)}
							className="grid gap-2 rounded border border-[var(--line)] p-2"
						>
							{(editedCredits.length
								? editedCredits
								: [{ artistId: "", creditName: "", joinPhrase: "" }]
							).map((credit, index) => {
								const linked = artists.find(
									(artist) => artist.id === credit.artistId,
								);
								return (
									<div
										key={`${credit.artistId}-${index}`}
										className="grid min-w-0 gap-2 sm:grid-cols-2 lg:grid-cols-4"
									>
										<label className="grid gap-1 text-xs text-[var(--muted)]">
											<span>{m("label.linkedArtist")}</span>
											<select
												{...marker([creditKeys[0]])}
												aria-label={`${m("label.linkedArtist")} ${index + 1}`}
												className={inputClass}
												disabled={!editing || !artists.length}
												value={credit.artistId}
												onChange={(event) => {
													const selectedId = event.target.value;
													const selectedArtist = artists.find(
														(artist) => artist.id === selectedId,
													);
													setCredit(index, {
														artistId: selectedId,
														mbid:
															selectedArtist?.mbid ??
															(selectedId === credit.artistId
																? (credit.mbid ?? "")
																: ""),
														creditName: selectedArtist?.name ?? "",
													});
												}}
											>
												<option value="">{m("label.unlinked")}</option>
												{artists.map((artist) => (
													<option key={artist.id} value={artist.id}>
														{artist.name}
													</option>
												))}
											</select>
										</label>
										<KeyInput
											fieldKey={creditKeys[0]}
											label={m("label.creditName")}
											value={credit.creditName}
											readOnly={!editing}
											onChange={(creditName) =>
												setCredit(index, { creditName })
											}
										/>
										<KeyInput
											fieldKey={creditKeys[2]}
											label={m("label.artistMusicBrainz")}
											value={credit.mbid ?? ""}
											readOnly
											onChange={() => undefined}
										/>
										<KeyInput
											fieldKey="credit-joinphrase"
											label={m("label.joinPhrase")}
											value={credit.joinPhrase}
											readOnly={!editing}
											onChange={(joinPhrase) =>
												setCredit(index, { joinPhrase })
											}
										/>
										<KeyInput
											fieldKey={
												scope === "release" ? "albumartistsort" : "artistsort"
											}
											label={m("label.artistSort")}
											value={linked?.sortName ?? ""}
											readOnly
											onChange={() => undefined}
										/>
										{!readOnly && editing && (
											<div className="flex gap-1">
												<Button
													aria-label={`${m("action.moveUp")} ${m("label.artistSort")} ${index + 1}`}
													isDisabled={readOnly || !editing || index === 0}
													onPress={() =>
														writeArtistCredits(move(editedCredits, index, -1))
													}
												>
													↑
												</Button>
												<Button
													aria-label={`${m("action.moveDown")} ${m("label.artistSort")} ${index + 1}`}
													isDisabled={
														readOnly ||
														!editing ||
														index === editedCredits.length - 1
													}
													onPress={() =>
														writeArtistCredits(move(editedCredits, index, 1))
													}
												>
													↓
												</Button>
												<Button
													aria-label={`${m("action.remove")} ${m("group.releaseArtists")} ${index + 1}`}
													isDisabled={readOnly || !editing}
													onPress={() => {
														const next = editedCredits.filter(
															(_, row) => row !== index,
														);
														writeArtistCredits(next);
													}}
												>
													{m("action.remove")}
												</Button>
											</div>
										)}
									</div>
								);
							})}
							{editing && (
								<Button
									isDisabled={!editing}
									className="justify-self-start"
									onPress={() => {
										const next = [
											...editedCredits,
											{ artistId: "", creditName: "", joinPhrase: "" },
										];
										writeArtistCredits(next);
									}}
								>
									{m("action.addArtist")}
								</Button>
							)}
							<p className="m-0 text-xs text-[var(--muted)]">
								{m("hint.artistIdentity")}
							</p>
						</section>
					</details>
				</div>
			)}
			<div data-metadata-sections className="grid min-w-0 grid-cols-1 gap-2">
				{roles.length > 0 && (
					<details className="min-w-0">
						<summary className={disclosureSummaryClass}>
							{groupHeading("roles")} ·{" "}
							{metadataSummary(
								fields.filter((field) => field.group === "roles"),
								current,
								locale,
							)}
						</summary>
						<section
							{...marker(roleKeys)}
							className="grid gap-2 rounded border border-[var(--line)] p-2"
						>
							{activeRoles.map((roleField) => {
								const role = roleField.role ?? roleField.key;
								const sortField = fields.find(
									(field) => field.role === role && field.key.endsWith("sort"),
								);
								const roleContributors = readRoleContributors(
									current,
									role,
								).map((contributor, index) => ({
									...contributor,
									...(sortField?.key && rows(current, sortField.key)[index]
										? { sortName: rows(current, sortField.key)[index] }
										: {}),
								}));
								const idKey = `musicbrainz_${role}id`;
								const writeRole = (next: MetadataContributor[]) => {
									const written = writeRoleContributors(current, role, next);
									setValue(roleField.key, written[roleField.key] ?? []);
									setValue(idKey, written[idKey] ?? []);
									if (role === "performer")
										setDraft((state) => {
											const cleaned = { ...state };
											delete cleaned.performerinstrument;
											return cleaned;
										});
									if (sortField)
										setValue(
											sortField.key,
											next.map((item) => item.sortName ?? ""),
										);
								};
								const idField = fields.find(
									(field) => field.role === role && field.kind === "identity",
								);
								return (
									<ContributorRoleSectionView
										key={role}
										label={metadataFieldLabel(roleField.key, locale)}
									>
										{(roleContributors.length
											? roleContributors
											: [{ name: "" }]
										).map((contributor, index) => (
											<div
												key={`${role}-${index}`}
												className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4"
											>
												<ContributorRowView
													items={[
														{
															key: "name",
															label: m("label.contributorName"),
															value: contributor.name,
															dataKeys: [roleField.key],
															readOnly: !editing,
															onChange: (name: string) => {
																const next = [...roleContributors];
																next[index] = { ...next[index], name };
																writeRole(next);
															},
														},
														...(idField
															? [
																	{
																		key: "mbid",
																		label: m("label.contributorMusicBrainz"),
																		value: contributor.mbid ?? "",
																		dataKeys: [idField.key],
																		readOnly: !editing,
																		onChange: (mbid: string) => {
																			const next = [...roleContributors];
																			next[index] = {
																				...next[index],
																				mbid,
																			};
																			writeRole(next);
																		},
																	},
																]
															: []),
														...(sortField
															? [
																	{
																		key: "sort",
																		label: metadataFieldLabel(
																			sortField.key,
																			locale,
																		),
																		value: contributor.sortName ?? "",
																		dataKeys: [sortField.key],
																		readOnly: !editing,
																		onChange: (sortName: string) => {
																			const next = [...roleContributors];
																			next[index] = {
																				...next[index],
																				sortName,
																			};
																			writeRole(next);
																		},
																	},
																]
															: []),
														...(role === "performer"
															? [
																	{
																		key: "instrument",
																		label: m("label.instrument"),
																		value: contributor.instrument ?? "",
																		readOnly: !editing,
																		onChange: (instrument: string) => {
																			const next = [...roleContributors];
																			next[index] = {
																				...next[index],
																				instrument,
																			};
																			writeRole(next);
																		},
																	},
																]
															: []),
													]}
													actions={
														editing
															? {
																	moveUp: {
																		label: `${m("action.moveUp")}: ${metadataFieldLabel(roleField.key, locale)} ${index + 1}`,
																		disabled: !editing || index === 0,
																		onPress: () => {
																			writeRole(
																				move(roleContributors, index, -1),
																			);
																		},
																	},
																	moveDown: {
																		label: `${m("action.moveDown")}: ${metadataFieldLabel(roleField.key, locale)} ${index + 1}`,
																		disabled:
																			!editing ||
																			index === roleContributors.length - 1,
																		onPress: () => {
																			writeRole(
																				move(roleContributors, index, 1),
																			);
																		},
																	},
																	remove: {
																		label: `${m("action.remove")} ${metadataFieldLabel(roleField.key, locale)} ${index + 1}`,
																		text: m("action.remove"),
																		disabled: !editing,
																		onPress: () => {
																			writeRole(
																				roleContributors.filter(
																					(_, row) => row !== index,
																				),
																			);
																		},
																	},
																}
															: undefined
													}
												/>
											</div>
										))}
										{editing && (
											<Button
												isDisabled={!editing}
												className="justify-self-start"
												onPress={() => {
													writeRole([...roleContributors, { name: "" }]);
												}}
											>
												{m("action.addContributor")}
											</Button>
										)}
									</ContributorRoleSectionView>
								);
							})}
							{editing && roleOptions.length > 0 && (
								<label className="grid max-w-sm gap-1 text-xs text-[var(--muted)]">
									<span>{m("label.addRole")}</span>
									<select
										aria-label={m("label.addRole")}
										className={inputClass}
										value=""
										onChange={(event) => {
											if (event.target.value)
												setAddedRoles((items) => [
													...items,
													event.target.value,
												]);
										}}
									>
										<option value="">{m("label.selectRole")}</option>
										{roleOptions.map((field) => (
											<option key={field.role} value={field.role}>
												{metadataFieldLabel(field.key, locale)}
											</option>
										))}
									</select>
								</label>
							)}
						</section>
					</details>
				)}
				{worksMovementFields.length > 0 && (
					<WorksMovementSectionView
						label={m("group.worksMovement")}
						summary={metadataSummary(worksMovementFields, current, locale)}
						worksLabel={groupHeading("works")}
						movementLabel={groupHeading("movement")}
						worksDataKeys={
							worksFields.length ? ["work", "musicbrainz_workid"] : undefined
						}
						movementDataKeys={movementFields.map((field) => field.key)}
						works={
							worksFields.length ? (
								<WorkRowsView
									locale={locale}
									rows={workRows}
									editing={editing}
									disabled={saving}
									editableMbid
									titleDataKey="work"
									mbidDataKey="musicbrainz_workid"
									onChange={writeWorks}
									onAdd={() =>
										writeWorks([
											...(works.length ? workRows : []),
											{
												title: "",
												uiKey: `new-work-${++nextAddedWork.current}`,
											},
										])
									}
								/>
							) : undefined
						}
						movement={
							movementFields.length ? (
								<MovementFieldsView
									locale={locale}
									value={{
										name: rows(current, "movementname")[0] ?? "",
										count: rows(current, "movementtotal")[0] ?? "",
										number: rows(current, "movement")[0] ?? "",
									}}
									editing={editing}
									disabled={saving}
									onChange={(key, value) => {
										const tags = {
											name: "movementname",
											count: "movementtotal",
											number: "movement",
										} as const;
										setValue(tags[key], value ? [value] : []);
									}}
								/>
							) : undefined
						}
					/>
				)}
				{otherSections.map(([group, sectionFields]) => (
					<MetadataFieldGroup
						key={group}
						label={groupHeading(group)}
						summary={metadataSummary(sectionFields, current, locale)}
						columns={4}
						heading
						fields={sectionFields
							.filter(
								(field) =>
									field.key !== "musicbrainz_artistid" &&
									field.key !== "musicbrainz_albumartistid",
							)
							.map((field) =>
								simpleFieldDescriptor(
									field,
									!editing ||
										(field.kind === "identity" && field.key !== "asin"),
								),
							)}
					/>
				))}
				{fields
					.filter((field) => field.kind === "lyrics")
					.map((field) => (
						<MetadataFieldGroup
							key={field.key}
							label={m("group.lyrics")}
							summary={
								rows(current, field.key).some(Boolean)
									? m("state.filled")
									: m("state.empty")
							}
							columns={1}
							fields={[simpleFieldDescriptor(field, !editing)]}
						/>
					))}
				{extraFields.length > 0 && (
					<MetadataFieldGroup
						label={m("group.extraTags")}
						summary={`${extraFields.filter((field) => field.value).length} ${m("state.filled")}`}
						columns={4}
						fields={extraFields.map((field) => ({
							key: field.key,
							label: metadataExtraFieldLabel(field.key, field.label, locale),
							kind: "text",
							presentation: "control",
							value: editing ? (draftExtras[field.key] ?? "") : field.value,
							editing,
							onChange: (next) => {
								if (typeof next !== "string") return;
								setDraftExtras((state) => ({ ...state, [field.key]: next }));
								setDirtyExtras((state) => new Set(state).add(field.key));
								setError(undefined);
							},
						}))}
					/>
				)}
			</div>
			{displayedError && (
				<p role="alert" className="m-0 text-sm text-[var(--error)]">
					{displayedError}
				</p>
			)}
		</section>
	);
}
