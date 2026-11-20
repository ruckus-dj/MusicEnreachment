import { useI18n } from "../../i18n/I18nProvider";
import type { CatalogFilter as CatalogFilterValue } from "./availabilityRows";
import { LibraryCatalogFilterView } from "./LibraryCatalogFilterView";

export function CatalogFilter({
	value,
	onChange,
}: {
	value: CatalogFilterValue;
	onChange: (value: CatalogFilterValue) => void;
}) {
	const { locale } = useI18n();
	return (
		<LibraryCatalogFilterView
			locale={locale}
			value={value}
			onChange={onChange}
		/>
	);
}
