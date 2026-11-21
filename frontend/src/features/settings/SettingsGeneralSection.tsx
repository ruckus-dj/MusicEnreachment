import {
  Button,
  Input,
  Label,
  ListBox,
  ListBoxItem,
  Popover,
  Select,
  SelectValue,
  TextField,
} from "react-aria-components";
import { AppButton } from "../../components/AppButton";

const logLevels = ["debug", "info", "warn", "error"] as const;

const english = {
  title: "General",
  publicationFormat: "Publication format",
  originalFormat: "Original format",
  mkaRemux: "MKA remux",
  m4aLabel: "M4A",
  m4aHelp:
    "M4A keeps AAC and ALAC without transcoding; other lossless formats are encoded to ALAC, and other lossy formats to AAC.",
  concurrentFiles: "Concurrent files",
  concurrencyHelp:
    "A positive whole number of files to process at once. The value must be represented exactly in the browser.",
  concurrencyInvalid:
    "Enter a positive whole number that can be represented exactly in the browser.",
  concurrencyDirty: "Concurrency has an unsaved change.",
  saveConcurrency: "Save concurrency",
  matchingThreshold: "Matching threshold",
  matchingThresholdHelp:
    "Each file → position pair in an automatic layout must reach at least this score. Enter a number from 0 to 1; the default is 0.70. Changing the threshold does not re-confirm saved links or start publication.",
  matchingThresholdInvalid: "Enter a number from 0 to 1.",
  matchingThresholdDirty: "Matching threshold has an unsaved change.",
  saveMatchingThreshold: "Save matching threshold",
  calculateSha: "Calculate SHA-256",
  shaHelp:
    "This setting applies to future analyses. Changing it does not delete saved results or start a bulk recalculation.",
  logLevel: "Log level",
  sourceDeletionTitle: "Source deletion",
  allowSourceDeletion: "Allow source deletion after publication",
  sourceDeletionHelp:
    "Off by default. An original file is eligible for removal only after every segment assigned to it has been successfully published, including segments not queued yet. Active consumers and retained holds also block removal. Filesystem permission is required; working copies and manual safeguards are unchanged. Changing this setting does not delete existing files or start processing.",
  unsavedSourceDeletion: "The source deletion setting has unsaved changes.",
  unsavedChanges: "There are unsaved changes.",
  publicationFormatLabel: "publication format",
  logLevelLabel: "log level",
  enabled: "enabled",
  disabled: "disabled",
  saveFailed: "Could not save {label}: {value}.",
  saveUnconfirmed:
    "The server accepted {label} ({value}), but reading it for confirmation failed.",
  retrySave: "Retry save: {label} ({value})",
  retryConfirm: "Retry confirmation: {label} ({value})",
} as const;

type SettingsGeneralCopy = Record<keyof typeof english, string>;

const russian: SettingsGeneralCopy = {
  title: "Общие",
  publicationFormat: "Формат публикации",
  originalFormat: "Исходный формат",
  mkaRemux: "Ремакс в MKA",
  m4aLabel: "M4A",
  m4aHelp:
    "M4A сохраняет AAC и ALAC без перекодирования; другие lossless кодируются в ALAC, другие lossy — в AAC с потерями.",
  concurrentFiles: "Одновременные файлы",
  concurrencyHelp:
    "Положительное целое число файлов, обрабатываемых одновременно. Значение должно точно представляться в браузере.",
  concurrencyInvalid:
    "Введите положительное целое число, точно представимое в браузере.",
  concurrencyDirty: "Есть несохранённое изменение параллельности.",
  saveConcurrency: "Сохранить параллельность",
  matchingThreshold: "Порог сопоставления",
  matchingThresholdHelp:
    "Каждая пара «файл → позиция» в автоматической раскладке должна набрать не меньше этого значения. Введите число от 0 до 1; значение по умолчанию — 0.70. Изменение порога не переподтверждает сохранённые связи и не запускает публикацию.",
  matchingThresholdInvalid: "Введите число от 0 до 1.",
  matchingThresholdDirty: "Есть несохранённое изменение порога сопоставления.",
  saveMatchingThreshold: "Сохранить порог сопоставления",
  calculateSha: "Вычислять SHA-256",
  shaHelp:
    "Настройка применяется к последующим анализам. Её изменение не удаляет сохранённые результаты и не запускает массовый пересчёт.",
  logLevel: "Уровень журнала",
  sourceDeletionTitle: "Удаление исходников",
  allowSourceDeletion: "Разрешить удаление исходников после публикации",
  sourceDeletionHelp:
    "Выключено по умолчанию. Исходный файл можно удалить только после успешной публикации всех назначенных ему сегментов, включая ещё не поставленные в очередь. Активные потребители и сохранённые блокировки также запрещают удаление. Требуется разрешение файловой системы; рабочие копии и ручная защита не затрагиваются. Изменение настройки не удаляет существующие файлы и не запускает обработку.",
  unsavedSourceDeletion: "Есть несохранённая настройка удаления исходников.",
  unsavedChanges: "Есть несохранённые изменения.",
  publicationFormatLabel: "формат публикации",
  logLevelLabel: "уровень журнала",
  enabled: "включено",
  disabled: "выключено",
  saveFailed: "Не удалось сохранить {label}: {value}.",
  saveUnconfirmed:
    "Значение {label} ({value}) принято сервером, но чтение для подтверждения не удалось.",
  retrySave: "Повторить сохранение: {label} ({value})",
  retryConfirm: "Повторить подтверждение: {label} ({value})",
};

