import { browseServerDirectories } from "../api/generated/client";
import { useI18n } from "../i18n/I18nProvider";
import {
  type BrowseDirectory,
  DirectoryPickerView,
} from "./DirectoryPickerView";

const browse: BrowseDirectory = async (path, offset, signal) => {
  const response = await browseServerDirectories(
    { path: path || undefined, offset },
    { signal },
  );
  if (response.status !== 200) throw new Error("Directory browse failed");
  const page = response.data;
  return {
    currentPath: page.current_path,
    parentPath: page.parent_path,
    directories: page.directories ?? [],
    roots: page.roots ?? [],
    offset: page.offset,
    nextOffset: page.next_offset,
  };
};

export function DirectoryPicker(props: {
  open: boolean;
  initialPath: string;
  onSelect: (path: string) => void;
  onClose: () => void;
}) {
  const { locale } = useI18n();
  return <DirectoryPickerView {...props} browse={browse} locale={locale} />;
}
