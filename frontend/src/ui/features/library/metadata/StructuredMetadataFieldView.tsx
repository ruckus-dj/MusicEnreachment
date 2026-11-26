import { useId } from "react";
import type { Locale } from "../../../i18n/types";
import { MovementFieldsView } from "../MovementFieldsView";
import { WorkRowsView } from "../WorkRowsView";
import { releaseMetadataEditorMessages } from "./releaseMetadataEditorMessages";
import {
  createMetadataDraftRowKey,
  type MetadataMovementDraft,
  type MetadataWorkDraft,
} from "./structuredMetadataDraft";

export type StructuredMetadataFieldState =
  | "absent"
  | "value"
  | "explicit_empty";

type StructuredMetadataFieldBaseProps = {
  locale: Locale;
  state: StructuredMetadataFieldState;
  editing: boolean;
  saving: boolean;
  onStateChange: (state: StructuredMetadataFieldState) => void;
};

export type StructuredMetadataFieldViewProps =
  StructuredMetadataFieldBaseProps &
    (
      | {
          field: "works";
          value: MetadataWorkDraft[];
          onValueChange: (value: MetadataWorkDraft[]) => void;
        }
      | {
          field: "movement";
          value: MetadataMovementDraft;
          onValueChange: (value: MetadataMovementDraft) => void;
        }
    );

const inputClassName =
  "min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]";

/**
 * Structured works/movement field composition. The provider owns draft state
 * and change handling; this view owns the mode control and nested editors.
 */
export function StructuredMetadataFieldView(
  props: StructuredMetadataFieldViewProps,
) {
  const controlPrefix = useId();
  const text = releaseMetadataEditorMessages[props.locale];
  const label = text.labels[props.field];
  return (
    <div className="grid min-w-0 gap-1.5">
      {props.editing ? (
        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
          <label htmlFor={`${controlPrefix}-state`}>{text.mode}</label>
          <select
            id={`${controlPrefix}-state`}
            aria-label={`${label} — ${text.mode}`}
            className={inputClassName}
            value={props.state}
            disabled={props.saving}
            onChange={(event) =>
              props.onStateChange(
                event.currentTarget.value as StructuredMetadataFieldState,
              )
            }
          >
            <option value="absent">{text.absent}</option>
            <option value="value">{text.value}</option>
            <option value="explicit_empty">{text.explicitEmpty}</option>
          </select>
        </div>
      ) : null}

      {props.state === "absent" ? (
        <span className="text-sm text-[var(--muted)]">{text.noOverride}</span>
      ) : props.state === "explicit_empty" ? (
        <span className="text-sm text-[var(--muted)]">
          {text.explicitEmpty}
        </span>
      ) : props.field === "works" ? (
        <WorkRowsView
          locale={props.locale}
          rows={props.value.map((work) => ({
            uiKey: work.uiKey,
            title: work.name,
            ...(work.mbid !== undefined ? { mbid: work.mbid } : {}),
          }))}
          editing={props.editing}
          disabled={props.saving}
          onChange={(rows) =>
            props.onValueChange(
              rows.map((row) => ({
                uiKey: row.uiKey,
                name: row.title,
                ...(row.mbid !== undefined ? { mbid: row.mbid } : {}),
              })),
            )
          }
          onAdd={() =>
            props.onValueChange([
              ...props.value,
              { uiKey: createMetadataDraftRowKey(), name: "" },
            ])
          }
        />
      ) : (
        <MovementFieldsView
          locale={props.locale}
          value={props.value}
          editing={props.editing}
          disabled={props.saving}
          emptyValueLabel={text.emptyValue}
          onChange={(key, value) =>
            props.onValueChange({ ...props.value, [key]: value })
          }
        />
      )}
    </div>
  );
}