const messages: Record<SettingsGeneralLocale, SettingsGeneralCopy> = {
  en: english,
  ru: russian,
};

const formatLabelKeys: Record<
  SettingsGeneralFormat,
  keyof SettingsGeneralCopy
> = {
  source: "originalFormat",
  mka: "mkaRemux",
  m4a: "m4aLabel",
};

function formatMessage(template: string, values: Record<string, string>) {
  return template.replace(
    /\{([^}]+)\}/g,
    (match, name: string) => values[name] ?? match,
  );
}

function failureValueText(
  copy: SettingsGeneralCopy,
  attempt: SettingsGeneralAttemptedValue,
): string {
  if (attempt.setting === "publicationFormat")
    return copy[formatLabelKeys[attempt.value]];
  if (attempt.setting === "sha256")
    return attempt.value ? copy.enabled : copy.disabled;
  return attempt.value;
}

function failureLabelText(
  copy: SettingsGeneralCopy,
  setting: SettingsGeneralFailureSetting,
): string {
  if (setting === "publicationFormat") return copy.publicationFormatLabel;
  if (setting === "sha256") return "SHA-256";
  return copy.logLevelLabel;
}

const sectionStyles = {
  panel:
    "min-w-0 mb-[10px] rounded-[8px] border border-[var(--line)] bg-[var(--surface)] p-[10px] [&>h2]:mb-2 [&>h2]:text-sm [&>h2]:font-semibold [&>h3]:mb-2 [&>h3]:text-sm [&>h3]:font-semibold",
  layout: "grid gap-3 min-[1400px]:grid-cols-2 min-[1400px]:gap-x-6",
  controls: "grid content-start gap-3 min-[600px]:grid-cols-2",
  column: "grid content-start gap-2",
  field: "grid min-w-0 gap-[5px] text-xs font-semibold text-[var(--ink)]",
  label:
    "react-aria-Label ui-label mb-1 block text-[11px] font-semibold text-[var(--muted)]",
  select:
    "react-aria-Select ui-select relative min-w-0 [&>button]:flex [&>button]:min-h-[30px] [&>button]:w-full [&>button]:items-center [&>button]:justify-between [&>button]:gap-1.5 [&>button]:rounded-[var(--control-radius)] [&>button]:border [&>button]:border-[var(--control)] [&>button]:bg-[var(--surface)] [&>button]:px-[9px] [&>button]:py-[5px] [&>button]:text-left [&>button]:text-[var(--ink)] [&>button]:hover:border-[var(--accent)] [&>button]:hover:bg-[var(--raised)]",
  trigger:
    "react-aria-Button ui-button inline-flex min-h-[30px] w-full cursor-pointer items-center justify-between gap-1.5 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-[5px] text-left text-[var(--ink)] hover:border-[var(--accent)] hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
  popover:
    "react-aria-Popover ui-popover z-20 max-h-[280px] min-w-[var(--trigger-width)] overflow-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_8px_24px_#0002]",
  listBox: "react-aria-ListBox ui-listbox grid gap-0.5 outline-none",
  option:
    "react-aria-ListBoxItem ui-listbox-item cursor-pointer rounded-sm px-2 py-[7px] outline-none data-[focused]:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--accent)] data-[focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)]",
  value:
    "react-aria-SelectValue ui-select-value overflow-hidden text-ellipsis whitespace-nowrap",
  input:
    "react-aria-Input ui-input min-h-8 min-w-0 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-[5px] text-[var(--ink)] aria-[invalid=true]:border-[var(--error)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2",
  help: "m-0 text-xs text-[var(--muted)]",
  error: "m-0 text-xs text-[var(--error)]",
  cleanup:
    "grid content-start gap-2 border-t border-[var(--line)] pt-3 min-[1400px]:border-t-0 min-[1400px]:pt-0",
  cleanupTitle: "m-0 text-sm font-semibold",
  cleanupHelp: "m-0 text-xs text-[var(--warning)]",
  failure:
    "grid gap-2 rounded border border-[var(--warning)] bg-[var(--warning-soft)] p-3",
};

