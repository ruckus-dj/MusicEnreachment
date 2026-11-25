import type { ComponentProps, ReactNode } from "react";
import { Button, Checkbox } from "react-aria-components";
import {
  actions,
  errorClass,
  type Field,
  muted,
  panel,
  primary,
  secondary,
} from "../local-release/localReleaseUi";
import { OwnCatalogRecordFieldsView } from "./OwnCatalogRecordFieldsView";

type FieldInput = ComponentProps<typeof Field>;

const checkboxClass =
  "react-aria-Checkbox ui-checkbox inline-flex min-h-8 pointer-coarse:min-h-[40px] cursor-pointer items-center gap-[7px] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2 [&_.checkbox]:grid [&_.checkbox]:size-4 [&_.checkbox]:place-items-center [&_.checkbox]:border [&_.checkbox]:border-[var(--control)] [&_.checkbox]:bg-[var(--surface)] [&_.checkbox]:text-[var(--button-text)] data-[selected]:[&_.checkbox]:border-[var(--accent)] data-[selected]:[&_.checkbox]:bg-[var(--accent)]";

export type OwnCatalogRecordEditorChoice = {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: readonly { id: string; label: string }[];
};

export type OwnCatalogRecordEditorAction = {
  label: string;
  disabled?: boolean;
  onPress: () => void;
};

export type OwnCatalogArtistLink = {
  id: string;
  name: string;
  provider: string;
  providerKey: string;
  externalNotice?: string;
};

export type OwnCatalogArtistLinks = {
  legend: string;
  explanation: string;
  artists: readonly OwnCatalogArtistLink[];
  selectedIds: readonly string[];
  onChange: (id: string, selected: boolean) => void;
};

function OwnCatalogArtistLinksView({
  legend,
  explanation,
  artists,
  selectedIds,
  onChange,
}: OwnCatalogArtistLinks) {
  return (
    <fieldset className="m-0 grid min-w-0 gap-1 border-0 p-0">
      <legend className="mb-1 p-0 text-xs font-semibold">{legend}</legend>
      <p className={muted}>{explanation}</p>
      <div className="flex flex-wrap gap-2">
        {artists.map((artist) => (
          <div className="flex items-center gap-1" key={artist.id}>
            <Checkbox
              className={checkboxClass}
              isSelected={selectedIds.includes(artist.id)}
              onChange={(selected) => onChange(artist.id, selected)}
            >
              {artist.name}
            </Checkbox>
            <span className={muted}>
              · {artist.provider}
              {artist.externalNotice ? ` · ${artist.externalNotice}` : ""} ·{" "}
              <code>{artist.providerKey}</code>
            </span>
          </div>
        ))}
      </div>
    </fieldset>
  );
}

export function OwnCatalogRecordEditorView({
  notice,
  help,
  fields,
  choice,
  fieldError,
  error,
  genre,
  extra,
  artistLinks,
  save,
  cancel,
}: {
  notice: string;
  help?: string;
  fields: readonly FieldInput[];
  choice?: OwnCatalogRecordEditorChoice;
  fieldError?: string;
  error?: string;
  genre?: ReactNode;
  extra?: ReactNode;
  artistLinks?: OwnCatalogArtistLinks;
  save: OwnCatalogRecordEditorAction;
  cancel: OwnCatalogRecordEditorAction;
}) {
  return (
    <section className={`${panel} grid gap-3`}>
      <OwnCatalogRecordFieldsView
        notice={notice}
        help={help}
        fields={fields}
        choice={choice}
      />
      {fieldError && (
        <p className={errorClass} role="alert">
          {fieldError}
        </p>
      )}
      {genre}
      {extra}
      {artistLinks && <OwnCatalogArtistLinksView {...artistLinks} />}
      {error && (
        <p className={errorClass} role="alert">
          {error}
        </p>
      )}
      <div className={actions}>
        <Button
          className={primary}
          isDisabled={save.disabled}
          onPress={save.onPress}
        >
          {save.label}
        </Button>
        <Button
          className={secondary}
          isDisabled={cancel.disabled}
          onPress={cancel.onPress}
        >
          {cancel.label}
        </Button>
      </div>
    </section>
  );
}
