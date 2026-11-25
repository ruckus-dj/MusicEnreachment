import { GenrePickerView } from "./GenrePickerView";
import {
	type GenrePickerProps,
	useGenrePickerDemoProvider,
} from "./useGenrePickerDemoProvider";

export type { GenreReference } from "./GenrePickerView";

/**
 * Public genre picker entry point. The demo provider owns catalog, search and
 * load state; the view owns markup and open state. This facade preserves the
 * existing props and imports used by metadata and local-catalog editors.
 */
export function GenrePicker(props: GenrePickerProps) {
	return <GenrePickerView {...useGenrePickerDemoProvider(props)} />;
}
