import { useId, useLayoutEffect, useRef, useState } from "react";
import { focusViewById } from "../../../../ui/components/useViewFocus";
import type { MetadataEditorRolesView } from "../../../../ui/features/library/MetadataEditorView";
import { metadataFilledSummary } from "../../../../ui/features/library/MetadataFieldPresentation";
import { libraryTagComparisonFieldLabel } from "../../../../ui/features/library/libraryTagComparisonFields";
import { ContributorArtistLinkNotice } from "../../../../ui/features/library/metadata/ContributorArtistLinkNotice";
import { ContributorModeControlView } from "../../../../ui/features/library/metadata/ContributorModeControlView";
import { ContributorRoleControlView } from "../../../../ui/features/library/metadata/ContributorRoleControlView";
import { releaseMetadataEditorMessages } from "../../../../ui/features/library/metadata/releaseMetadataEditorMessages";
import { createMetadataDraftRowKey } from "../../../../ui/features/library/metadata/structuredMetadataDraft";
import type { Locale } from "../../../../ui/i18n/types";
import {
	registryContributorRoles,
	type ScopedContributorRow,
	type ScopedContributors,
} from "./scopedContributors";

type Row = ScopedContributorRow & { key: string; added?: boolean };
type Draft = { state: ScopedContributors["state"]; rows: Row[] };
const contributorIDPattern =
	/^(?:[0-9a-f]{32}|(?:urn:uuid:)?[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}|\{[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}\})$/i;

const roleOrder = [...new Set(registryContributorRoles())];
const toDraft = (saved: ScopedContributors): Draft => ({
	state: saved.state,
	rows:
		saved.state === "value"
			? saved.rows.map((row, index) => ({ ...row, key: `saved-${index}` }))
			: [],
});

