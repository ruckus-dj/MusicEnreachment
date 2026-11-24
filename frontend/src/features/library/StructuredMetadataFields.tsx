import { useId } from "react";
import { MovementFieldsView } from "./MovementFieldsView";
import { WorkRowsView } from "./WorkRowsView";

let nextMetadataDraftRowKey = 0;

export function createMetadataDraftRowKey() {
  nextMetadataDraftRowKey += 1;
  return `metadata-draft-row-${nextMetadataDraftRowKey}`;
}

export type MetadataWorkDraft = {
  uiKey: string;
  name: string;
  mbid?: string;
};
export type MetadataMovementDraft = {
  name: string;
  number: string;
  count: string;
};

export function readMetadataWorkDrafts(value: unknown): MetadataWorkDraft[] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((item) => {
    if (!item || typeof item !== "object" || Array.isArray(item)) return [];
    const work = item as Record<string, unknown>;
    if (typeof work.name !== "string") return [];
    return [
      {
        uiKey: createMetadataDraftRowKey(),
        name: work.name,
        ...(typeof work.mbid === "string" ? { mbid: work.mbid } : {}),
      },
    ];
  });
}

export function readMetadataMovementDraft(
  value: unknown,
): MetadataMovementDraft {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return { name: "", number: "", count: "" };
  }
  const movement = value as Record<string, unknown>;
  return {
    name: typeof movement.name === "string" ? movement.name : "",
    number:
      typeof movement.number === "number" || typeof movement.number === "string"
        ? String(movement.number)
        : "",
    count:
      typeof movement.count === "number" || typeof movement.count === "string"
        ? String(movement.count)
        : "",
  };
}

type FieldState = "absent" | "value" | "explicit_empty";

type StructuredMetadataFieldProps = {
  locale: "en" | "ru";
  label: string;
  state: FieldState;
  editing: boolean;
  saving: boolean;
  modeLabel: string;
  absentLabel: string;
  valueLabel: string;
  explicitEmptyLabel: string;
  noOverrideLabel: string;
  emptyValueLabel: string;
  onStateChange: (state: FieldState) => void;
} & (
  | {
      kind: "works";
      value: MetadataWorkDraft[];
      onValueChange: (value: MetadataWorkDraft[]) => void;
    }
  | {
      kind: "movement";
      value: MetadataMovementDraft;
      onValueChange: (value: MetadataMovementDraft) => void;
    }
);

const inputClassName =
  "min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]";

export function StructuredMetadataField(props: StructuredMetadataFieldProps) {
  const controlPrefix = useId();
  return (
    <div className="grid min-w-0 gap-1.5">
      {props.editing ? (
        <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
          <label htmlFor={`${controlPrefix}-state`}>{props.modeLabel}</label>
          <select
            id={`${controlPrefix}-state`}
            aria-label={`${props.label} — ${props.modeLabel}`}
            className={inputClassName}
            value={props.state}
            disabled={props.saving}
            onChange={(event) =>
              props.onStateChange(event.currentTarget.value as FieldState)
            }
          >
            <option value="absent">{props.absentLabel}</option>
            <option value="value">{props.valueLabel}</option>
            <option value="explicit_empty">{props.explicitEmptyLabel}</option>
          </select>
        </div>
      ) : null}

      {props.state === "absent" ? (
        <span className="text-sm text-[var(--muted)]">
          {props.noOverrideLabel}
        </span>
      ) : props.state === "explicit_empty" ? (
        <span className="text-sm text-[var(--muted)]">
          {props.explicitEmptyLabel}
        </span>
      ) : props.kind === "works" ? (
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
          emptyValueLabel={props.emptyValueLabel}
          onChange={(key, value) =>
            props.onValueChange({ ...props.value, [key]: value })
          }
        />
      )}
    </div>
  );
}
