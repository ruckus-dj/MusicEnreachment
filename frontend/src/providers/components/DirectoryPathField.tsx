import { useI18n } from "../../app/i18n/host";
import {
  type DirectoryPathFieldProps,
  DirectoryPathFieldView,
} from "../../ui/components/DirectoryPathFieldView";
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
