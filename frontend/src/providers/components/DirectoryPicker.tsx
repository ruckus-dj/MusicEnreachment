import { browseServerDirectories } from "../../api/generated/client";
import type { BrowseDirectory } from "../../ui/components/DirectoryPickerView";

export const browseServerDirectory: BrowseDirectory = async (
  path,
  offset,
  signal,
) => {
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
