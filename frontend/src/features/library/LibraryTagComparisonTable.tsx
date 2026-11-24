import { Checkbox } from "react-aria-components";

const tableWrap =
  "min-w-0 overflow-auto rounded-[5px] border border-[var(--line)] bg-[var(--surface)]";
const headingClass =
  "sticky top-0 z-[1] border-b border-[var(--line)] bg-[var(--raised)] p-2 text-left align-top wrap-anywhere";
const comparisonRow =
  "[&:hover]:bg-[color-mix(in_srgb,var(--accent-soft)_36%,var(--surface))]";
const rowHeading =
  "border-b border-[var(--line)] p-[7px] text-left align-top font-semibold";
const cellClass =
  "border-b border-[var(--line)] p-2 text-left align-top wrap-anywhere";

export const libraryTagComparisonCopy = {
  en: {
    field: "Field",
    source: "Source file",
    current: "Current publication",
    differencesOnly: "Differences only",
  },
  ru: {
    field: "Поле",
    source: "Исходный файл",
    current: "Сейчас в публикации",
    differencesOnly: "Только отличия",
  },
};

export function LibraryTagDifferencesControl({
  locale,
  selected,
  onChange,
}: {
  locale: "en" | "ru";
  selected: boolean;
  onChange: (selected: boolean) => void;
}) {
  return (
    <Checkbox
      isSelected={selected}
      onChange={onChange}
      className="react-aria-Checkbox ui-checkbox inline-flex min-h-8 cursor-pointer items-center gap-[7px] pointer-coarse:min-h-10 data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2 [&_.checkbox]:grid [&_.checkbox]:size-4 [&_.checkbox]:place-items-center [&_.checkbox]:border [&_.checkbox]:border-[var(--control)] [&_.checkbox]:bg-[var(--surface)] [&_.checkbox]:text-[var(--button-text)] data-[selected]:[&_.checkbox]:border-[var(--accent)] data-[selected]:[&_.checkbox]:bg-[var(--accent)]"
    >
      <span className="checkbox" aria-hidden="true" />
      {libraryTagComparisonCopy[locale].differencesOnly}
    </Checkbox>
  );
}

export type LibraryTagComparisonColumn = {
  id: string;
  label: string;
  unavailable?: string;
};

export type LibraryTagComparisonRow = {
  key: string;
  label?: string;
  values: readonly (readonly string[] | null)[];
  different?: boolean;
};

export function LibraryTagComparisonTable({
  columns,
  fieldLabel,
  rows,
}: {
  columns: readonly LibraryTagComparisonColumn[];
  fieldLabel: string;
  rows: readonly LibraryTagComparisonRow[];
}) {
  return (
    <div className={tableWrap}>
      <table className="w-full min-w-[720px] border-collapse text-left text-[11px]">
        <thead>
          <tr>
            <th className={headingClass} scope="col">
              {fieldLabel}
            </th>
            {columns.map(({ id, label }) => (
              <th className={headingClass} scope="col" key={id}>
                {label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map(({ key, label, values, different }) => (
            <tr className={comparisonRow} key={key}>
              <th
                scope="row"
                className={`${rowHeading} ${different ? "font-bold! text-[var(--warning)]" : ""}`}
              >
                {label ? `${label} · ` : ""}
                {key}
              </th>
              {columns.map(({ id, unavailable }, index) => {
                const value = values[index];
                return (
                  <td className={cellClass} key={id}>
                    {value == null ? (
                      <span className="block text-[11px] text-[var(--muted)]">
                        {unavailable ?? "—"}
                      </span>
                    ) : value.length ? (
                      <span className="whitespace-pre-wrap">
                        {value.join("\n")}
                      </span>
                    ) : (
                      "—"
                    )}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
