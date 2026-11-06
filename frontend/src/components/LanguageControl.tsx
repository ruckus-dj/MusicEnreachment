import {
  ListBox,
  ListBoxItem,
  Popover,
  Select,
  SelectValue,
} from "react-aria-components";
import { useI18n } from "../i18n/I18nProvider";
import { AppButton } from "./AppButton";

const optionClassName =
  "react-aria-ListBoxItem cursor-pointer rounded-sm px-2 py-[7px] outline-none data-[focused]:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--accent)] data-[focus-visible]:outline-none data-[focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)]";

export function LanguageControl() {
  const { locale, preference, setPreference, persistenceError } = useI18n();
  const copy =
    locale === "ru"
      ? { language: "Язык интерфейса", system: "Как в системе" }
      : { language: "Interface language", system: "System default" };

  return (
    <div className="flex min-w-0 items-center gap-2">
      <Select
        aria-label={copy.language}
        selectedKey={preference}
        onSelectionChange={(key) => {
          if (key === "system" || key === "en" || key === "ru")
            setPreference(key);
        }}
        className="react-aria-Select min-w-0"
      >
        <AppButton className="!min-h-0 h-[26px] w-[122px] justify-between gap-1.5 text-xs pointer-coarse:min-h-[40px]! max-[599px]:w-[112px]">
          <SelectValue className="react-aria-SelectValue overflow-hidden text-ellipsis whitespace-nowrap" />
          <svg
            aria-hidden="true"
            viewBox="0 0 16 16"
            className="size-3 shrink-0"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.5"
          >
            <path d="m4 6 4 4 4-4" />
          </svg>
        </AppButton>
        <Popover className="react-aria-Popover z-20 min-w-[var(--trigger-width)] overflow-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_8px_24px_#0002]">
          <ListBox className="react-aria-ListBox grid gap-0.5 outline-none">
            <ListBoxItem
              id="system"
              textValue={copy.system}
              className={optionClassName}
            >
              {copy.system}
            </ListBoxItem>
            <ListBoxItem
              id="en"
              textValue="English"
              className={optionClassName}
            >
              <span lang="en">English</span>
            </ListBoxItem>
            <ListBoxItem
              id="ru"
              textValue="Русский"
              className={optionClassName}
            >
              <span lang="ru">Русский</span>
            </ListBoxItem>
          </ListBox>
        </Popover>
      </Select>
      {persistenceError && (
        <span
          role="status"
          className="max-w-[220px] text-[11px] text-[var(--warning)]"
        >
          {persistenceError}
        </span>
      )}
    </div>
  );
}