export type SettingsGeneralLocale = "en" | "ru";

export type SettingsGeneralFormat = "source" | "mka" | "m4a";

export type SettingsGeneralFailureSetting =
  | "publicationFormat"
  | "sha256"
  | "logLevel";

export type SettingsGeneralFailurePhase = "write" | "confirm";

export type SettingsGeneralAttemptedValue =
  | { setting: "publicationFormat"; value: SettingsGeneralFormat }
  | { setting: "sha256"; value: boolean }
  | { setting: "logLevel"; value: string };

export interface SettingsGeneralFailureModel {
  phase: SettingsGeneralFailurePhase;
  attempt: SettingsGeneralAttemptedValue;
  detail?: string;
  disabled?: boolean;
  onAction: () => void;
}

export interface SettingsGeneralFormatModel {
  value: SettingsGeneralFormat;
  choices: readonly SettingsGeneralFormat[];
  onChange: (value: SettingsGeneralFormat) => void;
}

export interface SettingsGeneralConcurrencyModel {
  value: string;
  invalid: boolean;
  dirty: boolean;
  saveDisabled: boolean;
  onChange: (value: string) => void;
  onSave: () => void;
}

export interface SettingsGeneralThresholdModel {
  value: string;
  invalid: boolean;
  dirty: boolean;
  saveDisabled: boolean;
  onChange: (value: string) => void;
  onSave: () => void;
}

export interface SettingsGeneralSha256Model {
  enabled: boolean;
  onChange: (enabled: boolean) => void;
}

export interface SettingsGeneralLogLevelModel {
  value: string;
  onChange: (value: string) => void;
}

export interface SettingsGeneralCleanupModel {
  enabled: boolean;
  dirty: boolean;
  onChange: (enabled: boolean) => void;
}

export interface SettingsGeneralModel {
  locale: SettingsGeneralLocale;
  format: SettingsGeneralFormatModel;
  concurrency: SettingsGeneralConcurrencyModel;
  matchingThreshold: SettingsGeneralThresholdModel;
  sha256: SettingsGeneralSha256Model;
  logLevel: SettingsGeneralLogLevelModel;
  cleanup: SettingsGeneralCleanupModel;
  failures?: readonly SettingsGeneralFailureModel[];
}

