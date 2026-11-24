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
const checkboxClass =
  "react-aria-Checkbox ui-checkbox inline-flex min-h-8 cursor-pointer items-center gap-[7px] pointer-coarse:min-h-10 data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--focus)] data-[focus-visible]:outline-offset-2 [&_.checkbox]:grid [&_.checkbox]:size-4 [&_.checkbox]:place-items-center [&_.checkbox]:border [&_.checkbox]:border-[var(--control)] [&_.checkbox]:bg-[var(--surface)] [&_.checkbox]:text-[var(--button-text)] data-[selected]:[&_.checkbox]:border-[var(--accent)] data-[selected]:[&_.checkbox]:bg-[var(--accent)]";

export const libraryTagComparisonCopy = {
  en: {
    field: "Field",
    source: "Source file",
    current: "Current publication",
    differencesOnly: "Differences only",
    provider: "Provider",
    localOverride: "Local override",
    showAll: "Show all",
    cleared: "Cleared",
    providerLoading: "Provider tags are loading…",
    providerUnavailable: "Provider tags are unavailable.",
    providerUnlinked: "No linked provider for this position.",
    overridesUnavailable: "Local overrides are unavailable.",
    noDifferences: "No differences in the saved tags.",
    noTags: "No saved tag values to show.",
  },
  ru: {
    field: "Поле",
    source: "Исходный файл",
    current: "Сейчас в публикации",
    differencesOnly: "Только отличия",
    provider: "Провайдер",
    localOverride: "Локальное переопределение",
    showAll: "Показать все",
    cleared: "Очищено",
    providerLoading: "Теги провайдера загружаются…",
    providerUnavailable: "Теги провайдера недоступны.",
    providerUnlinked: "У этой позиции нет связанного провайдера.",
    overridesUnavailable: "Локальные переопределения недоступны.",
    noDifferences: "Сохранённые теги не отличаются.",
    noTags: "Нет сохранённых значений тегов.",
  },
};

function LibraryTagCheckbox({
  label,
  selected,
  onChange,
}: {
  label: string;
  selected: boolean;
  onChange: (selected: boolean) => void;
}) {
  return (
    <Checkbox
      isSelected={selected}
      onChange={onChange}
      className={checkboxClass}
    >
      <span className="checkbox" aria-hidden="true" />
      {label}
    </Checkbox>
  );
}

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
    <LibraryTagCheckbox
      label={libraryTagComparisonCopy[locale].differencesOnly}
      selected={selected}
      onChange={onChange}
    />
  );
}

