import {
	browseDemoDirectory,
	demoDirectoryStart,
} from "../fixtures/directoryBrowseDemo";
import { useI18n } from "../i18n/I18nProvider";
import { DirectoryPickerView } from "./DirectoryPickerView";

export function DirectoryPicker({
	initialPath,
	onChoose,
	onClose,
}: {
	initialPath: string;
	onChoose: (path: string) => void;
	onClose: () => void;
}) {
	const { locale } = useI18n();
	return (
		<DirectoryPickerView
			open
			initialPath={demoDirectoryStart(initialPath)}
			browse={browseDemoDirectory}
			locale={locale}
			onSelect={onChoose}
			onClose={onClose}
		/>
	);
}
