import type { KeyboardEvent, RefObject } from "react";
import { Button } from "../components/ui";
import {
	ContributorRoleSectionView,
	ContributorRowView,
	type ContributorRowItem,
} from "../features/library/ContributorRowView";
import { MetadataCardActions } from "../features/library/MetadataCardActions";
import {
	MetadataBasicsSection,
	type MetadataFieldDescriptor,
	MetadataFieldGroup,
} from "../features/library/MetadataFieldPresentation";
import { MovementFieldsView } from "../features/library/MovementFieldsView";
import {
	ProviderGenreBaselineRow,
	type ProviderGenreBaselineValue,
} from "../features/library/ProviderGenreBaselineRow";
import {
	type WorkRowDraft,
	WorkRowsView,
} from "../features/library/WorkRowsView";
import { WorksMovementSectionView } from "../features/library/WorksMovementSectionView";
import type { Locale } from "../i18n/types";
import { GenrePickerView, type GenrePickerViewProps } from "./GenrePickerView";
import { type MetadataMessageKey, metadataMessage } from "./messages";

export type MetadataEditorBasicsView = {
	fields: MetadataFieldDescriptor[];
	genre?: { key?: string; picker: GenrePickerViewProps };
};

export type MetadataEditorCreditView = {
	artistId: string;
	creditName: string;
	joinPhrase: string;
	mbid: string;
	displayName: string;
	sortName: string;
};

export type MetadataEditorArtistsView = {
	groupLabel: string;
	compositionLabel: string;
	hint: string;
	creditKeys: readonly string[];
	sortFieldKey: string;
	summaryCredits: readonly MetadataEditorCreditView[];
	compositionCredits: readonly MetadataEditorCreditView[];
	creditCount: number;
	artistOptions: readonly { id: string; name: string }[];
	editing: boolean;
	readOnly: boolean;
	showRowActions: boolean;
	showAdd: boolean;
	onChangeArtist: (index: number, artistId: string) => void;
	onChangeCreditName: (index: number, value: string) => void;
	onChangeJoinPhrase: (index: number, value: string) => void;
	onMove: (index: number, delta: number) => void;
	onRemove: (index: number) => void;
	onAdd: () => void;
};

export type MetadataEditorContributorPatch = {
	name?: string;
	mbid?: string;
	sortName?: string;
	instrument?: string;
};

export type MetadataEditorContributorView = {
	uiKey: string;
	name: string;
	mbid?: string;
	sortName?: string;
	instrument?: string;
	nameKey: string;
	mbidKey?: string;
	sortKey?: string;
	sortLabel?: string;
	showInstrument: boolean;
};

export type MetadataEditorRoleView = {
	role: string;
	label: string;
	contributors: readonly MetadataEditorContributorView[];
	contributorCount: number;
	editing: boolean;
	onChange: (index: number, patch: MetadataEditorContributorPatch) => void;
	onMove: (index: number, delta: number) => void;
	onRemove: (index: number) => void;
	onAdd: () => void;
};

export type MetadataEditorRolesView = {
	label: string;
	summary: string;
	dataKeys: readonly string[];
	roles: readonly MetadataEditorRoleView[];
	roleOptions: readonly { role: string; label: string }[];
	addRoleLabel: string;
	selectRoleLabel: string;
	editing: boolean;
	onAddRole: (role: string) => void;
};

export type MetadataEditorWorksView = {
	rows: readonly WorkRowDraft[];
	editing: boolean;
	disabled: boolean;
	onChange: (rows: WorkRowDraft[]) => void;
	onAdd: () => void;
};

export type MetadataEditorMovementView = {
	value: { name: string; count: string; number: string };
	editing: boolean;
	disabled: boolean;
	onChange: (field: "name" | "count" | "number", value: string) => void;
};

export type MetadataEditorWorksMovementView = {
	label: string;
	summary: string;
	worksLabel: string;
	movementLabel: string;
	worksDataKeys?: string[];
	movementDataKeys: string[];
	works?: MetadataEditorWorksView;
	movement?: MetadataEditorMovementView;
};

export type MetadataEditorSectionView = {
	key: string;
	label: string;
	summary: string;
	fields: MetadataFieldDescriptor[];
};

