import type { KeyboardEvent, ReactNode, RefObject } from "react";
import { AppButton as Button } from "../../components/AppButton";
import type { Locale } from "../../i18n/types";
import {
  ContributorRoleSectionView,
  type ContributorRowItem,
  ContributorRowView,
} from "./ContributorRowView";
import { MetadataCardActions } from "./MetadataCardActions";
import {
  MetadataBasicsSection,
  type MetadataFieldDescriptor,
  MetadataFieldGroup,
} from "./MetadataFieldPresentation";
import { MovementFieldsView } from "./MovementFieldsView";
import {
  ProviderGenreBaselineRow,
  type ProviderGenreBaselineValue,
} from "./ProviderGenreBaselineRow";
import { type WorkRowDraft, WorkRowsView } from "./WorkRowsView";
import { WorksMovementSectionView } from "./WorksMovementSectionView";

export type MetadataEditorLabels = {
  foreignFields: string;
  noCredits: string;
  linkedArtist: string;
  unlinked: string;
  creditName: string;
  artistMusicBrainz: string;
  joinPhrase: string;
  artistSort: string;
  moveUp: string;
  moveDown: string;
  remove: string;
  addArtist: string;
  contributorName: string;
  contributorMusicBrainz: string;
  instrument: string;
  addContributor: string;
  addRole: string;
  selectRole: string;
  nameRequired: string;
};

export type MetadataEditorBasicsView = {
  fields: MetadataFieldDescriptor[];
  genreKey?: string;
  genreControl?: ReactNode;
};

