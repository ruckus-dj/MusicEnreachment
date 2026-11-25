import {
  Label,
  RadioButton,
  RadioField,
  RadioGroup,
} from "react-aria-components";

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
        <div className="mt-1 flex flex-wrap gap-1">
          {(["source", "mka", "m4a"] as const).map((value) => (
            <RadioField key={value} value={value} className="inline-block">
              <RadioButton className="inline-flex cursor-pointer items-center gap-1 rounded border border-[var(--control)] px-2 py-1 data-selected:border-[var(--accent)] data-selected:bg-[var(--accent-soft)] data-selected:font-semibold data-selected:text-[var(--accent)]">
                {value === "m4a" ? "M4A" : text[value]}
              </RadioButton>
            </RadioField>
          ))}
        </div>
      </RadioGroup>
      <label className="flex items-start gap-2">
        <input
          type="checkbox"
          checked={removeSources}
          disabled={disabled}
          className="mt-1 accent-[var(--accent)]"
          onChange={(event) => onRemoveSources(event.currentTarget.checked)}
        />
        <span>
          <span className="block">{text.removeSources}</span>
          <span className="block text-[var(--muted)]">{text.cleanup}</span>
        </span>
      </label>
    </section>
  );
}
