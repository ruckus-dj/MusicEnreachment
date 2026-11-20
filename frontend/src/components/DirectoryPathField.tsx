import { useI18n } from "../i18n/I18nProvider";
import {
  type DirectoryPathFieldProps,
  DirectoryPathFieldView,
} from "./DirectoryPathFieldView";
import { browseServerDirectory } from "./DirectoryPicker";

export function DirectoryPathField(props: DirectoryPathFieldProps) {
  const { locale } = useI18n();
  return (
    <DirectoryPathFieldView
      {...props}
      locale={locale}
      browse={browseServerDirectory}
    />
  );
}
