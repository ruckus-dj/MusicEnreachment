import { useId, useState } from "react";
import { AppButton } from "../../components/AppButton";

export type MetadataFieldState = "absent" | "value" | "explicit_empty";

export type MetadataFieldKind =
  | "text"
  | "date"
  | "list"
  | "boolean"
  | "integer"
  | "number"
  | "advisory"
  | "lyrics";

export type MetadataFieldChoice = { value: string; label: string };

export type MetadataFieldMode = {
  label: string;
  absent: string;
  value: string;
  explicitEmpty: string;
};

export type MetadataListActions = {
  add: string;
  moveUp: (index: number) => string;
  moveDown: (index: number) => string;
  remove: (index: number) => string;
};

export type MetadataFieldDescriptor = {
  key: string;
  label: string;
  kind: MetadataFieldKind;
  value: string | string[];
  editing: boolean;
  disabled?: boolean;
  /** Static fields show text when not editing; control fields stay visible. */
  presentation?: "static" | "control";
  displayValue?: string;
  state?: MetadataFieldState;
  mode?: MetadataFieldMode;
  choices?: MetadataFieldChoice[];
  emptyChoiceLabel?: string;
  hint?: string;
  placeholder?: string;
  inputMode?: "text" | "numeric" | "decimal";
  nativeType?: "text" | "number";
  dataKeys?: string[];
  notices?: string[];
  listMode?: "lines" | "rows";
  listActions?: MetadataListActions;
  onChange?: (value: string | string[]) => void;
  onStateChange?: (state: MetadataFieldState) => void;
};

const inputClass =
  "min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 text-sm text-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]";

function asArray(value: string | string[]): string[] {
  return Array.isArray(value) ? value : [];
}

function asScalar(value: string | string[]): string {
  return Array.isArray(value) ? (value[0] ?? "") : value;
}

function dataAttributes(keys: string[] | undefined) {
  if (!keys?.length) return {};
  return { "data-metadata-keys": keys.join(",") };
}

function ListRowsInput({ field }: { field: MetadataFieldDescriptor }) {
  const values = asArray(field.value);
  const rows = values.length ? values : [""];
  const actions = field.listActions;
  const [identity, setIdentity] = useState(() => ({
    keys: Array.from({ length: rows.length }, (_, key) => key),
    next: rows.length,
  }));
  let keys = identity.keys;
  if (keys.length !== rows.length) {
    const grow = Math.max(0, rows.length - keys.length);
    keys = [
      ...keys.slice(0, rows.length),
      ...Array.from({ length: grow }, (_, index) => identity.next + index),
    ];
    setIdentity({ keys, next: identity.next + grow });
  }

  function move(from: number, to: number) {
    const next = [...rows];
    const order = [...keys];
    [next[from], next[to]] = [next[to], next[from]];
    [order[from], order[to]] = [order[to], order[from]];
    setIdentity({ ...identity, keys: order });
    field.onChange?.(next);
  }
  return (
    <>
      {keys.map((key, index) => (
        <div key={key} className="relative min-w-0">
          <input
            {...dataAttributes(field.dataKeys)}
            aria-label={`${field.label}${rows.length > 1 ? ` ${index + 1}` : ""}`}
            readOnly={!field.editing}
            disabled={field.disabled}
            className={`${inputClass} ${field.editing ? "pr-28" : ""}`}
            value={rows[index]}
            onChange={(event) => {
              const next = rows.map((entry, row) =>
                row === index ? event.currentTarget.value : entry,
              );
              field.onChange?.(next);
            }}
          />
          {actions ? (
            <div
              aria-hidden={!field.editing}
              className={`absolute right-1 top-0 flex h-9 items-center gap-0.5 [&_button]:min-h-7 [&_button]:px-1 [&_button]:py-0 ${
                field.editing ? "" : "invisible"
              }`}
            >
              <AppButton
                type="button"
                aria-label={`${actions.moveUp(index + 1)}`}
                isDisabled={!!field.disabled || index === 0}
                onPress={() => move(index, index - 1)}
              >
                ↑
              </AppButton>
              <AppButton
                type="button"
                aria-label={`${actions.moveDown(index + 1)}`}
                isDisabled={!!field.disabled || index === rows.length - 1}
                onPress={() => move(index, index + 1)}
              >
                ↓
              </AppButton>
              <AppButton
                type="button"
                aria-label={actions.remove(index + 1)}
                isDisabled={!!field.disabled}
                onPress={() => {
                  if (rows.length > 1) {
                    setIdentity({
                      ...identity,
                      keys: keys.filter((_, row) => row !== index),
                    });
                  }
                  field.onChange?.(rows.filter((_, row) => row !== index));
                }}
              >
                ×
              </AppButton>
              {index === rows.length - 1 ? (
                <AppButton
                  type="button"
                  aria-label={actions.add}
                  isDisabled={!!field.disabled}
                  onPress={() => field.onChange?.([...rows, ""])}
                >
                  +
                </AppButton>
              ) : null}
            </div>
          ) : null}
        </div>
      ))}
    </>
  );
}

