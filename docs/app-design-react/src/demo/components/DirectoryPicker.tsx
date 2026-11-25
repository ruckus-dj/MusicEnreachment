import { useI18n } from "../../app/i18n/host";
import { DirectoryPickerView } from "../../ui/components/DirectoryPickerView";
import {
	browseDemoDirectory,
	demoDirectoryStart,
} from "../fixtures/directoryBrowseDemo";

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
