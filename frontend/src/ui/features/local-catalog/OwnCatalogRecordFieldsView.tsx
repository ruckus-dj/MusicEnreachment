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
import { AppIcon } from "../../components/AppIcon";
import { ui } from "../../components/uiStyles";
import { Field, muted } from "../local-release/localReleaseUi";

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
      <div className="grid gap-4 sm:grid-cols-2">
        {fields.map((field) => (
          <Field key={field.label} {...field} />
        ))}
        {choice && (
          <Select
            className="grid gap-1.5"
            selectedKey={choice.value}
            onSelectionChange={(key) => choice.onChange(String(key ?? ""))}
          >
            <Label className="text-sm font-medium text-[var(--ink)]">
              {choice.label}
            </Label>
            <Button className={ui.selectTrigger}>
              <SelectValue className={ui.selectValue} />
              <AppIcon
                name="chevronDown"
                className="size-4 shrink-0 text-[var(--muted)]"
              />
            </Button>
            <Popover className={ui.popover}>
              <ListBox className={ui.listBox}>
                {choice.options.map((option) => (
                  <ListBoxItem
                    key={option.id}
                    id={option.id}
                    className={ui.option}
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
