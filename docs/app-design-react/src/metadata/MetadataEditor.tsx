import { useEffect, useMemo, useRef, useState } from "react";
import { useI18n } from "../i18n";
import { Button, Dialog, Heading, Modal } from "../ui";
import { essentialMetadataFields, metadataSummary } from "./editorLayout";
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
	validateMetadataIssues,
	metadataValidationMessage,
} from "./projection";
import {
	fieldByKey,
	fieldsForScope,
	metadataFieldLabel,
	metadataGroupLabel,
	type MetadataField,
	type MetadataScope,
} from "./registry";
import {
	metadataExtraFieldLabel,
	metadataMessage,
	type MetadataMessageKey,
} from "./messages";

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
	impact?: string[];
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

function SimpleField({
	field,
	locale,
	m,
	value,
	readOnly,
	onChange,
}: {
	field: MetadataField;
	locale: "en" | "ru";
	m: (key: MetadataMessageKey) => string;
	value: string[];
	readOnly: boolean;
	onChange: (value: string[]) => void;
}) {
	const label = metadataFieldLabel(field.key, locale);
	const scalar = [
		"text",
		"date",
		"integer",
		"number",
		"boolean",
		"advisory",
		"lyrics",
	].includes(field.kind);
	const type =
		field.kind === "date"
			? "text"
			: field.kind === "integer" || field.kind === "number"
				? "number"
				: "text";
	const values = value.length ? value : [""];
	return (
		<div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
			<span>{label}</span>
			{scalar ? (
				<>
					{field.kind === "boolean" || field.kind === "advisory" ? (
						<select
							{...marker([field.key])}
							aria-label={label}
							disabled={readOnly}
							className={inputClass}
							value={value[0] ?? ""}
							onChange={(event) =>
								onChange(event.target.value ? [event.target.value] : [])
							}
						>
							<option value="">{m("state.unspecified")}</option>
							{(field.kind === "boolean"
								? [
										["1", m("value.yes")],
										["0", m("value.no")],
									]
								: [
										["0", m("value.unrated")],
										["2", m("value.clean")],
										["1", m("value.explicit")],
										["4", m("value.explicitLegacy")],
									]
							).map(([v, label]) => (
								<option key={v} value={v}>
									{label}
								</option>
							))}
						</select>
					) : field.kind === "lyrics" ? (
						<textarea
							{...marker([field.key])}
							aria-label={label}
							readOnly={readOnly}
							className={`${inputClass} h-24 resize-y py-1`}
							value={value[0] ?? ""}
							onChange={(event) =>
								onChange(event.target.value ? [event.target.value] : [])
							}
						/>
					) : (
						<input
							{...marker([field.key])}
							aria-label={label}
							readOnly={readOnly}
							className={inputClass}
							type={type}
							inputMode={field.kind === "date" ? "text" : undefined}
							placeholder={field.kind === "date" ? "YYYY[-MM[-DD]]" : undefined}
							min={field.kind === "integer" ? "0" : undefined}
							step={
								field.kind === "integer"
									? "1"
									: field.kind === "number"
										? "any"
										: undefined
							}
							value={value[0] ?? ""}
							onChange={(event) =>
								onChange(event.target.value ? [event.target.value] : [])
							}
						/>
					)}
					{field.kind === "date" && <span>{m("hint.datePrecision")}</span>}
				</>
			) : (
				<>
					{values.map((item, index) => (
						<div key={`${field.key}-${index}`} className="relative min-w-0">
							<input
								{...marker([field.key])}
								aria-label={`${label}${values.length > 1 ? ` ${index + 1}` : ""}`}
								readOnly={readOnly}
								className={`${inputClass} pointer-coarse:h-10 ${readOnly ? "" : "pr-28 pointer-coarse:pr-44"}`}
								value={item}
								onChange={(event) =>
									onChange(
										values.map((entry, row) =>
											row === index ? event.target.value : entry,
										),
									)
								}
							/>
							<div
								aria-hidden={readOnly}
								className={`absolute right-1 top-0 flex h-8 items-center gap-0.5 pointer-coarse:h-10 pointer-coarse:[&_button]:min-w-10 [&_button]:h-7 [&_button]:px-1 [&_button]:py-0 ${readOnly ? "invisible" : ""}`}
							>
								<Button
									aria-label={`${m("action.moveUp")}: ${label} ${index + 1}`}
									isDisabled={readOnly || index === 0}
									onPress={() => onChange(move(values, index, -1))}
								>
									↑
								</Button>
								<Button
									aria-label={`${m("action.moveDown")}: ${label} ${index + 1}`}
									isDisabled={readOnly || index === values.length - 1}
									onPress={() => onChange(move(values, index, 1))}
								>
									↓
								</Button>
								<Button
									aria-label={`${m("action.remove")}: ${label} ${index + 1}`}
									isDisabled={readOnly}
									onPress={() =>
										onChange(values.filter((_, row) => row !== index))
									}
								>
									×
								</Button>
								{index === values.length - 1 && (
									<Button
										aria-label={`${m("action.addValue")}: ${label}`}
										isDisabled={readOnly}
										onPress={() => onChange([...values, ""])}
									>
										+
									</Button>
								)}
							</div>
						</div>
					))}
				</>
			)}
		</div>
	);
}

