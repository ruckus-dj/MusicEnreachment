import {
  ListBox,
  ListBoxItem,
  Popover,
  Select,
  SelectValue,
} from "react-aria-components";
import { useI18n } from "../i18n/I18nProvider";
import { AppButton } from "./AppButton";
import { AppIcon } from "./AppIcon";
import { ui } from "./uiStyles";

const messages = {
  en: {
    language: "Interface language",
    system: "System default",
    read: "Language preference is active but could not be read from browser storage.",
    write:
      "Language preference is active but could not be saved in browser storage.",
  },
  ru: {
    language: "Язык интерфейса",
    system: "Как в системе",
    read: "Настройка языка действует, но не удалось прочитать её из хранилища браузера.",
    write:
      "Настройка языка действует, но не удалось сохранить её в хранилище браузера.",
  },
} as const;

export function LanguageControl() {
  const { locale, preference, setPreference, persistenceIssue } = useI18n();
  const copy = messages[locale];

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
        <AppButton
          variant="secondary"
          className="w-auto min-w-[136px] justify-between gap-2 max-[599px]:min-w-0"
        >
          <AppIcon name="globe" className="size-4 text-[var(--muted)]" />
          <SelectValue className={`${ui.selectValue} flex-1`} />
          <AppIcon
            name="chevronDown"
            className="size-3.5 shrink-0 text-[var(--muted)]"
          />
        </AppButton>
        <Popover className={ui.popover}>
          <ListBox className={ui.listBox}>
            <ListBoxItem
              id="system"
              textValue={copy.system}
              className={ui.option}
            >
              {copy.system}
            </ListBoxItem>
            <ListBoxItem id="en" textValue="English" className={ui.option}>
              <span lang="en">English</span>
            </ListBoxItem>
            <ListBoxItem id="ru" textValue="Русский" className={ui.option}>
              <span lang="ru">Русский</span>
            </ListBoxItem>
          </ListBox>
        </Popover>
      </Select>
      {persistenceIssue && (
        <span
          role="status"
          className="max-w-[220px] text-xs text-[var(--warning)]"
        >
          {copy[persistenceIssue]}
        </span>
      )}
    </div>
  );
}
