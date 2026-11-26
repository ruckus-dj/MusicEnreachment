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
import { AppCheckbox } from "../../components/AppCheckbox";
import { AppSwitch } from "../../components/AppSwitch";
import { ui } from "../../components/uiStyles";
import { SettingsHelpDetails } from "./SettingsHelpDetails";

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
  matchingThresholdHint:
    "Each file → position pair in an automatic layout must reach at least this score. Enter a number from 0 to 1; the default is 0.70.",
  matchingThresholdDetails:
    "Changing the threshold does not re-confirm saved links or start publication.",
  matchingThresholdInvalid: "Enter a number from 0 to 1.",
  matchingThresholdDirty: "Matching threshold has an unsaved change.",
  saveMatchingThreshold: "Save matching threshold",
  calculateSha: "Calculate SHA-256",
  shaHint: "This setting applies to future analyses.",
  shaDetails:
    "Changing it does not delete saved results or start a bulk recalculation.",
  logLevel: "Log level",
  detailsLabel: "Details",
  sourceDeletionTitle: "Source deletion",
  allowSourceDeletion: "Allow source deletion after publication",
  sourceDeletionHint:
    "Off by default. An original file is eligible for removal only after every segment assigned to it has been successfully published, including segments not queued yet.",
  sourceDeletionDetails:
    "Active consumers and retained holds also block removal. Filesystem permission is required; working copies and manual safeguards are unchanged. Changing this setting does not delete existing files or start processing.",
  unsavedSourceDeletion: "The source deletion setting has unsaved changes.",
  albumCompletionTitle: "Automatic publication",
  autoCompleteAlbums: "Automatically publish complete albums",
  albumCompletionHint:
    "Off by default. First publication starts automatically only when every track position in the edition reaches the matching threshold.",
  albumCompletionDetails:
    "Changing this setting does not publish previously matched albums.",
  unsavedAlbumCompletion:
    "The automatic publication setting has unsaved changes.",
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
  matchingThresholdHint:
    "Каждая пара «файл → позиция» в автоматической раскладке должна набрать не меньше этого значения. Введите число от 0 до 1; значение по умолчанию — 0.70.",
  matchingThresholdDetails:
    "Изменение порога не переподтверждает сохранённые связи и не запускает публикацию.",
  matchingThresholdInvalid: "Введите число от 0 до 1.",
  matchingThresholdDirty: "Есть несохранённое изменение порога сопоставления.",
  saveMatchingThreshold: "Сохранить порог сопоставления",
  calculateSha: "Вычислять SHA-256",
  shaHint: "Настройка применяется к последующим анализам.",
  shaDetails:
    "Её изменение не удаляет сохранённые результаты и не запускает массовый пересчёт.",
  logLevel: "Уровень журнала",
  detailsLabel: "Подробнее",
  sourceDeletionTitle: "Удаление исходников",
  allowSourceDeletion: "Разрешить удаление исходников после публикации",
  sourceDeletionHint:
    "Выключено по умолчанию. Исходный файл можно удалить только после успешной публикации всех назначенных ему сегментов, включая ещё не поставленные в очередь.",
  sourceDeletionDetails:
    "Активные потребители и сохранённые блокировки также запрещают удаление. Требуется разрешение файловой системы; рабочие копии и ручная защита не затрагиваются. Изменение настройки не удаляет существующие файлы и не запускает обработку.",
  unsavedSourceDeletion: "Есть несохранённая настройка удаления исходников.",
  albumCompletionTitle: "Автопубликация",
  autoCompleteAlbums: "Автоматически публиковать полные альбомы",
  albumCompletionHint:
    "Выключено по умолчанию. Первая публикация запускается автоматически, только если каждая позиция издания достигает порога сопоставления.",
  albumCompletionDetails: "Включение не публикует уже сопоставленные альбомы.",
  unsavedAlbumCompletion: "Есть несохранённая настройка автопубликации.",
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
    "min-w-0 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5 [&>h2]:mb-4 [&>h2]:text-base [&>h2]:font-semibold [&>h3]:mb-3 [&>h3]:text-sm [&>h3]:font-semibold",
  layout: "grid gap-6 min-[1000px]:grid-cols-2 min-[1000px]:gap-x-8",
  controls: "grid content-start gap-5 min-[600px]:grid-cols-2",
  column: "grid content-start gap-2",
  field: "grid min-w-0 gap-1.5 text-sm font-medium text-[var(--ink)]",
  label: "react-aria-Label ui-label text-sm font-medium text-[var(--ink)]",
  select:
    "react-aria-Select ui-select relative min-w-0 [&>button]:flex [&>button]:h-9 [&>button]:w-full [&>button]:items-center [&>button]:justify-between [&>button]:gap-2 [&>button]:rounded-[var(--control-radius)] [&>button]:border [&>button]:border-[var(--control)] [&>button]:bg-[var(--surface)] [&>button]:px-3 [&>button]:text-left [&>button]:text-sm [&>button]:text-[var(--ink)] [&>button:hover]:border-[var(--control)] [&>button:hover]:bg-[var(--raised)]",
  trigger:
    "react-aria-Button ui-button inline-flex h-9 w-full cursor-pointer items-center justify-between gap-2 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-3 text-left text-sm text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0",
  popover: ui.popover,
  listBox: ui.listBox,
  option: ui.option,
  value:
    "react-aria-SelectValue ui-select-value overflow-hidden text-ellipsis whitespace-nowrap",
  input:
    "react-aria-Input ui-input h-9 min-w-0 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] placeholder:text-[var(--muted)] hover:border-[var(--control)] aria-[invalid=true]:border-[var(--error)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0",
  help: "m-0 text-xs text-[var(--muted)]",
  error: "m-0 text-xs font-medium text-[var(--error)]",
  cleanup:
    "grid content-start gap-3 rounded-lg border border-[var(--line)] p-4",
  cleanupTitle: "m-0 text-sm font-semibold",
  cleanupHelp: "m-0 text-xs text-[var(--muted)]",
  failure:
    "mb-4 grid gap-3 rounded-lg border border-[color-mix(in_srgb,var(--warning)_30%,transparent)] bg-[var(--warning-soft)] p-4",
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