export function useScopedContributorsEditor(
	locale: Locale,
	saved: ScopedContributors | undefined,
	editing: boolean,
	saving: boolean,
) {
	const text = releaseMetadataEditorMessages[locale];
	const prefix = useId();
	const [draft, setDraft] = useState<Draft>();
	const [focus, setFocus] = useState<string>();
	const addBaseline = useRef<
		{ value: Draft | undefined; rows: Row[] } | undefined
	>(undefined);
	const modeTouched = useRef(false);
	useLayoutEffect(() => {
		if (focus && focusViewById(focus)) setFocus(undefined);
	}, [focus]);

	const reset = () => {
		setDraft(undefined);
		setFocus(undefined);
		addBaseline.current = undefined;
		modeTouched.current = false;
	};
	if (!saved)
		return {
			reset,
			view: undefined,
			change: undefined,
			invalid: undefined,
			invalidIdentity: false,
		};
	const current = (editing && draft) || toDraft(saved);
	const rows = current.state === "value" ? current.rows : [];
	const idFor = (key: string, field: "name" | "role") =>
		`${prefix}-${key}-${field}`;
	const roleLabel = (role: string) =>
		Object.hasOwn(text.contributorRoles, role)
			? libraryTagComparisonFieldLabel(role, locale)
			: role || text.contributorRole;
	const roles = new Map<string, number[]>();
	for (const [index, row] of rows.entries()) {
		const role = row.role ?? "";
		const indices = roles.get(role) ?? [];
		indices.push(index);
		roles.set(role, indices);
	}
	const update = (next: Row[]) => setDraft({ state: "value", rows: next });
	const replace = (index: number, patch: Partial<ScopedContributorRow>) =>
		update(rows.map((row, i) => (i === index ? { ...row, ...patch } : row)));
	const add = (role: string) => {
		if (!addBaseline.current) {
			addBaseline.current = { value: draft, rows };
			modeTouched.current = false;
		}
		const key = createMetadataDraftRowKey();
		setFocus(idFor(key, "name"));
		update([...rows, { key, role, name: "", added: true }]);
	};
	const remove = (index: number) => {
		const next = rows.filter((_, i) => i !== index);
		const baseline = addBaseline.current;
		if (
			baseline &&
			!modeTouched.current &&
			next.length === baseline.rows.length &&
			next.every((row, i) => {
				const before = baseline.rows[i];
				return (
					row.key === before.key &&
					row.name === before.name &&
					row.role === before.role &&
					row.instrument === before.instrument &&
					row.artist_id === before.artist_id &&
					row.mbid === before.mbid
				);
			})
		) {
			setDraft(baseline.value);
			addBaseline.current = undefined;
			return;
		}
		update(next);
	};
	const invalid = rows.find((row) => row.added && !row.name.trim());
	const change: ScopedContributors | undefined =
		draft &&
		(draft.state === "value"
			? {
					state: "value",
					rows: draft.rows.map(({ key: _key, added: _added, ...row }) => ({
						...row,
						role: row.role ?? "",
						mbid: row.mbid?.trim() ? row.mbid : undefined,
						instrument: row.instrument?.trim() ? row.instrument : undefined,
					})),
				}
			: { state: draft.state });
	const view: MetadataEditorRolesView = {
		label: text.groups.contributors,
		summary: metadataFilledSummary(
			locale,
			rows.filter((row) => row.name.trim()).length,
		),
		dataKeys: ["contributors"],
		editing,
		roles: [...new Set([...roleOrder, ...roles.keys()])].flatMap((role) => {
			const indices = roles.get(role);
			if (!indices) return [];
			return [
				{
					role,
					label: roleLabel(role),
					editing,
					contributorCount: indices.length,
					contributors: indices.map((index) => {
						const row = rows[index];
						return {
							uiKey: row.key,
							name: row.name,
							mbid: row.mbid,
							instrument: row.instrument,
							nameKey: "contributors",
							mbidKey: "contributors",
							nameInputId: idFor(row.key, "name"),
							nameRequired: Boolean(row.added),
							nameError:
								row.added && !row.name.trim()
									? text.contributorNameRequired
									: undefined,
							showInstrument:
								editing || role === "performer" || row.instrument !== undefined,
							roleControl: editing ? (
								<ContributorRoleControlView
									locale={locale}
									inputId={idFor(row.key, "role")}
									value={role}
									index={index}
									disabled={saving}
									onChange={(next) => {
										replace(index, { role: next });
										setFocus(idFor(row.key, "role"));
									}}
								/>
							) : undefined,
							artistLink: row.artist_id ? (
								<ContributorArtistLinkNotice locale={locale} />
							) : undefined,
						};
					}),
					onAdd: () => add(role),
					onChange: (i, patch) => replace(indices[i], patch),
					onRemove: (i) => remove(indices[i]),
					onMove: (i, delta) => {
						const from = indices[i],
							to = indices[i + delta];
						if (from === undefined || to === undefined) return;
						const next = [...rows];
						[next[from], next[to]] = [next[to], next[from]];
						update(next);
					},
				},
			];
		}),
		roleOptions: roleOrder
			.filter((role) => !roles.has(role))
			.map((role) => ({ role, label: roleLabel(role) })),
		addRoleLabel: text.addRole,
		selectRoleLabel: text.selectRole,
		onAddRole: add,
		modeControl: editing ? (
			<ContributorModeControlView
				locale={locale}
				inputId={`${prefix}-contributors-state`}
				value={current.state}
				disabled={saving}
				onChange={(state) => {
					modeTouched.current = true;
					setDraft({ state, rows: current.rows });
				}}
			/>
		) : undefined,
		emptyText:
			current.state === "explicit_empty"
				? text.explicitEmpty
				: current.state === "absent"
					? text.noOverride
					: text.emptyValue,
	};

	return {
		view,
		change: editing ? change : undefined,
		reset,
		invalid: invalid ? idFor(invalid.key, "name") : undefined,
		invalidIdentity:
			change?.state === "value" &&
			change.rows.some((row) =>
				[row.artist_id, row.mbid].some(
					(id) => id !== undefined && !contributorIDPattern.test(id),
				),
			),
	};
}
