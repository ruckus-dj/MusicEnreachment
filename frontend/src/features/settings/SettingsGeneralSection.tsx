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
import type {
  UpdateLogLevelBodyLevel,
  UpdateSettingsBodyPublicationFormat,
} from "../../api/generated/client.schemas";
import { AppButton } from "../../components/AppButton";

const sectionStyles = {
  panel:
    "min-w-0 mb-[10px] rounded-[8px] border border-[var(--line)] bg-[var(--surface)] p-[10px] [&>h2]:mb-2 [&>h2]:text-sm [&>h2]:font-semibold [&>h3]:mb-2 [&>h3]:text-sm [&>h3]:font-semibold [&>p]:my-2",
  controls: "grid content-start gap-3 min-[600px]:grid-cols-2",
  field:
    "grid min-w-0 gap-[5px] text-xs font-semibold text-[var(--ink)] [&_.react-aria-TextField]:min-w-0 [&_.react-aria-Input]:min-h-[38px] [&_.react-aria-Input]:w-full [&_.react-aria-Input]:min-w-0 [&_.react-aria-Input]:rounded-[5px] [&_.react-aria-Input]:border [&_.react-aria-Input]:border-[var(--control)] [&_.react-aria-Input]:bg-[var(--surface)] [&_.react-aria-Input]:px-[10px] [&_.react-aria-Input]:py-2 [&_.react-aria-Input]:text-[var(--ink)]",
  select:
    "react-aria-Select relative min-w-0 [&>button]:flex [&>button]:min-h-[30px] [&>button]:w-full [&>button]:items-center [&>button]:justify-between [&>button]:gap-1.5 [&>button]:rounded-[var(--control-radius)] [&>button]:border [&>button]:border-[var(--control)] [&>button]:bg-[var(--surface)] [&>button]:px-[9px] [&>button]:py-[5px] [&>button]:text-left [&>button]:text-[var(--ink)] [&>button]:hover:border-[var(--accent)] [&>button]:hover:bg-[var(--raised)]",
  label:
    "react-aria-Label mb-1 block text-[11px] font-semibold text-[var(--muted)]",
  trigger:
    "react-aria-Button inline-flex min-h-[30px] cursor-pointer items-center justify-center gap-1.5 rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-[5px] text-[var(--ink)] hover:border-[var(--accent)] hover:bg-[var(--raised)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]",
  popover:
    "react-aria-Popover z-20 max-h-[280px] min-w-[var(--trigger-width)] overflow-auto rounded-[var(--radius)] border border-[var(--line)] bg-[var(--surface)] p-1 text-[var(--ink)] shadow-[0_8px_24px_#0002]",
  listBox: "react-aria-ListBox grid gap-0.5 outline-none",
  option:
    "react-aria-ListBoxItem cursor-pointer rounded-sm px-2 py-[7px] outline-none data-[focused]:bg-[var(--raised)] data-[selected]:bg-[var(--accent-soft)] data-[selected]:text-[var(--accent)] data-[focus-visible]:outline-none data-[focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)]",
  value:
    "react-aria-SelectValue overflow-hidden text-ellipsis whitespace-nowrap",
  input:
    "react-aria-Input min-h-8 min-w-0 w-full rounded-[var(--control-radius)] border border-[var(--control)] bg-[var(--surface)] px-2 py-[5px] text-[var(--ink)] aria-[invalid=true]:border-[var(--error)] focus-visible:outline-2 focus-visible:outline-[var(--focus)]",
};

type Props = {
  publicationFormat: UpdateSettingsBodyPublicationFormat;
  onPublicationFormatChange: (
    value: UpdateSettingsBodyPublicationFormat,
  ) => void;
  sourceFileConcurrency: string;
  concurrencyInvalid: boolean;
  concurrencySavingDisabled: boolean;
  concurrencyDirty: boolean;
  onConcurrencyChange: (value: string) => void;
  onSaveConcurrency: () => void;
  sha256Enabled: boolean;
  onSha256Change: (enabled: boolean) => void;
  logLevel: UpdateLogLevelBodyLevel;
  onLogLevelChange: (value: UpdateLogLevelBodyLevel) => void;
};

