import { GenrePickerView } from "./GenrePickerView";
import type { GenrePickerProps } from "./useGenrePickerDemoProvider";

export type { GenreReference } from "./useGenrePickerDemoProvider";

/**
 * Public demo genre picker entry point. The adapter supplies fixture catalog,
 * search and selection state, while the shared library picker owns the
 * removable tags, search popover and all of its interaction behavior.
 */
export function GenrePicker(props: GenrePickerProps) {
  return <GenrePickerView {...props} />;
}