export type MetadataEditorCreditView = {
  uiKey: string;
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
  nameInputId?: string;
  nameRequired?: boolean;
  nameError?: string;
  mbidKey?: string;
  sortKey?: string;
  sortLabel?: string;
  showInstrument: boolean;
  roleControl?: ReactNode;
  artistLink?: ReactNode;
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
  modeControl?: ReactNode;
  emptyText?: string;
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
  worksContent?: ReactNode;
  movementContent?: ReactNode;
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
  labels: MetadataEditorLabels;
  label: string;
  editing: boolean;
  saving: boolean;
  readOnly: boolean;
  editOrSaveDisabled?: boolean;
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
  artistCredits?: ReactNode;
  roles?: MetadataEditorRolesView;
  worksMovement?: MetadataEditorWorksMovementView;
  sections: readonly MetadataEditorSectionView[];
  lyrics: readonly MetadataEditorSectionView[];
  extras?: MetadataEditorExtrasView;
  notices?: ReactNode;
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
  labels,
  label,
  editing,
  saving,
  readOnly,
  editOrSaveDisabled,
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
  artistCredits,
  roles,
  worksMovement,
  sections,
  lyrics,
  extras,
  notices,
  error,
}: MetadataEditorViewProps) {
  return (
    <section
      ref={editorRef}
      aria-label={label}
      tabIndex={-1}
      className="grid min-w-0 gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3"
      onKeyDown={onKeyDown}
    >
      <MetadataCardActions
        locale={locale}
        title={label}
        editing={editing}
        saving={saving}
        editOrSaveDisabled={editOrSaveDisabled ?? readOnly}
        hidden={hideActions}
        actionsRef={actionsRef}
        onEditOrSave={onEditOrSave}
        onCancel={onCancel}
      />
      {showForeignFieldsNotice && (
        <p className="m-0 text-xs text-[var(--muted)]">
          {labels.foreignFields}
        </p>
      )}
      {basics && (
        <MetadataBasicsSection locale={locale} fields={basics.fields}>
          {basics.genreControl ? (
            basics.genreKey ? (
              <div key={basics.genreKey} {...marker([basics.genreKey])}>
                {basics.genreControl}
              </div>
            ) : (
              basics.genreControl
            )
          ) : null}
        </MetadataBasicsSection>
      )}
      <ProviderGenreBaselineRow
        baseline={providerGenreBaseline}
        locale={locale}
      />
      {artistCredits}
      {artists && (
        <div className="grid gap-2 rounded border border-[var(--line)] p-2">
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <strong>{artists.groupLabel}</strong>
            <span className="flex flex-wrap items-center text-[var(--muted)]">
              {artists.summaryCredits.length ? (
                artists.summaryCredits.map((credit) => (
                  <span key={credit.uiKey}>
                    {credit.displayName}
                    {credit.joinPhrase}
                  </span>
                ))
              ) : (
                <span>{labels.noCredits}</span>
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
                  key={credit.uiKey}
                  className="grid min-w-0 gap-2 sm:grid-cols-2 lg:grid-cols-4"
                >
                  <label className="grid gap-1 text-xs text-[var(--muted)]">
                    <span>{labels.linkedArtist}</span>
                    <select
                      {...marker([artists.creditKeys[0] ?? ""])}
                      aria-label={`${labels.linkedArtist} ${index + 1}`}
                      className={inputClass}
                      disabled={
                        !artists.editing || !artists.artistOptions.length
                      }
                      value={credit.artistId}
                      onChange={(event) =>
                        artists.onChangeArtist(index, event.target.value)
                      }
                    >
                      <option value="">{labels.unlinked}</option>
                      {artists.artistOptions.map((artist) => (
                        <option key={artist.id} value={artist.id}>
                          {artist.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <KeyInput
                    fieldKey={artists.creditKeys[0]}
                    label={labels.creditName}
                    value={credit.creditName}
                    readOnly={!artists.editing}
                    onChange={(creditName) =>
                      artists.onChangeCreditName(index, creditName)
                    }
                  />
                  <KeyInput
                    fieldKey={artists.creditKeys[2]}
                    label={labels.artistMusicBrainz}
                    value={credit.mbid}
                    readOnly
                    onChange={() => undefined}
                  />
                  <KeyInput
                    fieldKey="credit-joinphrase"
                    label={labels.joinPhrase}
                    value={credit.joinPhrase}
                    readOnly={!artists.editing}
                    onChange={(joinPhrase) =>
                      artists.onChangeJoinPhrase(index, joinPhrase)
                    }
                  />
                  <KeyInput
                    fieldKey={artists.sortFieldKey}
                    label={labels.artistSort}
                    value={credit.sortName}
                    readOnly
                    onChange={() => undefined}
                  />
                  {artists.showRowActions && (
                    <div className="flex gap-1">
                      <Button
                        aria-label={`${labels.moveUp} ${labels.artistSort} ${index + 1}`}
                        isDisabled={index === 0}
                        onPress={() => artists.onMove(index, -1)}
                      >
                        ↑
                      </Button>
                      <Button
                        aria-label={`${labels.moveDown} ${labels.artistSort} ${index + 1}`}
                        isDisabled={index === artists.creditCount - 1}
                        onPress={() => artists.onMove(index, 1)}
                      >
                        ↓
                      </Button>
                      <Button
                        aria-label={`${labels.remove} ${artists.groupLabel} ${index + 1}`}
                        onPress={() => artists.onRemove(index)}
                      >
                        {labels.remove}
                      </Button>
                    </div>
                  )}
                </div>
              ))}
              {artists.showAdd && (
                <Button className="justify-self-start" onPress={artists.onAdd}>
                  {labels.addArtist}
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
              {roles.roles.length === 0 && roles.emptyText && (
                <p className="m-0 text-sm text-[var(--muted)]">
                  {roles.emptyText}
                </p>
              )}
              {roles.roles.map((role) => (
                <ContributorRoleSectionView key={role.role} label={role.label}>
                  {role.contributors.map((contributor, index) => {
                    const items: ContributorRowItem[] = [
                      {
                        key: "name",
                        id: contributor.nameInputId,
                        label: labels.contributorName,
                        value: contributor.name,
                        dataKeys: contributor.nameInputId
                          ? [contributor.nameKey, contributor.nameInputId]
                          : [contributor.nameKey],
                        readOnly: !role.editing,
                        disabled: saving,
                        required: contributor.nameRequired,
                        error: contributor.nameError,
                        onChange: (name: string) =>
                          role.onChange(index, { name }),
                      },
                      ...(contributor.roleControl
                        ? [
                            {
                              kind: "slot" as const,
                              key: "role",
                              content: contributor.roleControl,
                            },
                          ]
                        : []),
                      ...(contributor.mbidKey
                        ? [
                            {
                              key: "mbid",
                              label: labels.contributorMusicBrainz,
                              value: contributor.mbid ?? "",
                              dataKeys: [contributor.mbidKey],
                              readOnly: !role.editing,
                              disabled: saving,
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
                              disabled: saving,
                              onChange: (sortName: string) =>
                                role.onChange(index, { sortName }),
                            },
                          ]
                        : []),
                      ...(contributor.showInstrument
                        ? [
                            {
                              key: "instrument",
                              label: labels.instrument,
                              value: contributor.instrument ?? "",
                              readOnly: !role.editing,
                              disabled: saving,
                              onChange: (instrument: string) =>
                                role.onChange(index, { instrument }),
                            },
                          ]
                        : []),
                      ...(contributor.artistLink
                        ? [
                            {
                              kind: "slot" as const,
                              key: "artist-link",
                              content: contributor.artistLink,
                            },
                          ]
                        : []),
                    ];
                    return (
                      <fieldset
                        key={contributor.uiKey}
                        className="m-0 grid min-w-0 gap-2 border-0 p-0 sm:grid-cols-2 lg:grid-cols-4"
                        aria-label={`${role.label} ${index + 1}`}
                      >
                        <ContributorRowView
                          items={items}
                          actions={
                            role.editing
                              ? {
                                  moveUp: {
                                    label: `${labels.moveUp}: ${role.label} ${index + 1}`,
                                    disabled: saving || index === 0,
                                    onPress: () => role.onMove(index, -1),
                                  },
                                  moveDown: {
                                    label: `${labels.moveDown}: ${role.label} ${index + 1}`,
                                    disabled:
                                      saving ||
                                      index === role.contributorCount - 1,
                                    onPress: () => role.onMove(index, 1),
                                  },
                                  remove: {
                                    label: `${labels.remove} ${role.label} ${index + 1}`,
                                    text: labels.remove,
                                    disabled: saving,
                                    onPress: () => role.onRemove(index),
                                  },
                                }
                              : undefined
                          }
                        />
                      </fieldset>
                    );
                  })}
                  {role.editing && (
                    <Button
                      className="justify-self-start"
                      isDisabled={saving}
                      onPress={role.onAdd}
                    >
                      {labels.addContributor}
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
                    disabled={saving}
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
              {roles.modeControl}
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
              worksMovement.worksContent ??
              (worksMovement.works ? (
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
              ) : undefined)
            }
            movement={
              worksMovement.movementContent ??
              (worksMovement.movement ? (
                <MovementFieldsView
                  locale={locale}
                  value={worksMovement.movement.value}
                  editing={worksMovement.movement.editing}
                  disabled={worksMovement.movement.disabled}
                  onChange={worksMovement.movement.onChange}
                />
              ) : undefined)
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
      {notices}
      {error && (
        <p role="alert" className="m-0 text-sm text-[var(--error)]">
          {error}
        </p>
      )}
    </section>
  );
}
