import type { ComponentProps } from "react";
import {
  Button,
  Label,
  ListBox,
  ListBoxItem,
  Popover,
  Select,
  SelectValue,
} from "react-aria-components";
import { Field, muted, secondary } from "../local-release/localReleaseUi";

type FieldInput = ComponentProps<typeof Field>;
type ChoiceField = {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: readonly { id: string; label: string }[];
};

export function OwnCatalogRecordFieldsView({
  notice,
  fields,
  choice,
  help,
}: {
  notice: string;
  fields: readonly FieldInput[];
  choice?: ChoiceField;
  help?: string;
}) {
  return (
    <>
      <p className={muted}>{notice}</p>
      <div className="grid gap-2 sm:grid-cols-2">
        {fields.map((field) => (
          <Field key={field.label} {...field} />
        ))}
        {choice && (
          <Select
            className="grid gap-1 text-xs font-medium"
            selectedKey={choice.value}
            onSelectionChange={(key) => choice.onChange(String(key ?? ""))}
          >
            <Label>{choice.label}</Label>
            <Button className={secondary}>
              <SelectValue />
            </Button>
            <Popover className="rounded border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)]">
              <ListBox>
                {choice.options.map((option) => (
                  <ListBoxItem
                    key={option.id}
                    id={option.id}
                    className="cursor-pointer rounded px-2 py-1 outline-none data-[focused]:bg-[var(--raised)]"
                  >
                    {option.label}
                  </ListBoxItem>
                ))}
              </ListBox>
            </Popover>
          </Select>
        )}
      </div>
      {help && <p className={muted}>{help}</p>}
    </>
  );
}