export function SettingsGeneralSection({
  model,
}: {
  model: SettingsGeneralModel;
}) {
  const copy = messages[model.locale];
  return (
    <section
      aria-labelledby="settings-general-title"
      className={sectionStyles.panel}
    >
      <h2 id="settings-general-title">{copy.title}</h2>
      {(model.failures ?? []).map((failure) => {
        const label = failureLabelText(copy, failure.attempt.setting);
        const value = failureValueText(copy, failure.attempt);
        return (
          <fieldset
            key={failure.attempt.setting}
            className={sectionStyles.failure}
            aria-label={copy.unsavedChanges}
          >
            <p className="m-0" role="status">
              {formatMessage(
                failure.phase === "write"
                  ? copy.saveFailed
                  : copy.saveUnconfirmed,
                { label, value },
              )}
            </p>
            {failure.detail && (
              <p className={`${sectionStyles.help} [overflow-wrap:anywhere]`}>
                {failure.detail}
              </p>
            )}
            <AppButton isDisabled={failure.disabled} onPress={failure.onAction}>
              {formatMessage(
                failure.phase === "write" ? copy.retrySave : copy.retryConfirm,
                { label, value },
              )}
            </AppButton>
          </fieldset>
        );
      })}
      <div className={sectionStyles.layout}>
        <div className={sectionStyles.controls}>
          <div className={sectionStyles.column}>
            <Select
              selectedKey={model.format.value}
              onSelectionChange={(key) => {
                if (key !== null)
                  model.format.onChange(key as SettingsGeneralFormat);
              }}
              className={sectionStyles.select}
            >
              <Label className={sectionStyles.label}>
                {copy.publicationFormat}
              </Label>
              <Button
                aria-label={copy.publicationFormat}
                className={sectionStyles.trigger}
              >
                <SelectValue className={sectionStyles.value} />
              </Button>
              <Popover className={sectionStyles.popover}>
                <ListBox className={sectionStyles.listBox}>
                  {model.format.choices.map((choice) => (
                    <ListBoxItem
                      key={choice}
                      id={choice}
                      className={sectionStyles.option}
                    >
                      {copy[formatLabelKeys[choice]]}
                    </ListBoxItem>
                  ))}
                </ListBox>
              </Popover>
            </Select>
            {model.format.value === "m4a" && (
              <p className={sectionStyles.help} role="note">
                {copy.m4aHelp}
              </p>
            )}
          </div>
          <div className={sectionStyles.column}>
            <TextField
              value={model.concurrency.value}
              onChange={model.concurrency.onChange}
              className={sectionStyles.field}
            >
              <Label className={sectionStyles.label}>
                {copy.concurrentFiles}
              </Label>
              <Input
                id="settings-concurrency"
                type="text"
                inputMode="numeric"
                aria-invalid={model.concurrency.invalid}
                aria-describedby={
                  model.concurrency.invalid
                    ? "source-file-concurrency-help settings-concurrency-error"
                    : "source-file-concurrency-help"
                }
                className={sectionStyles.input}
              />
            </TextField>
            <p id="source-file-concurrency-help" className={sectionStyles.help}>
              {copy.concurrencyHelp}
            </p>
            {model.concurrency.invalid && (
              <p
                id="settings-concurrency-error"
                className={sectionStyles.error}
                role="alert"
              >
                {copy.concurrencyInvalid}
              </p>
            )}
            {model.concurrency.dirty && (
              <p className="m-0">{copy.concurrencyDirty}</p>
            )}
            <AppButton
              className="justify-self-start"
              isDisabled={model.concurrency.saveDisabled}
              onPress={model.concurrency.onSave}
            >
              {copy.saveConcurrency}
            </AppButton>
          </div>
          <div className={sectionStyles.column}>
            <TextField
              value={model.matchingThreshold.value}
              onChange={model.matchingThreshold.onChange}
              className={sectionStyles.field}
            >
              <Label className={sectionStyles.label}>
                {copy.matchingThreshold}
              </Label>
              <Input
                id="settings-matching-threshold"
                type="text"
                inputMode="decimal"
                aria-invalid={model.matchingThreshold.invalid}
                aria-describedby={
                  model.matchingThreshold.invalid
                    ? "settings-matching-threshold-help settings-matching-threshold-error"
                    : "settings-matching-threshold-help"
                }
                className={sectionStyles.input}
              />
            </TextField>
            <p
              id="settings-matching-threshold-help"
              className={sectionStyles.help}
            >
              {copy.matchingThresholdHelp}
            </p>
            {model.matchingThreshold.invalid && (
              <p
                id="settings-matching-threshold-error"
                className={sectionStyles.error}
                role="alert"
              >
                {copy.matchingThresholdInvalid}
              </p>
            )}
            {model.matchingThreshold.dirty && (
              <p className="m-0">{copy.matchingThresholdDirty}</p>
            )}
            <AppButton
              className="justify-self-start"
              isDisabled={model.matchingThreshold.saveDisabled}
              onPress={model.matchingThreshold.onSave}
            >
              {copy.saveMatchingThreshold}
            </AppButton>
          </div>
          <div className={sectionStyles.column}>
            <label className="flex items-center gap-2">
              <input
                type="checkbox"
                aria-describedby="sha256-help"
                checked={model.sha256.enabled}
                onChange={(event) =>
                  model.sha256.onChange(event.target.checked)
                }
              />
              {copy.calculateSha}
            </label>
            <p id="sha256-help" className={sectionStyles.help}>
              {copy.shaHelp}
            </p>
          </div>
          <Select
            selectedKey={model.logLevel.value}
            onSelectionChange={(key) => {
              if (key !== null) model.logLevel.onChange(String(key));
            }}
            className={sectionStyles.select}
          >
            <Label className={sectionStyles.label}>{copy.logLevel}</Label>
            <Button
              aria-label={copy.logLevel}
              className={sectionStyles.trigger}
            >
              <SelectValue className={sectionStyles.value} />
            </Button>
            <Popover className={sectionStyles.popover}>
              <ListBox className={sectionStyles.listBox}>
                {logLevels.map((level) => (
                  <ListBoxItem
                    id={level}
                    key={level}
                    className={sectionStyles.option}
                  >
                    {level}
                  </ListBoxItem>
                ))}
              </ListBox>
            </Popover>
          </Select>
        </div>
        <section
          aria-labelledby="source-deletion-title"
          className={sectionStyles.cleanup}
        >
          <h3 id="source-deletion-title" className={sectionStyles.cleanupTitle}>
            {copy.sourceDeletionTitle}
          </h3>
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              aria-describedby="source-cleanup-help"
              checked={model.cleanup.enabled}
              onChange={(event) => model.cleanup.onChange(event.target.checked)}
            />
            {copy.allowSourceDeletion}
          </label>
          <p
            id="source-cleanup-help"
            className={sectionStyles.cleanupHelp}
            role="note"
          >
            {copy.sourceDeletionHelp}
          </p>
          {model.cleanup.dirty && (
            <p className="m-0">{copy.unsavedSourceDeletion}</p>
          )}
        </section>
      </div>
    </section>
  );
}