export type MetadataEditorExtrasView = {
	label: string;
	summary: string;
	fields: MetadataFieldDescriptor[];
};

export type MetadataEditorViewProps = {
	locale: Locale;
	label: string;
	editing: boolean;
	saving: boolean;
	readOnly: boolean;
	hideActions: boolean;
	editorRef: RefObject<HTMLElement | null>;
	actionsRef: RefObject<HTMLDivElement | null>;
	onEditOrSave: () => void;
	onCancel: () => void;
	onKeyDown: (event: KeyboardEvent<HTMLElement>) => void;
	showForeignFieldsNotice: boolean;
	basics?: MetadataEditorBasicsView;
	providerGenreBaseline?: ProviderGenreBaselineValue | null;
	artists?: MetadataEditorArtistsView;
	roles?: MetadataEditorRolesView;
	worksMovement?: MetadataEditorWorksMovementView;
	sections: readonly MetadataEditorSectionView[];
	lyrics: readonly MetadataEditorSectionView[];
	extras?: MetadataEditorExtrasView;
	error?: string;
};

const inputClass =
	"h-8 min-w-0 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] outline-none focus-visible:ring-2 focus-visible:ring-[var(--focus)] read-only:border-transparent read-only:bg-transparent";
const disclosureSummaryClass =
	"w-full cursor-pointer rounded border border-[var(--line)] p-2 text-sm font-semibold hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";