function ProviderGenreBaselineRow({
	baseline,
	locale,
}: {
	baseline: { genres: GenreReference[] } | null;
	locale: "en" | "ru";
}) {
	const label = metadataMessage(locale, "genre.providerBaseline");
	return (
		<section
			aria-label={label}
			data-provider-genre-baseline
			className="grid min-w-0 gap-1 border-t border-[var(--line)] pt-2 text-sm"
		>
			<h4 className="m-0 text-sm font-medium">{label}</h4>
			{baseline === null ? (
				<p className="m-0 text-[var(--muted)]">
					{metadataMessage(locale, "genre.providerBaselineUnavailable")}
				</p>
			) : baseline.genres.length ? (
				<ul className="m-0 flex list-none flex-wrap gap-2 p-0">
					{baseline.genres.map((genre) => (
						<li
							key={genre.id ?? genre.name}
							className="rounded-full border border-[var(--line)] px-2 py-0.5"
						>
							{genre.name}
						</li>
					))}
				</ul>
			) : (
				<p className="m-0 text-[var(--muted)]">
					{metadataMessage(locale, "genre.providerBaselineEmpty")}
				</p>
			)}
		</section>
	);
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
	impact = [],
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
	const [confirm, setConfirm] = useState(false);
	const [pending, setPending] = useState<{
		values: MetadataValues;
		details?: SaveDetails;
	}>();
	const [addedRoles, setAddedRoles] = useState<string[]>([]);
	useEffect(() => {
		if (!editing) setAddedRoles([]);
	}, [editing]);
	const actionRef = useRef<HTMLButtonElement>(null);
	const editorRef = useRef<HTMLElement>(null);
	const fields = useMemo(() => fieldsForScope(scope), [scope]);
	const editableFields = fields.filter(
		(field) => !audioMeasurementKeySet.has(field.key),
	);
	const current = controlled || !editing ? values : draft;
	const controlledValues = useRef(values);
	controlledValues.current = values;
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
		setConfirm(false);
		setPending(undefined);
		setError(undefined);
		setDraft({});
		setDirty(new Set());
		setDraftCredits([]);
		setDraftGenres([]);
		setDirtyGenres(false);
		setDraftExtras({});
		setDirtyExtras(new Set());
		setAddedRoles([]);
		requestAnimationFrame(() => actionRef.current?.focus());
	};
	const complete = async (next: MetadataValues, details?: SaveDetails) => {
		setSaving(true);
		setError(undefined);
		try {
			if (details) await onSave(next, details);
			else await onSave(next);
			setInternalEditing(false);
			setConfirm(false);
			setPending(undefined);
			setDraft({});
			setDirty(new Set());
			setDraftCredits([]);
			setDraftGenres([]);
			setDirtyGenres(false);
			setDraftExtras({});
			setDirtyExtras(new Set());
		} catch (reason) {
			setConfirm(false);
			setPending(undefined);
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
			if (invalidKey) {
				const candidates = [
					...(editorRef.current?.querySelectorAll<HTMLElement>(
						"[data-metadata-keys]",
					) ?? []),
				].filter((node) =>
					node
						.getAttribute("data-metadata-keys")
						?.split(",")
						.includes(invalidKey),
				);
				const marked =
					candidates.find((node) => node.matches("input, select, textarea")) ??
					candidates[0];
				let ancestor: HTMLElement | null = marked ?? null;
				while (ancestor) {
					if (ancestor instanceof HTMLDetailsElement) ancestor.open = true;
					ancestor = ancestor.parentElement;
				}
				requestAnimationFrame(() => marked?.focus());
			}
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
		const doSave = () =>
			void complete(update, Object.keys(details).length ? details : undefined);
		if (impact.length > 1) {
			setPending({
				values: update,
				details: Object.keys(details).length ? details : undefined,
			});
			setConfirm(true);
		} else doSave();
	};
	const confirmSave = () =>
		pending && void complete(pending.values, pending.details);
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

	return (
		<section
			ref={editorRef}
			aria-label={label}
			className="grid min-w-0 gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3"
			onKeyDown={(event) => {
				if (!controlled && editing && event.key === "Escape" && !confirm) {
					event.preventDefault();
					event.stopPropagation();
					cancel();
				}
			}}
		>
			<div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
				<h2 className="m-0 text-sm font-semibold">{label}</h2>
				<div
					aria-hidden={hideActions}
					className={`flex min-h-[30px] items-center gap-2 ${hideActions ? "invisible" : ""}`}
				>
					<Button
						ref={actionRef}
						className="w-[132px] shrink-0"
						isDisabled={hideActions || readOnly || saving}
						onPress={editing ? save : begin}
					>
						{saving
							? m("action.saving")
							: editing
								? m("action.save")
								: m("action.edit")}
					</Button>
					<Button
						className={`w-[132px] shrink-0 ${!hideActions && editing ? "visible" : "invisible"}`}
						isDisabled={hideActions || !editing || saving}
						aria-hidden={!editing}
						onPress={cancel}
					>
						{m("action.cancel")}
					</Button>
				</div>
			</div>
			{foreignFields.length > 0 && (
				<p className="m-0 text-xs text-[var(--muted)]">
					{m("label.foreignFields")}
				</p>
			)}
			{scope === "audio" && (
				<section
					aria-label={m("group.audioMeasurements")}
					className="grid min-w-0 gap-2"
				>
					<h3 className="m-0 text-sm font-semibold">
						{m("group.audioMeasurements")}
					</h3>
					<dl className="m-0 grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
						{audioMeasurementKeys.map((key) => {
							const field = fieldByKey(key);
							const measured = rows(values, key).filter(Boolean).join(" · ");
							return (
								<div
									key={key}
									{...marker([key])}
									className="grid min-w-0 gap-1 rounded border border-[var(--line)] p-2"
								>
									<dt className="text-xs text-[var(--muted)]">
										{field ? metadataFieldLabel(field.key, locale) : key}
									</dt>
									<dd className="m-0 break-words text-sm text-[var(--ink)]">
										{measured || m("state.notCalculated")}
									</dd>
								</div>
							);
						})}
					</dl>
				</section>
			)}
			{(basicFields.length > 0 ||
				editableFields.some((field) => field.kind === "genres") ||
				genres !== undefined) && (
				<section className="grid min-w-0 gap-2">
					<h3 className="m-0 text-sm font-semibold">{m("group.basic")}</h3>
					<div className="grid min-w-0 grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
						{basicFields.map((field) => (
							<SimpleField
								key={field.key}
								field={field}
								locale={locale}
								m={m}
								value={rows(current, field.key)}
								readOnly={
									!editing ||
									(field.kind === "identity" &&
										!["asin", "musicbrainz_discid"].includes(field.key))
								}
								onChange={(next) => setValue(field.key, next)}
							/>
						))}
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
					</div>
				</section>
			)}
			{providerGenreBaseline !== undefined && (
				<ProviderGenreBaselineRow
					baseline={providerGenreBaseline}
					locale={locale}
				/>
			)}
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
									<div
										key={role}
										className="grid gap-2 border-b border-[var(--line)] pb-2 last:border-0"
									>
										<h4 className="m-0 text-xs font-semibold">
											{metadataFieldLabel(roleField.key, locale)}
										</h4>
										{(roleContributors.length
											? roleContributors
											: [{ name: "" }]
										).map((contributor, index) => (
											<div
												key={`${role}-${index}`}
												className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4"
											>
												<KeyInput
													fieldKey={roleField.key}
													label={m("label.contributorName")}
													value={contributor.name}
													readOnly={!editing}
													onChange={(name) => {
														const next = [...roleContributors];
														next[index] = { ...next[index], name };
														writeRole(next);
													}}
												/>
												{idField && (
													<KeyInput
														fieldKey={idField.key}
														label={m("label.contributorMusicBrainz")}
														value={contributor.mbid ?? ""}
														readOnly={!editing}
														onChange={(mbid) => {
															const next = [...roleContributors];
															next[index] = { ...next[index], mbid };
															writeRole(next);
														}}
													/>
												)}
												{sortField && (
													<KeyInput
														fieldKey={sortField.key}
														label={metadataFieldLabel(sortField.key, locale)}
														value={contributor.sortName ?? ""}
														readOnly={!editing}
														onChange={(sortName) => {
															const next = [...roleContributors];
															next[index] = { ...next[index], sortName };
															writeRole(next);
														}}
													/>
												)}
												{role === "performer" && (
													<KeyInput
														fieldKey={undefined}
														label={m("label.instrument")}
														value={contributor.instrument ?? ""}
														readOnly={!editing}
														onChange={(instrument) => {
															const next = [...roleContributors];
															next[index] = { ...next[index], instrument };
															writeRole(next);
														}}
													/>
												)}
												{editing && (
													<div className="flex gap-1">
														<Button
															aria-label={`${m("action.moveUp")}: ${metadataFieldLabel(roleField.key, locale)} ${index + 1}`}
															isDisabled={!editing || index === 0}
															onPress={() => {
																writeRole(move(roleContributors, index, -1));
															}}
														>
															↑
														</Button>
														<Button
															aria-label={`${m("action.moveDown")}: ${metadataFieldLabel(roleField.key, locale)} ${index + 1}`}
															isDisabled={
																!editing ||
																index === roleContributors.length - 1
															}
															onPress={() => {
																writeRole(move(roleContributors, index, 1));
															}}
														>
															↓
														</Button>
														<Button
															aria-label={`${m("action.remove")} ${metadataFieldLabel(roleField.key, locale)} ${index + 1}`}
															isDisabled={!editing}
															onPress={() => {
																writeRole(
																	roleContributors.filter(
																		(_, row) => row !== index,
																	),
																);
															}}
														>
															{m("action.remove")}
														</Button>
													</div>
												)}
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
									</div>
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
				{(fields.some((field) => field.group === "works") ||
					fields.some((field) => field.group === "movement")) && (
					<details className="min-w-0">
						<summary className={disclosureSummaryClass}>
							{m("group.worksMovement")} ·{" "}
							{metadataSummary(
								fields.filter(
									(field) =>
										field.group === "works" || field.group === "movement",
								),
								current,
								locale,
							)}
						</summary>
						{fields.some((field) => field.group === "works") && (
							<section
								{...marker(["work", "musicbrainz_workid"])}
								className="grid gap-2 rounded border border-[var(--line)] p-2"
							>
								<h3 className="m-0 text-sm font-semibold">
									{groupHeading("works")}
								</h3>
								{(readMetadataWorks(current).length
									? readMetadataWorks(current)
									: [{ title: "" }]
								).map((work, index) => (
									<div
										key={`work-${index}`}
										className="grid gap-2 sm:grid-cols-2"
									>
										<KeyInput
											fieldKey="work"
											label={m("label.workTitle")}
											value={work.title}
											readOnly={!editing}
											onChange={(title) => {
												const next = [...readMetadataWorks(current)];
												next[index] = { ...next[index], title };
												const written = writeMetadataWorks(current, next);
												setValue("work", written.work);
												setValue(
													"musicbrainz_workid",
													written.musicbrainz_workid,
												);
											}}
										/>
										<KeyInput
											fieldKey="musicbrainz_workid"
											label={m("label.workMusicBrainz")}
											value={work.mbid ?? ""}
											readOnly={!editing}
											onChange={(mbid) => {
												const next = [...readMetadataWorks(current)];
												next[index] = { ...next[index], mbid };
												const written = writeMetadataWorks(current, next);
												setValue("work", written.work);
												setValue(
													"musicbrainz_workid",
													written.musicbrainz_workid,
												);
											}}
										/>
										{editing && (
											<div className="contents">
												<Button
													aria-label={`${m("action.moveUp")} ${m("group.works")} ${index + 1}`}
													isDisabled={!editing || index === 0}
													onPress={() => {
														const written = writeMetadataWorks(
															current,
															move(readMetadataWorks(current), index, -1),
														);
														setValue("work", written.work);
														setValue(
															"musicbrainz_workid",
															written.musicbrainz_workid,
														);
													}}
												>
													↑
												</Button>
												<Button
													aria-label={`${m("action.moveDown")} ${m("group.works")} ${index + 1}`}
													isDisabled={
														!editing ||
														index === readMetadataWorks(current).length - 1
													}
													onPress={() => {
														const written = writeMetadataWorks(
															current,
															move(readMetadataWorks(current), index, 1),
														);
														setValue("work", written.work);
														setValue(
															"musicbrainz_workid",
															written.musicbrainz_workid,
														);
													}}
												>
													↓
												</Button>
												<Button
													aria-label={`${m("action.remove")} ${m("group.works")} ${index + 1}`}
													isDisabled={!editing}
													onPress={() => {
														const written = writeMetadataWorks(
															current,
															readMetadataWorks(current).filter(
																(_, row) => row !== index,
															),
														);
														setValue("work", written.work);
														setValue(
															"musicbrainz_workid",
															written.musicbrainz_workid,
														);
													}}
												>
													{m("action.remove")}
												</Button>
											</div>
										)}
									</div>
								))}
								{editing && (
									<Button
										isDisabled={!editing}
										className="justify-self-start"
										onPress={() => {
											const written = writeMetadataWorks(current, [
												...readMetadataWorks(current),
												{ title: "" },
											]);
											setValue("work", written.work);
											setValue(
												"musicbrainz_workid",
												written.musicbrainz_workid,
											);
										}}
									>
										{m("action.addWork")}
									</Button>
								)}
							</section>
						)}
						{fields.some((field) => field.group === "movement") && (
							<section
								{...marker(
									fields
										.filter((field) => field.group === "movement")
										.map((field) => field.key),
								)}
								className="grid gap-2 rounded border border-[var(--line)] p-2"
							>
								<h3 className="m-0 text-sm font-semibold">
									{groupHeading("movement")}
								</h3>
								<div className="grid gap-2 sm:grid-cols-3">
									{fields
										.filter((field) => field.group === "movement")
										.map((field) => (
											<SimpleField
												key={field.key}
												field={field}
												locale={locale}
												m={m}
												value={rows(current, field.key)}
												readOnly={!editing}
												onChange={(next) => setValue(field.key, next)}
											/>
										))}
								</div>
							</section>
						)}
					</details>
				)}
				{otherSections.map(([group, sectionFields]) => (
					<details key={group} className="min-w-0">
						<summary className={disclosureSummaryClass}>
							{groupHeading(group)} ·{" "}
							{metadataSummary(sectionFields, current, locale)}
						</summary>
						<section className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-2">
							<h3 className="m-0 text-sm font-semibold">
								{groupHeading(group)}
							</h3>
							<div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
								{sectionFields
									.filter(
										(field) =>
											!basicKeys.has(field.key) &&
											field.kind !== "lyrics" &&
											!consumed.has(field.key) &&
											!(
												field.kind === "identity" &&
												(field.key === "musicbrainz_artistid" ||
													field.key === "musicbrainz_albumartistid")
											),
									)
									.map((field) =>
										field.kind === "genres" ? (
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
										) : (
											<SimpleField
												key={field.key}
												field={field}
												locale={locale}
												m={m}
												value={rows(current, field.key)}
												readOnly={
													!editing ||
													(field.kind === "identity" &&
														!["asin", "musicbrainz_discid"].includes(field.key))
												}
												onChange={(next) => setValue(field.key, next)}
											/>
										),
									)}
							</div>
						</section>
					</details>
				))}
				{fields
					.filter((field) => field.kind === "lyrics")
					.map((field) => (
						<details key={field.key} className="min-w-0">
							<summary className={disclosureSummaryClass}>
								{m("group.lyrics")} ·{" "}
								{rows(current, field.key).some(Boolean)
									? m("state.filled")
									: m("state.empty")}
							</summary>
							<section className="grid gap-2 p-2" {...marker([field.key])}>
								<SimpleField
									field={field}
									locale={locale}
									m={m}
									value={rows(current, field.key)}
									readOnly={!editing}
									onChange={(next) => setValue(field.key, next)}
								/>
							</section>
						</details>
					))}
				{extraFields.length > 0 && (
					<details className="min-w-0">
						<summary className={disclosureSummaryClass}>
							{m("group.extraTags")} ·{" "}
							{extraFields.filter((field) => field.value).length}{" "}
							{m("state.filled")}
						</summary>
						<section className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-2">
							<h3 className="m-0 text-sm font-semibold">
								{m("group.extraDetails")}
							</h3>
							<div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
								{extraFields.map((field) => (
									<label
										key={field.key}
										className="grid min-w-0 gap-1 text-xs text-[var(--muted)]"
									>
										<span>
											{metadataExtraFieldLabel(field.key, field.label, locale)}
										</span>
										<input
											aria-label={metadataExtraFieldLabel(
												field.key,
												field.label,
												locale,
											)}
											className={inputClass}
											readOnly={!editing}
											value={
												editing ? (draftExtras[field.key] ?? "") : field.value
											}
											onChange={(event) => {
												setDraftExtras((state) => ({
													...state,
													[field.key]: event.target.value,
												}));
												setDirtyExtras((state) =>
													new Set(state).add(field.key),
												);
												setError(undefined);
											}}
										/>
									</label>
								))}
							</div>
						</section>
					</details>
				)}
			</div>
			{displayedError && (
				<p role="alert" className="m-0 text-sm text-[var(--error)]">
					{displayedError}
				</p>
			)}
			{confirm && (
				<Modal
					isOpen
					isDismissable
					onOpenChange={(open) => {
						if (!open) {
							setConfirm(false);
							setPending(undefined);
						}
					}}
				>
					<Dialog aria-label={m("label.confirmImpact")} className="grid gap-3">
						<Heading slot="title">{m("label.affected")}</Heading>
						<ul className="m-0 list-disc pl-5">
							{impact.map((position) => (
								<li key={position}>{position}</li>
							))}
						</ul>
						<div className="flex gap-2">
							<Button isDisabled={saving} onPress={confirmSave}>
								{m("action.confirmSave")}
							</Button>
							<Button
								isDisabled={saving}
								onPress={() => {
									setConfirm(false);
									setPending(undefined);
								}}
							>
								{m("action.returnEdit")}
							</Button>
						</div>
					</Dialog>
				</Modal>
			)}
		</section>
	);
}
