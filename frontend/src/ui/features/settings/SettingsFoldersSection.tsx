import { Input, Label, TextField } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { DirectoryPathFieldView } from "../../components/DirectoryPathFieldView";
import type { BrowseDirectory } from "../../components/DirectoryPickerView";
import { SettingsHelpDetails } from "./SettingsHelpDetails";

const copyByLocale = {
  en: {
    publication: "Publication",
    outputDirectory: "Output directory",
    unsavedPublication: "Publication changes are unsaved.",
    publicationTemplate: "Publication path template",
    templateHint:
      "Tokens in braces are filled from metadata. Use / to separate folders under the output directory.",
    templateDetails: "Add a fallback after | when a value may be missing.",
    unsavedTemplate: "The publication path template has unsaved changes.",
    saveTemplate: "Save path template",
    changeOutput: "Change output directory",
    sourceProcessing: "Source processing",
    workDirectory: "Work directory for staged copies",
    workDirectoryHint:
      "Required to process staged sources. It cannot be changed while operations or retained copies exist.",
    workDirectoryDetails: "Not required for Setup or source-root registration.",
    unsavedWorkDirectory: "The work directory has unsaved changes.",
    saveWorkDirectory: "Save work directory",
    detailsLabel: "Details",
  },
  ru: {
    publication: "Публикация",
    outputDirectory: "Каталог публикации",
    unsavedPublication: "Есть несохранённые изменения публикации.",
    publicationTemplate: "Шаблон пути публикации",
    templateHint:
      "Токены в фигурных скобках подставляются из метаданных; разделяйте вложенные папки каталога публикации знаком /.",
    templateDetails:
      "Для отсутствующего значения задайте альтернативу через |.",
    unsavedTemplate: "Есть несохранённый шаблон пути публикации.",
    saveTemplate: "Сохранить шаблон пути",
    changeOutput: "Сменить каталог публикации",
    sourceProcessing: "Обработка источников",
    workDirectory: "Рабочая папка для подготовленных копий",
    workDirectoryHint:
      "Обязательна для обработки подготовленных источников. Каталог нельзя изменить, пока существуют операции или сохранённые копии.",
    workDirectoryDetails:
      "Для первичной настройки и регистрации корня источников не требуется.",
    unsavedWorkDirectory: "Есть несохранённые изменения рабочей папки.",
    saveWorkDirectory: "Сохранить рабочую папку",
    detailsLabel: "Подробнее",
  },
} as const;

const panel =
  "grid min-w-0 content-start gap-3 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5 text-sm";
const heading = "m-0 text-base font-semibold";
const note = "m-0 text-xs text-[var(--muted)]";

type Field = {
  value: string;
  dirty: boolean;
  onChange: (value: string) => void;
};

export function SettingsFoldersSection({
  locale,
  browse,
  resolveInitialPath,
  busy,
  output,
  template,
  work,
}: {
  locale: "en" | "ru";
  browse: BrowseDirectory;
  resolveInitialPath?: (path: string) => string;
  busy: boolean;
  output: Field & {
    matchesSaved?: boolean;
    onReset: (trigger: Element) => void;
  };
  template: Field & { onSave: () => void };
  work: Field & { onSave: () => void; locked?: boolean };
}) {
  const copy = copyByLocale[locale];
  const directory = { locale, browse, resolveInitialPath };
  return (
    <div className="grid grid-cols-[repeat(auto-fit,minmax(min(100%,300px),1fr))] gap-3">
      <section aria-labelledby="runtime-title" className={panel}>
        <h2 id="runtime-title" className={heading}>
          {copy.publication}
        </h2>
        <DirectoryPathFieldView
          {...directory}
          id="settings-output-directory"
          label={copy.outputDirectory}
          value={output.value}
          onSelect={output.onChange}
          disabled={busy}
        />
        {output.dirty && <p className={note}>{copy.unsavedPublication}</p>}
        <TextField
          value={template.value}
          onChange={template.onChange}
          className="grid min-w-0 gap-1.5"
        >
          <Label className="text-sm font-medium text-[var(--ink)]">
            {copy.publicationTemplate}
          </Label>
          <Input
            aria-describedby="publication-template-help"
            maxLength={4096}
            className="h-9 min-w-0 rounded-lg border border-[var(--control)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] hover:border-[var(--control)] focus-visible:border-[var(--accent)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0"
          />
        </TextField>
        <p id="publication-template-help" className={note}>
          {copy.templateHint}
        </p>
        <SettingsHelpDetails label={copy.detailsLabel}>
          <p>{copy.templateDetails}</p>
        </SettingsHelpDetails>
        {template.dirty && <p className={note}>{copy.unsavedTemplate}</p>}
        <div className="flex flex-wrap gap-2">
          <AppButton
            isDisabled={busy || !template.value}
            onPress={template.onSave}
          >
            {copy.saveTemplate}
          </AppButton>
          <AppButton
            isDisabled={busy || (output.matchesSaved ?? !output.dirty)}
            onPress={(event) => output.onReset(event.target)}
          >
            {copy.changeOutput}
          </AppButton>
        </div>
      </section>
      <section aria-labelledby="work-directory-title" className={panel}>
        <h2 id="work-directory-title" className={heading}>
          {copy.sourceProcessing}
        </h2>
        <DirectoryPathFieldView
          {...directory}
          id="settings-work-directory"
          label={copy.workDirectory}
          value={work.value}
          onSelect={work.onChange}
          disabled={busy || work.locked}
        />
        <p className={note}>{copy.workDirectoryHint}</p>
        <SettingsHelpDetails label={copy.detailsLabel}>
          <p>{copy.workDirectoryDetails}</p>
        </SettingsHelpDetails>
        {work.dirty && <p className={note}>{copy.unsavedWorkDirectory}</p>}
        <AppButton
          className="justify-self-start"
          isDisabled={busy || work.locked || !work.dirty}
          onPress={work.onSave}
        >
          {copy.saveWorkDirectory}
        </AppButton>
      </section>
    </div>
  );
}