function marker(keys: readonly string[]) {
	return { "data-metadata-keys": keys.join(",") };
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

/**
 * Copy-ready metadata editor card composition.
 *
 * The provider owns draft state, normalisation, validation, save/cancel and
 * focus. This module owns the complete card markup, including scalar field
 * descriptors, artist credits, grouped contributor roles, works and movement,
 * lyrics, extra tags and notices, and renders the prepared genre picker model.
 */
export function MetadataEditorView({
	locale,
	label,
	editing,
	saving,
	readOnly,
	hideActions,
	editorRef,
	actionsRef,
	onEditOrSave,
	onCancel,
	onKeyDown,
	showForeignFieldsNotice,
	basics,
	providerGenreBaseline,
	artists,
	roles,
	worksMovement,
	sections,
	lyrics,
	extras,
	error,
}: MetadataEditorViewProps) {
	const m = (key: MetadataMessageKey) => metadataMessage(locale, key);
	return (
		<section
			ref={editorRef}
			aria-label={label}
			className="grid min-w-0 gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3"
			onKeyDown={onKeyDown}
		>
			<MetadataCardActions
				locale={locale}
				title={label}
				editing={editing}
				saving={saving}
				editOrSaveDisabled={readOnly}
				hidden={hideActions}
				actionsRef={actionsRef}
				onEditOrSave={onEditOrSave}
				onCancel={onCancel}
			/>
			{showForeignFieldsNotice && (
				<p className="m-0 text-xs text-[var(--muted)]">
					{m("label.foreignFields")}
				</p>
			)}
			{basics && (
				<MetadataBasicsSection locale={locale} fields={basics.fields}>
					{basics.genre?.key ? (
						<div key={basics.genre.key} {...marker([basics.genre.key])}>
							<GenrePickerView {...basics.genre.picker} />
						</div>
					) : basics.genre ? (
						<GenrePickerView {...basics.genre.picker} />
					) : null}
				</MetadataBasicsSection>
			)}
			<ProviderGenreBaselineRow
				baseline={providerGenreBaseline}
				locale={locale}
			/>
			{artists && (
				<div className="grid gap-2 rounded border border-[var(--line)] p-2">
					<div className="flex flex-wrap items-center gap-2 text-sm">
						<strong>{artists.groupLabel}</strong>
						<span className="flex flex-wrap items-center text-[var(--muted)]">
							{artists.summaryCredits.length ? (
								artists.summaryCredits.map((credit, index) => (
									<span key={`${credit.artistId}-${index}`}>
										{credit.displayName}
										{credit.joinPhrase}
									</span>
								))
							) : (
								<span>{m("state.noCredits")}</span>
							)}
						</span>
					</div>
					<details>
						<summary className="cursor-pointer text-xs text-[var(--muted)]">
							{artists.compositionLabel}
						</summary>
						<section
							{...marker(artists.creditKeys)}
							className="grid gap-2 rounded border border-[var(--line)] p-2"
						>
							{artists.compositionCredits.map((credit, index) => (
								<div
									key={`${credit.artistId}-${index}`}
									className="grid min-w-0 gap-2 sm:grid-cols-2 lg:grid-cols-4"
								>
									<label className="grid gap-1 text-xs text-[var(--muted)]">
										<span>{m("label.linkedArtist")}</span>
										<select
											{...marker([artists.creditKeys[0] ?? ""])}
											aria-label={`${m("label.linkedArtist")} ${index + 1}`}
											className={inputClass}
											disabled={
												!artists.editing || !artists.artistOptions.length
											}
											value={credit.artistId}
											onChange={(event) =>
												artists.onChangeArtist(index, event.target.value)
											}
										>
											<option value="">{m("label.unlinked")}</option>
											{artists.artistOptions.map((artist) => (
												<option key={artist.id} value={artist.id}>
													{artist.name}
												</option>
											))}
										</select>
									</label>
									<KeyInput
										fieldKey={artists.creditKeys[0]}
										label={m("label.creditName")}
										value={credit.creditName}
										readOnly={!artists.editing}
										onChange={(creditName) =>
											artists.onChangeCreditName(index, creditName)
										}
									/>
									<KeyInput
										fieldKey={artists.creditKeys[2]}
										label={m("label.artistMusicBrainz")}
										value={credit.mbid}
										readOnly
										onChange={() => undefined}
									/>
									<KeyInput
										fieldKey="credit-joinphrase"
										label={m("label.joinPhrase")}
										value={credit.joinPhrase}
										readOnly={!artists.editing}
										onChange={(joinPhrase) =>
											artists.onChangeJoinPhrase(index, joinPhrase)
										}
									/>
									<KeyInput
										fieldKey={artists.sortFieldKey}
										label={m("label.artistSort")}
										value={credit.sortName}
										readOnly
										onChange={() => undefined}
									/>
									{artists.showRowActions && (
										<div className="flex gap-1">
											<Button
												aria-label={`${m("action.moveUp")} ${m("label.artistSort")} ${index + 1}`}
												isDisabled={index === 0}
												onPress={() => artists.onMove(index, -1)}
											>
												↑
											</Button>
											<Button
												aria-label={`${m("action.moveDown")} ${m("label.artistSort")} ${index + 1}`}
												isDisabled={index === artists.creditCount - 1}
												onPress={() => artists.onMove(index, 1)}
											>
												↓
											</Button>
											<Button
												aria-label={`${m("action.remove")} ${m("group.releaseArtists")} ${index + 1}`}
												onPress={() => artists.onRemove(index)}
											>
												{m("action.remove")}
											</Button>
										</div>
									)}
								</div>
							))}
							{artists.showAdd && (
								<Button className="justify-self-start" onPress={artists.onAdd}>
									{m("action.addArtist")}
								</Button>
							)}
							<p className="m-0 text-xs text-[var(--muted)]">{artists.hint}</p>
						</section>
					</details>
				</div>
			)}
			<div data-metadata-sections className="grid min-w-0 grid-cols-1 gap-2">
				{roles && (
					<details className="min-w-0">
						<summary className={disclosureSummaryClass}>
							{roles.label} · {roles.summary}
						</summary>
						<section
							{...marker(roles.dataKeys)}
							className="grid gap-2 rounded border border-[var(--line)] p-2"
						>
							{roles.roles.map((role) => (
								<ContributorRoleSectionView key={role.role} label={role.label}>
									{role.contributors.map((contributor, index) => {
										const items: ContributorRowItem[] = [
											{
												key: "name",
												label: m("label.contributorName"),
												value: contributor.name,
												dataKeys: [contributor.nameKey],
												readOnly: !role.editing,
												onChange: (name: string) =>
													role.onChange(index, { name }),
											},
											...(contributor.mbidKey
												? [
														{
															key: "mbid",
															label: m("label.contributorMusicBrainz"),
															value: contributor.mbid ?? "",
															dataKeys: [contributor.mbidKey],
															readOnly: !role.editing,
															onChange: (mbid: string) =>
																role.onChange(index, { mbid }),
														},
													]
												: []),
											...(contributor.sortKey
												? [
														{
															key: "sort",
															label: contributor.sortLabel ?? "",
															value: contributor.sortName ?? "",
															dataKeys: [contributor.sortKey],
															readOnly: !role.editing,
															onChange: (sortName: string) =>
																role.onChange(index, { sortName }),
														},
													]
												: []),
											...(contributor.showInstrument
												? [
														{
															key: "instrument",
															label: m("label.instrument"),
															value: contributor.instrument ?? "",
															readOnly: !role.editing,
															onChange: (instrument: string) =>
																role.onChange(index, { instrument }),
														},
													]
												: []),
										];
										return (
											<div
												key={contributor.uiKey}
												className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4"
											>
												<ContributorRowView
													items={items}
													actions={
														role.editing
															? {
																	moveUp: {
																		label: `${m("action.moveUp")}: ${role.label} ${index + 1}`,
																		disabled: index === 0,
																		onPress: () => role.onMove(index, -1),
																	},
																	moveDown: {
																		label: `${m("action.moveDown")}: ${role.label} ${index + 1}`,
																		disabled:
																			index === role.contributorCount - 1,
																		onPress: () => role.onMove(index, 1),
																	},
																	remove: {
																		label: `${m("action.remove")} ${role.label} ${index + 1}`,
																		text: m("action.remove"),
																		onPress: () => role.onRemove(index),
																	},
																}
															: undefined
													}
												/>
											</div>
										);
									})}
									{role.editing && (
										<Button className="justify-self-start" onPress={role.onAdd}>
											{m("action.addContributor")}
										</Button>
									)}
								</ContributorRoleSectionView>
							))}
							{roles.editing && roles.roleOptions.length > 0 && (
								<label className="grid max-w-sm gap-1 text-xs text-[var(--muted)]">
									<span>{roles.addRoleLabel}</span>
									<select
										aria-label={roles.addRoleLabel}
										className={inputClass}
										value=""
										onChange={(event) => {
											if (event.target.value)
												roles.onAddRole(event.target.value);
										}}
									>
										<option value="">{roles.selectRoleLabel}</option>
										{roles.roleOptions.map((option) => (
											<option key={option.role} value={option.role}>
												{option.label}
											</option>
										))}
									</select>
								</label>
							)}
						</section>
					</details>
				)}
				{worksMovement && (
					<WorksMovementSectionView
						label={worksMovement.label}
						summary={worksMovement.summary}
						worksLabel={worksMovement.worksLabel}
						movementLabel={worksMovement.movementLabel}
						worksDataKeys={worksMovement.worksDataKeys}
						movementDataKeys={worksMovement.movementDataKeys}
						works={
							worksMovement.works ? (
								<WorkRowsView
									locale={locale}
									rows={worksMovement.works.rows}
									editing={worksMovement.works.editing}
									disabled={worksMovement.works.disabled}
									editableMbid
									titleDataKey="work"
									mbidDataKey="musicbrainz_workid"
									onChange={worksMovement.works.onChange}
									onAdd={worksMovement.works.onAdd}
								/>
							) : undefined
						}
						movement={
							worksMovement.movement ? (
								<MovementFieldsView
									locale={locale}
									value={worksMovement.movement.value}
									editing={worksMovement.movement.editing}
									disabled={worksMovement.movement.disabled}
									onChange={worksMovement.movement.onChange}
								/>
							) : undefined
						}
					/>
				)}
				{sections.map((section) => (
					<MetadataFieldGroup
						key={section.key}
						label={section.label}
						summary={section.summary}
						columns={4}
						heading
						fields={section.fields}
					/>
				))}
				{lyrics.map((section) => (
					<MetadataFieldGroup
						key={section.key}
						label={section.label}
						summary={section.summary}
						columns={1}
						fields={section.fields}
					/>
				))}
				{extras && (
					<MetadataFieldGroup
						label={extras.label}
						summary={extras.summary}
						columns={4}
						fields={extras.fields}
					/>
				)}
			</div>
			{error && (
				<p role="alert" className="m-0 text-sm text-[var(--error)]">
					{error}
				</p>
			)}
		</section>
	);
}
