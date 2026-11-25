import { AppButton } from "../../components/AppButton";
import { DirectoryPathFieldView } from "../../components/DirectoryPathFieldView";
import type { BrowseDirectory } from "../../components/DirectoryPickerView";

const copyByLocale = {
  en: {
    publication: "Publication",
    outputDirectory: "Output directory",
    unsavedPublication: "Publication changes are unsaved.",
    publicationTemplate: "Publication path template",
    templateHelp:
      "Tokens in braces are filled from metadata. Add a fallback after | when a value may be missing. Use / to separate folders under the output directory.",
    unsavedTemplate: "The publication path template has unsaved changes.",
    saveTemplate: "Save path template",
    changeOutput: "Change output directory",
    sourceProcessing: "Source processing",
    workDirectory: "Work directory for staged copies",
    workDirectoryHelp:
      "This setting is not required for Setup or source-root registration, but is required to process staged sources. It cannot be changed while operations or retained copies exist.",
    unsavedWorkDirectory: "The work directory has unsaved changes.",
    saveWorkDirectory: "Save work directory",
  },
  ru: {
    publication: "Публикация",
    outputDirectory: "Каталог публикации",
    unsavedPublication: "Есть несохранённые изменения публикации.",
    publicationTemplate: "Шаблон пути публикации",
    templateHelp:
      "Токены в фигурных скобках подставляются из метаданных; для отсутствующего значения задайте альтернативу через |. Разделяйте вложенные папки каталога публикации знаком /.",
    unsavedTemplate: "Есть несохранённый шаблон пути публикации.",
    saveTemplate: "Сохранить шаблон пути",
    changeOutput: "Сменить каталог публикации",
    sourceProcessing: "Обработка источников",
    workDirectory: "Рабочая папка для подготовленных копий",
    workDirectoryHelp:
      "Настройка не требуется для первичной настройки или регистрации корня источников, но обязательна для обработки подготовленных источников. Каталог нельзя изменить, пока существуют операции или сохранённые копии.",
    unsavedWorkDirectory: "Есть несохранённые изменения рабочей папки.",
    saveWorkDirectory: "Сохранить рабочую папку",
  },
} as const;

const panel =
  "grid min-w-0 content-start gap-2 rounded-md border border-[var(--line)] bg-[var(--surface)] p-3 text-sm";
const heading = "m-0 text-sm font-semibold";
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
        <label className="grid gap-1 font-semibold">
          {copy.publicationTemplate}
          <input
            className="min-h-9 min-w-0 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-1 font-normal focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-[var(--focus)]"
            aria-describedby="publication-template-help"
            maxLength={4096}
            value={template.value}
            onChange={(event) => template.onChange(event.target.value)}
          />
        </label>
        <p id="publication-template-help" className={note}>
          {copy.templateHelp}
        </p>
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
        <p className={note}>{copy.workDirectoryHelp}</p>
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
