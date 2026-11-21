import { DirectoryPathFieldView } from "../../components/DirectoryPathFieldView";
import type { BrowseDirectory } from "../../components/DirectoryPickerView";

type Locale = "en" | "ru";

export type SetupDirectoryFieldKind = "tools" | "output";

export type SetupDirectoryField = {
  kind: SetupDirectoryFieldKind;
  value: string;
  onSelect: (path: string) => void;
};

const labels: Record<Locale, Record<SetupDirectoryFieldKind, string>> = {
  en: {
    tools: "Tools directory",
    output: "Output directory",
  },
  ru: {
    tools: "Каталог инструментов",
    output: "Каталог публикации",
  },
};

const ids: Record<SetupDirectoryFieldKind, string> = {
  tools: "setup-tools-directory",
  output: "setup-output-directory",
};

export type SetupDirectoryFieldsViewProps = {
  fields: readonly SetupDirectoryField[];
  locale: Locale;
  browse: BrowseDirectory;
  disabled?: boolean;
  description?: string;
  "aria-describedby"?: string;
  resolveInitialPath?: (value: string) => string;
};

export function SetupDirectoryFieldsView({
  fields,
  locale,
  browse,
  disabled,
  description,
  "aria-describedby": ariaDescribedBy,
  resolveInitialPath,
}: SetupDirectoryFieldsViewProps) {
  return (
    <div className="grid gap-3">
      {fields.map((field) => (
        <DirectoryPathFieldView
          key={field.kind}
          id={ids[field.kind]}
          label={labels[locale][field.kind]}
          value={field.value}
          onSelect={field.onSelect}
          disabled={disabled}
          description={description}
          aria-describedby={ariaDescribedBy}
          browse={browse}
          locale={locale}
          resolveInitialPath={resolveInitialPath}
        />
      ))}
    </div>
  );
}

const bodyCopy = {
  en: {
    server:
      "These paths belong to the server, not this computer. The output directory must be empty.",
    tools: "Choose an absolute server directory. It is checked when selected.",
    output: "Choose the directory for new publications.",
    publication: "Publication",
    selected: "Directory selected",
  },
  ru: {
    server:
      "Пути относятся к серверу, а не к этому компьютеру. Каталог публикации должен быть пустым.",
    tools: "Выберите абсолютный каталог на сервере. Он проверяется при выборе.",
    output: "Выберите каталог для новых публикаций.",
    publication: "Публикация",
    selected: "Каталог выбран",
  },
} as const;

export function SetupDirectoryStepBody({
  kind,
  selected,
  ...fields
}: Omit<SetupDirectoryFieldsViewProps, "description" | "aria-describedby"> & {
  kind: "server" | "tools" | "output";
  selected?: boolean;
}) {
  const text = bodyCopy[fields.locale];
  return (
    <section className="grid min-w-0 gap-3">
      {kind === "output" && (
        <>
          <h3 className="m-0 font-semibold">{text.publication}</h3>
          <p className="m-0">{text.output}</p>
        </>
      )}
      <SetupDirectoryFieldsView
        {...fields}
        description={kind === "tools" ? text.tools : undefined}
        aria-describedby={
          kind === "server" ? "setup-directory-help" : undefined
        }
      />
      {kind === "server" && <p id="setup-directory-help">{text.server}</p>}
      {kind === "tools" && selected && <p role="status">{text.selected}</p>}
    </section>
  );
}