export type LibraryTagComparisonColumn = {
  id: string;
  label: string;
  notice?: string;
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
            {columns.map(({ id, label, notice }) => (
              <th
                className={headingClass}
                aria-label={label}
                scope="col"
                key={id}
              >
                {label}
                {notice && (
                  <span
                    role="status"
                    className="block text-[var(--muted)] font-normal"
                  >
                    {notice}
                  </span>
                )}
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
              {columns.map(({ id }, index) => {
                const value = values[index];
                return (
                  <td className={cellClass} key={id}>
                    {value?.some((entry) => entry !== "") ? (
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

export type LibraryTagValues = Readonly<
  Record<string, readonly string[] | null>
>;

export type LibraryTagComparisonProvider = {
  name?: string;
  status: "loading" | "unavailable" | "unlinked" | "saved";
  tags?: LibraryTagValues;
};

export type LibraryTagComparisonKnownField = {
  key: string;
  label?: string;
};

export type LibraryTagEvidenceComparisonProps = {
  locale: "en" | "ru";
  source?: LibraryTagValues;
  current?: LibraryTagValues;
  provider: LibraryTagComparisonProvider;
  overrides?: LibraryTagValues;
  overridesUnavailable?: boolean;
  knownFields: readonly LibraryTagComparisonKnownField[];
  showAll: boolean;
  onShowAllChange: (selected: boolean) => void;
  differencesOnly: boolean;
  onDifferencesChange: (selected: boolean) => void;
  showDifferencesControl?: boolean;
};

type OverrideCell =
  | { kind: "unknown" }
  | { kind: "cleared" }
  | { kind: "values"; values: readonly string[] };

const isExplicitlyCleared = (values: readonly string[]) =>
  values.every((value) => value === "");

const hasTagValue = (values: readonly string[]) =>
  values.some((value) => value !== "");

const sameTagValues = (left: readonly string[], right: readonly string[]) =>
  left.length === right.length &&
  left.every((value, index) => value === right[index]);

function overrideCell(
  overrides: LibraryTagValues | undefined,
  key: string,
): OverrideCell {
  const values =
    overrides && Object.hasOwn(overrides, key) ? overrides[key] : undefined;
  if (values == null) return { kind: "unknown" };
  if (isExplicitlyCleared(values)) return { kind: "cleared" };
  return { kind: "values", values };
}

const columnTagValues = (tags: LibraryTagValues | undefined, key: string) =>
  tags ? (Object.hasOwn(tags, key) ? tags[key] : []) : null;

function comparisonKeys({
  source,
  current,
  provider,
  overrides,
  knownFields,
}: Pick<
  LibraryTagEvidenceComparisonProps,
  "source" | "current" | "provider" | "overrides" | "knownFields"
>) {
  const keys: string[] = [];
  const seen = new Set<string>();
  for (const { key } of knownFields) {
    if (!seen.has(key)) {
      seen.add(key);
      keys.push(key);
    }
  }
  const observed = new Set<string>();
  for (const tags of [
    source,
    current,
    provider.status === "saved" ? provider.tags : undefined,
    overrides,
  ]) {
    if (!tags) continue;
    for (const key of Object.keys(tags)) observed.add(key);
  }
  for (const key of [...observed].sort()) {
    if (!seen.has(key)) {
      seen.add(key);
      keys.push(key);
    }
  }
  return keys;
}

export function LibraryTagEvidenceComparison({
  locale,
  source,
  current,
  provider,
  overrides,
  overridesUnavailable,
  knownFields,
  showAll,
  onShowAllChange,
  differencesOnly,
  onDifferencesChange,
  showDifferencesControl = true,
}: LibraryTagEvidenceComparisonProps) {
  const copy = libraryTagComparisonCopy[locale];
  const canCompare = source !== undefined && current !== undefined;
  const filterDifferences = canCompare && differencesOnly;
  const providerTags = provider.status === "saved" ? provider.tags : undefined;
  const labelByKey = new Map<string, string | undefined>();
  for (const { key, label } of knownFields) {
    if (!labelByKey.has(key)) labelByKey.set(key, label);
  }
  const rows = comparisonKeys({
    source,
    current,
    provider,
    overrides,
    knownFields,
  }).map((key) => {
    const sourceValues = columnTagValues(source, key);
    const providerValues = columnTagValues(providerTags, key);
    const override = overrideCell(overrides, key);
    const overrideValues =
      override.kind === "cleared"
        ? [copy.cleared]
        : override.kind === "values"
          ? override.values
          : null;
    const currentValues = columnTagValues(current, key);
    const different =
      sourceValues != null &&
      currentValues != null &&
      ((source &&
        current &&
        Object.hasOwn(source, key) !== Object.hasOwn(current, key)) ||
        !sameTagValues(sourceValues, currentValues));
    const hasValue =
      (sourceValues != null && hasTagValue(sourceValues)) ||
      (providerValues != null && hasTagValue(providerValues)) ||
      override.kind === "cleared" ||
      (override.kind === "values" && hasTagValue(override.values)) ||
      (currentValues != null && hasTagValue(currentValues));
    return {
      key,
      label: labelByKey.get(key),
      values: [sourceValues, providerValues, overrideValues, currentValues],
      different,
      hasValue,
    };
  });
  const visibleRows = rows.filter((row) =>
    filterDifferences ? row.different : showAll || row.hasValue,
  );
  const providerNotice = {
    loading: copy.providerLoading,
    unavailable: copy.providerUnavailable,
    unlinked: copy.providerUnlinked,
    saved: undefined,
  }[provider.status];
  const providerLabel = provider.name
    ? `${copy.provider} · ${provider.name}`
    : copy.provider;

  return (
    <>
      <div className="flex flex-wrap items-center gap-3">
        <LibraryTagCheckbox
          label={copy.showAll}
          selected={showAll}
          onChange={onShowAllChange}
        />
        {showDifferencesControl && (
          <LibraryTagDifferencesControl
            locale={locale}
            selected={differencesOnly}
            onChange={onDifferencesChange}
          />
        )}
      </div>
      <LibraryTagComparisonTable
        fieldLabel={copy.field}
        columns={[
          { id: "source", label: copy.source },
          { id: "provider", label: providerLabel, notice: providerNotice },
          {
            id: "override",
            label: copy.localOverride,
            notice: overridesUnavailable
              ? copy.overridesUnavailable
              : undefined,
          },
          { id: "current", label: copy.current },
        ]}
        rows={visibleRows}
      />
      {!visibleRows.length && (
        <p className="m-0 text-sm text-[var(--muted)]">
          {filterDifferences ? copy.noDifferences : copy.noTags}
        </p>
      )}
    </>
  );
}
