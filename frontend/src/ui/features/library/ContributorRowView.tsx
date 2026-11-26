import { Fragment, type ReactNode, useId } from "react";
import { AppButton } from "../../components/AppButton";

export type ContributorRowField = {
  key: string;
  id?: string;
  label: string;
  value: string;
  dataKeys?: string[];
  disabled?: boolean;
  readOnly?: boolean;
  required?: boolean;
  error?: string;
  /** Static text keeps the read-only presentation of the application editor. */
  display?: "input" | "text";
  emptyValue?: string;
  onChange?: (value: string) => void;
};

export type ContributorRowItem =
  | ({ kind?: "field" } & ContributorRowField)
  | { kind: "slot"; key: string; content: ReactNode };

export type ContributorRowAction = {
  label: string;
  disabled?: boolean;
  onPress: () => void;
};

export type ContributorRowActions = {
  moveUp: ContributorRowAction;
  moveDown: ContributorRowAction;
  remove: ContributorRowAction & { text: string };
};

const inputClassName =
  "min-h-9 w-full min-w-0 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] read-only:border-transparent read-only:bg-transparent focus-visible:outline-2 focus-visible:outline-[var(--focus)]";

function dataAttributes(keys?: string[]) {
  return keys?.length ? { "data-metadata-keys": keys.join(",") } : {};
}

function ContributorFieldView({ field }: { field: ContributorRowField }) {
  const fallbackId = useId();
  const inputId = field.id ?? fallbackId;
  if (field.display === "text") {
    return (
      <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
        <span>{field.label}</span>
        <span className="min-h-9 break-words py-2 text-sm text-[var(--ink)]">
          {field.value || field.emptyValue}
        </span>
      </div>
    );
  }
  return (
    <div className="grid min-w-0 gap-1 text-xs text-[var(--muted)]">
      <label htmlFor={inputId}>
        {field.label}
        {field.required && <span aria-hidden="true"> *</span>}
      </label>
      <input
        {...dataAttributes(field.dataKeys)}
        id={inputId}
        className={inputClassName}
        value={field.value}
        disabled={field.disabled}
        readOnly={field.readOnly}
        required={field.required}
        aria-invalid={field.error ? true : undefined}
        aria-describedby={field.error ? `${inputId}-error` : undefined}
        onChange={(event) => field.onChange?.(event.currentTarget.value)}
      />
      {field.error && (
        <p
          id={`${inputId}-error`}
          role="alert"
          className="m-0 text-xs text-[var(--error)]"
        >
          {field.error}
        </p>
      )}
    </div>
  );
}

export function ContributorRoleSectionView({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="grid min-w-0 gap-2 border-b border-[var(--line)] pb-2 last:border-0">
      <h4 className="m-0 text-xs font-semibold">{label}</h4>
      {children}
    </div>
  );
}

export function ContributorRowView({
  items,
  actions,
}: {
  items: readonly ContributorRowItem[];
  actions?: ContributorRowActions;
}) {
  return (
    <>
      {items.map((item) =>
        item.kind === "slot" ? (
          <Fragment key={item.key}>{item.content}</Fragment>
        ) : (
          <ContributorFieldView key={item.key} field={item} />
        ),
      )}
      {actions ? (
        <div className="flex flex-wrap items-end gap-1">
          <AppButton
            type="button"
            aria-label={actions.moveUp.label}
            isDisabled={actions.moveUp.disabled}
            onPress={actions.moveUp.onPress}
            className="min-w-0 justify-center"
          >
            ↑
          </AppButton>
          <AppButton
            type="button"
            aria-label={actions.moveDown.label}
            isDisabled={actions.moveDown.disabled}
            onPress={actions.moveDown.onPress}
            className="min-w-0 justify-center"
          >
            ↓
          </AppButton>
          <AppButton
            type="button"
            aria-label={actions.remove.label}
            isDisabled={actions.remove.disabled}
            onPress={actions.remove.onPress}
            className="min-w-0 justify-center"
          >
            {actions.remove.text}
          </AppButton>
        </div>
      ) : null}
    </>
  );
}