export function SettingsGeneralSection({
  publicationFormat,
  onPublicationFormatChange,
  sourceFileConcurrency,
  concurrencyInvalid,
  concurrencySavingDisabled,
  concurrencyDirty,
  onConcurrencyChange,
  onSaveConcurrency,
  sha256Enabled,
  onSha256Change,
  logLevel,
  onLogLevelChange,
}: Props) {
  return (
    <section
      aria-labelledby="settings-general-title"
      className={`settings-general-section ${sectionStyles.panel}`}
    >
      <h2 id="settings-general-title">Общие</h2>
      <div className={sectionStyles.controls}>
        <div className="grid content-start gap-2">
          <Select
            selectedKey={publicationFormat}
            onSelectionChange={(key) => {
              if (key)
                onPublicationFormatChange(
                  String(key) as UpdateSettingsBodyPublicationFormat,
                );
            }}
            className={sectionStyles.select}
          >
            <Label className={sectionStyles.label}>Publication format</Label>
            <Button
              aria-label="Publication format"
              className={sectionStyles.trigger}
            >
              <SelectValue className={sectionStyles.value} />
            </Button>
            <Popover className={sectionStyles.popover}>
              <ListBox className={sectionStyles.listBox}>
                <ListBoxItem id="source" className={sectionStyles.option}>
                  Исходный формат
                </ListBoxItem>
                <ListBoxItem id="mka" className={sectionStyles.option}>
                  MKA remux
                </ListBoxItem>
                <ListBoxItem id="m4a" className={sectionStyles.option}>
                  M4A
                </ListBoxItem>
              </ListBox>
            </Popover>
          </Select>
          {publicationFormat === "m4a" && (
            <p className="m-0 text-xs text-[var(--muted)]" role="note">
              M4A сохраняет AAC и ALAC без перекодирования; другие lossless
              кодируются в ALAC, другие lossy — в AAC с потерями.
            </p>
          )}
        </div>
        <div className="grid content-start gap-2">
          <TextField
            value={sourceFileConcurrency}
            onChange={onConcurrencyChange}
            className={sectionStyles.field}
          >
            <Label className={sectionStyles.label}>Одновременные файлы</Label>
            <Input
              id="settings-concurrency"
              type="number"
              min="1"
              inputMode="numeric"
              aria-invalid={concurrencyInvalid}
              aria-describedby={
                concurrencyInvalid
                  ? "source-file-concurrency-help settings-concurrency-error"
                  : "source-file-concurrency-help"
              }
              className={sectionStyles.input}
            />
          </TextField>
          <p
            id="source-file-concurrency-help"
            className="m-0 text-xs text-[var(--muted)]"
          >
            Положительное целое число файлов, обрабатываемых одновременно.
            Значение должно точно представляться в браузере.
          </p>
          {concurrencyInvalid && (
            <p
              id="settings-concurrency-error"
              className="m-0 text-[var(--error)]"
              role="alert"
            >
              Введите положительное целое число, точно представимое в браузере.
            </p>
          )}
          {concurrencyDirty && (
            <p className="m-0">Есть несохранённое изменение параллельности.</p>
          )}
          <AppButton
            className="justify-self-start"
            isDisabled={
              concurrencySavingDisabled ||
              concurrencyInvalid ||
              !sourceFileConcurrency
            }
            onPress={onSaveConcurrency}
          >
            Сохранить параллельность
          </AppButton>
        </div>
        <div className="grid content-start gap-2">
          <label className="flex items-center gap-2">
            <input
              type="checkbox"
              aria-describedby="sha256-help"
              checked={sha256Enabled}
              onChange={(event) => onSha256Change(event.target.checked)}
            />
            Вычислять SHA-256
          </label>
          <p id="sha256-help" className="m-0 text-xs text-[var(--muted)]">
            Настройка применяется к последующим анализам. Её изменение не
            удаляет сохранённые результаты и не запускает массовый пересчёт.
          </p>
        </div>
        <Select
          selectedKey={logLevel}
          onSelectionChange={(key) => {
            if (key) onLogLevelChange(String(key) as UpdateLogLevelBodyLevel);
          }}
          className={sectionStyles.select}
        >
          <Label className={sectionStyles.label}>Log level</Label>
          <Button aria-label="Log level" className={sectionStyles.trigger}>
            <SelectValue className={sectionStyles.value} />
          </Button>
          <Popover className={sectionStyles.popover}>
            <ListBox className={sectionStyles.listBox}>
              {(["debug", "info", "warn", "error"] as const).map((level) => (
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
    </section>
  );
}