export interface SettingsGeneralAlbumCompletionModel {
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
  albumCompletion: SettingsGeneralAlbumCompletionModel;
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
              {copy.matchingThresholdHint}
            </p>
            <SettingsHelpDetails label={copy.detailsLabel}>
              <p>{copy.matchingThresholdDetails}</p>
            </SettingsHelpDetails>
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
            <AppSwitch
              label={copy.calculateSha}
              description={copy.shaHint}
              isSelected={model.sha256.enabled}
              onChange={model.sha256.onChange}
            />
            <SettingsHelpDetails label={copy.detailsLabel}>
              <p>{copy.shaDetails}</p>
            </SettingsHelpDetails>
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
        <div className="grid content-start gap-3">
          <section
            aria-labelledby="source-deletion-title"
            className={sectionStyles.cleanup}
          >
            <h3
              id="source-deletion-title"
              className={sectionStyles.cleanupTitle}
            >
              {copy.sourceDeletionTitle}
            </h3>
            <AppCheckbox
              label={copy.allowSourceDeletion}
              describedBy="source-deletion-help"
              isSelected={model.cleanup.enabled}
              onChange={model.cleanup.onChange}
            />
            <p id="source-deletion-help" className={sectionStyles.help}>
              {copy.sourceDeletionHint}
            </p>
            <SettingsHelpDetails label={copy.detailsLabel}>
              <p>{copy.sourceDeletionDetails}</p>
            </SettingsHelpDetails>
            {model.cleanup.dirty && (
              <p className="m-0">{copy.unsavedSourceDeletion}</p>
            )}
          </section>
          <section
            aria-labelledby="album-completion-title"
            className={sectionStyles.cleanup}
          >
            <h3
              id="album-completion-title"
              className={sectionStyles.cleanupTitle}
            >
              {copy.albumCompletionTitle}
            </h3>
            <AppSwitch
              label={copy.autoCompleteAlbums}
              describedBy="album-completion-help"
              isSelected={model.albumCompletion.enabled}
              onChange={model.albumCompletion.onChange}
            />
            <p id="album-completion-help" className={sectionStyles.help}>
              {copy.albumCompletionHint}
            </p>
            <SettingsHelpDetails label={copy.detailsLabel}>
              <p>{copy.albumCompletionDetails}</p>
            </SettingsHelpDetails>
            {model.albumCompletion.dirty && (
              <p className="m-0">{copy.unsavedAlbumCompletion}</p>
            )}
          </section>
        </div>
      </div>
    </section>
  );
}