function FieldControl({ field }: { field: MetadataFieldDescriptor }) {
  const scalar = asScalar(field.value);
  if (field.kind === "list" && field.listMode === "rows") {
    return <ListRowsInput field={field} />;
  }
  if (field.kind === "list") {
    return (
      <textarea
        {...dataAttributes(field.dataKeys)}
        aria-label={field.label}
        className={`${inputClass} min-h-20 resize-y py-2`}
        value={asArray(field.value).join("\n")}
        disabled={field.disabled}
        readOnly={!field.editing}
        onChange={(event) =>
          field.onChange?.(event.currentTarget.value.split("\n"))
        }
      />
    );
  }
  if (field.kind === "boolean" || field.kind === "advisory") {
    return (
      <select
        {...dataAttributes(field.dataKeys)}
        aria-label={field.label}
        className={inputClass}
        value={scalar}
        disabled={field.disabled || !field.editing}
        onChange={(event) => {
          if (field.editing) field.onChange?.(event.currentTarget.value);
        }}
      >
        {field.emptyChoiceLabel ? (
          <option value="">{field.emptyChoiceLabel}</option>
        ) : null}
        {(field.choices ?? []).map((choice) => (
          <option key={choice.value} value={choice.value}>
            {choice.label}
          </option>
        ))}
      </select>
    );
  }
  if (field.kind === "lyrics") {
    return (
      <textarea
        {...dataAttributes(field.dataKeys)}
        aria-label={field.label}
        className={`${inputClass} h-24 resize-y py-2`}
        value={scalar}
        disabled={field.disabled}
        readOnly={!field.editing}
        onChange={(event) => field.onChange?.(event.currentTarget.value)}
      />
    );
  }
  return (
    <input
      {...dataAttributes(field.dataKeys)}
      aria-label={field.label}
      className={inputClass}
      type={field.nativeType ?? "text"}
      inputMode={field.inputMode}
      placeholder={field.placeholder}
      min={field.kind === "integer" ? "0" : undefined}
      step={
        field.kind === "integer"
          ? "1"
          : field.kind === "number"
            ? "any"
            : undefined
      }
      value={scalar}
      disabled={field.disabled}
      readOnly={!field.editing}
      onChange={(event) => field.onChange?.(event.currentTarget.value)}
    />
  );
}

const fieldLabelIdPrefix = "metadata-field";

export function MetadataFieldView({
  field,
}: {
  field: MetadataFieldDescriptor;
}) {
  const controlPrefix = useId();
  const controlId = `${fieldLabelIdPrefix}-${controlPrefix}-${field.key}`;
  const mode = field.mode;
  const showControl = field.presentation === "control" || field.editing;
  return (
    <div className="grid min-w-0 gap-1 rounded border border-[var(--line)] p-2">
      <dt className="text-xs text-[var(--muted)]">{field.label}</dt>
      <dd className="m-0 min-w-0 text-sm">
        {showControl ? (
          <div className="grid min-w-0 gap-1.5">
            {field.editing && field.state && mode && field.onStateChange ? (
              <label
                htmlFor={`${controlId}-state`}
                className="grid min-w-0 gap-1 text-xs text-[var(--muted)]"
              >
                <span>{mode.label}</span>
                <select
                  id={`${controlId}-state`}
                  aria-label={`${field.label} — ${mode.label}`}
                  className={inputClass}
                  value={field.state}
                  disabled={field.disabled}
                  onChange={(event) =>
                    field.onStateChange?.(
                      event.currentTarget.value as MetadataFieldState,
                    )
                  }
                >
                  <option value="absent">{mode.absent}</option>
                  <option value="value">{mode.value}</option>
                  <option value="explicit_empty">{mode.explicitEmpty}</option>
                </select>
              </label>
            ) : null}
            {(!field.mode || field.state === "value") && (
              <FieldControl field={field} />
            )}
            {field.hint && (!field.mode || field.state === "value") ? (
              <span className="text-xs text-[var(--muted)]">{field.hint}</span>
            ) : null}
          </div>
        ) : (
          <>
            <span className="block whitespace-pre-wrap break-words">
              {field.displayValue ??
                (Array.isArray(field.value)
                  ? field.value.join("\n")
                  : field.value)}
            </span>
            {field.notices?.map((notice) => (
              <span
                key={notice}
                className="mt-1 block text-xs text-[var(--muted)]"
              >
                {notice}
              </span>
            ))}
          </>
        )}
      </dd>
    </div>
  );
}

export function MetadataFieldsGrid({
  fields,
  columns = 3,
}: {
  fields: MetadataFieldDescriptor[];
  columns?: 1 | 2 | 3 | 4;
}) {
  if (!fields.length) return null;
  const columnClass =
    columns === 1
      ? "grid-cols-1"
      : columns === 2
        ? "grid-cols-1 sm:grid-cols-2"
        : columns === 4
          ? "grid-cols-1 sm:grid-cols-2 lg:grid-cols-4"
          : "grid-cols-1 sm:grid-cols-2 lg:grid-cols-3";
  return (
    <dl className={`m-0 grid min-w-0 gap-2 ${columnClass}`}>
      {fields.map((field) => (
        <MetadataFieldView key={field.key} field={field} />
      ))}
    </dl>
  );
}

const disclosureSummaryClass =
  "w-full cursor-pointer rounded border border-[var(--line)] p-2 text-sm font-semibold hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2";

export function MetadataFieldGroup({
  label,
  summary,
  fields,
  columns = 3,
  heading = false,
}: {
  label: string;
  summary?: string;
  fields: MetadataFieldDescriptor[];
  columns?: 1 | 2 | 3 | 4;
  heading?: boolean;
}) {
  if (!fields.length) return null;
  return (
    <details className="min-w-0">
      <summary className={disclosureSummaryClass}>
        {label}
        {summary ? ` · ${summary}` : ""}
      </summary>
      <section className="mt-2 grid min-w-0 gap-2 rounded border border-[var(--line)] p-2">
        {heading ? (
          <h3 className="m-0 text-sm font-semibold">{label}</h3>
        ) : null}
        <MetadataFieldsGrid fields={fields} columns={columns} />
      </section>
    </details>
  );
}
