import {
  Label,
  RadioButton,
  RadioField,
  RadioGroup,
} from "react-aria-components";
import { AppCheckbox } from "../../components/AppCheckbox";

const copy = {
  en: {
    format: "Publication format",
    source: "Source format",
    mka: "MKA remux",
    removeSources: "Remove source files after successful publication",
    cleanup:
      "When disabled, source files are kept. They are removed only after successful publication, never after a failure.",
  },
  ru: {
    format: "Формат публикации",
    source: "Исходный формат",
    mka: "Перепаковка в MKA",
    removeSources: "Удалять исходники после успешной публикации",
    cleanup:
      "Если флажок снят, исходники останутся. Они удаляются только после успешной публикации, не при ошибке.",
  },
};

export type SetupPublicationFormat = "source" | "mka" | "m4a";

export function SetupPublicationOptions({
  locale,
  format,
  removeSources,
  disabled,
  onFormat,
  onRemoveSources,
}: {
  locale: keyof typeof copy;
  format: SetupPublicationFormat | "";
  removeSources: boolean;
  disabled?: boolean;
  onFormat: (format: SetupPublicationFormat) => void;
  onRemoveSources: (value: boolean) => void;
}) {
  const text = copy[locale];
  return (
    <section className="grid gap-3 border-t border-[var(--line)] pt-3">
      <RadioGroup
        isDisabled={disabled}
        value={format}
        onChange={(value) => {
          if (value === "source" || value === "mka" || value === "m4a")
            onFormat(value);
        }}
      >
        <Label className="block font-semibold">{text.format}</Label>
        <div className="mt-2 flex flex-wrap gap-2">
          {(["source", "mka", "m4a"] as const).map((value) => (
            <RadioField key={value} value={value} className="inline-block">
              <RadioButton className="inline-flex h-9 cursor-pointer items-center rounded-lg border border-[var(--line)] px-3 text-sm text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] data-selected:border-[var(--accent)] data-selected:bg-[var(--accent-soft)] data-selected:font-medium data-selected:text-[var(--accent-strong)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)]">
                {value === "m4a" ? "M4A" : text[value]}
              </RadioButton>
            </RadioField>
          ))}
        </div>
      </RadioGroup>
      <AppCheckbox
        label={text.removeSources}
        description={text.cleanup}
        isSelected={removeSources}
        isDisabled={disabled}
        onChange={onRemoveSources}
      />
    </section>
  );
}
