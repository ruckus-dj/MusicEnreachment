import {
  type MetadataFieldDescriptor,
  MetadataFieldsGrid,
} from "./MetadataFieldPresentation";

type MovementFieldValues = {
  name: string;
  number: string;
  count: string;
};

type MovementField = keyof MovementFieldValues;

const labels = {
  en: {
    name: "Movement name",
    count: "Total movements",
    number: "Movement number",
  },
  ru: {
    name: "Название части",
    count: "Всего частей",
    number: "Номер части",
  },
} as const;

const movementFields = [
  ["name", "movementname", "text"],
  ["count", "movementtotal", "numeric"],
  ["number", "movement", "numeric"],
] as const;

export function MovementFieldsView({
  locale,
  value,
  editing,
  disabled = false,
  emptyValueLabel,
  onChange,
}: {
  locale: "ru" | "en";
  value: MovementFieldValues;
  editing: boolean;
  disabled?: boolean;
  emptyValueLabel?: string;
  onChange: (field: MovementField, value: string) => void;
}) {
  const fields: MetadataFieldDescriptor[] = movementFields.map(
    ([key, tag, inputMode]) => ({
      key: tag,
      label: labels[locale][key],
      kind: "text",
      value: value[key],
      editing,
      disabled,
      presentation: editing ? "control" : "static",
      displayValue: editing ? undefined : value[key] || emptyValueLabel,
      inputMode,
      dataKeys: [tag],
      onChange: (next) =>
        onChange(key, typeof next === "string" ? next : (next[0] ?? "")),
    }),
  );
  return <MetadataFieldsGrid columns={3} fields={fields} />;
}
