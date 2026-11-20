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
