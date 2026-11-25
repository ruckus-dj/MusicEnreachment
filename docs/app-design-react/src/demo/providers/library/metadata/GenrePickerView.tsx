import { GenrePicker as LibraryGenrePicker } from "../../../../ui/features/library/GenrePicker";
import {
	type GenrePickerProps,
	useGenrePickerDemoProvider,
} from "./useGenrePickerDemoProvider";

export type { GenreReference } from "./useGenrePickerDemoProvider";

/**
 * Small demo adapter for the shared library genre picker. Fixture catalog,
 * name resolution, search ranking and selection retention live in the demo
 * provider; the shared picker owns the dialog, status, search, selection and
 * keyboard behavior.
 */
export function GenrePickerView(props: GenrePickerProps) {
	const picker = useGenrePickerDemoProvider(props);
	return (
		<div className="grid min-w-0 content-start gap-1 text-xs text-[var(--muted)]">
			<span>{picker.label}</span>
			<LibraryGenrePicker {...picker} />
		</div>
	);
}
