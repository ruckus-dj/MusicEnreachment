import { useId } from "react";
import { AppButton } from "../../components/AppButton";

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
  workNameLabel: string;
  workMBIDLabel: string;
  movementNameLabel: string;
  movementNumberLabel: string;
  movementCountLabel: string;
  addWorkLabel: string;
  moveWorkUpLabel: (index: number) => string;
  moveWorkDownLabel: (index: number) => string;
  removeWorkLabel: (index: number) => string;
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
        <div className="grid min-w-0 gap-2">
          {props.value.length ? (
            props.value.map((work, index) => (
              <div
                key={work.uiKey}
                className="grid min-w-0 gap-2 rounded border border-[var(--line)] p-2 sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_auto]"
              >
                {props.editing ? (
                  <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                    <label htmlFor={`${controlPrefix}-${work.uiKey}-name`}>
                      {`${props.workNameLabel} ${index + 1}`}
                    </label>
                    <input
                      id={`${controlPrefix}-${work.uiKey}-name`}
                      className={inputClassName}
                      value={work.name}
                      disabled={props.saving}
                      onChange={(event) => {
                        const value = [...props.value];
                        value[index] = {
                          ...work,
                          name: event.currentTarget.value,
                        };
                        props.onValueChange(value);
                      }}
                    />
                  </div>
                ) : (
                  <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                    <span>{`${props.workNameLabel} ${index + 1}`}</span>
                    <span className="min-h-9 break-words py-2 text-sm text-[var(--ink)]">
                      {work.name || props.emptyValueLabel}
                    </span>
                  </div>
                )}
                {work.mbid ? (
                  <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
                    <span>{props.workMBIDLabel}</span>
                    <output className="min-h-9 break-all py-2 text-sm text-[var(--ink)]">
                      {work.mbid}
                    </output>
                  </div>
                ) : null}
                {props.editing ? (
                  <div className="flex flex-wrap items-end gap-1">
                    <AppButton
                      type="button"
                      aria-label={props.moveWorkUpLabel(index + 1)}
                      isDisabled={props.saving || index === 0}
                      onPress={() => {
                        const value = [...props.value];
                        [value[index - 1], value[index]] = [
                          value[index],
                          value[index - 1],
                        ];
                        props.onValueChange(value);
                      }}
                      className="min-w-0 justify-center"
                    >
                      ↑
                    </AppButton>
                    <AppButton
                      type="button"
                      aria-label={props.moveWorkDownLabel(index + 1)}
                      isDisabled={
                        props.saving || index === props.value.length - 1
                      }
                      onPress={() => {
                        const value = [...props.value];
                        [value[index], value[index + 1]] = [
                          value[index + 1],
                          value[index],
                        ];
                        props.onValueChange(value);
                      }}
                      className="min-w-0 justify-center"
                    >
                      ↓
                    </AppButton>
                    <AppButton
                      type="button"
                      aria-label={props.removeWorkLabel(index + 1)}
                      isDisabled={props.saving}
                      onPress={() =>
                        props.onValueChange(
                          props.value.filter((_, row) => row !== index),
                        )
                      }
                      className="min-w-0 justify-center"
                    >
                      {props.removeWorkLabel(index + 1)}
                    </AppButton>
                  </div>
                ) : null}
              </div>
            ))
          ) : (
            <span className="text-sm text-[var(--muted)]">
              {props.emptyValueLabel}
            </span>
          )}
          {props.editing ? (
            <AppButton
              type="button"
              isDisabled={props.saving}
              onPress={() =>
                props.onValueChange([
                  ...props.value,
                  { uiKey: createMetadataDraftRowKey(), name: "" },
                ])
              }
              className="min-w-0 justify-center justify-self-start"
            >
              {props.addWorkLabel}
            </AppButton>
          ) : null}
        </div>
      ) : (
        <div className="grid min-w-0 gap-2 sm:grid-cols-3">
          {(
            [
              ["name", props.movementNameLabel],
              ["number", props.movementNumberLabel],
              ["count", props.movementCountLabel],
            ] as const
          ).map(([key, label]) => (
            <div
              key={key}
              className="grid min-w-0 gap-1 text-xs text-[var(--muted)]"
            >
              {props.editing ? (
                <>
                  <label htmlFor={`${controlPrefix}-movement-${key}`}>
                    {label}
                  </label>
                  <input
                    id={`${controlPrefix}-movement-${key}`}
                    className={inputClassName}
                    inputMode={key === "name" ? undefined : "numeric"}
                    value={props.value[key]}
                    disabled={props.saving}
                    onChange={(event) =>
                      props.onValueChange({
                        ...props.value,
                        [key]: event.currentTarget.value,
                      })
                    }
                  />
                </>
              ) : (
                <>
                  <span>{label}</span>
                  <span className="min-h-9 break-words py-2 text-sm text-[var(--ink)]">
                    {props.value[key] || props.emptyValueLabel}
                  </span>
                </>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
