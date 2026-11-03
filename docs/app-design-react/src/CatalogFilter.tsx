import { Label, Radio, RadioGroup } from "./ui";
import type { CatalogFilter as CatalogFilterValue } from "./availabilityRows";
import { catalogueStyles } from "./catalogueStyles";
import { useI18n } from "./i18n/I18nProvider";
import {
	libraryRowsMessage,
	type LibraryRowsMessage,
} from "./libraryRowsMessages";

export function CatalogFilter({
	value,
	onChange,
}: {
	value: CatalogFilterValue;
	onChange: (value: CatalogFilterValue) => void;
}) {
	const { locale } = useI18n();
	const m = (key: LibraryRowsMessage) => libraryRowsMessage(locale, key);
	return (
		<RadioGroup
			orientation="horizontal"
			value={value}
			onChange={(next) => onChange(next as CatalogFilterValue)}
			className={catalogueStyles.filter}
		>
			<Label>{m("filterState")}</Label>
			<div className={catalogueStyles.segments}>
				<Radio value="all" className={catalogueStyles.segment}>
					{m("filterAll")}
				</Radio>
				<Radio value="missing" className={catalogueStyles.segment}>
					{m("filterMissing")}
				</Radio>
				<Radio value="available" className={catalogueStyles.segment}>
					{m("filterAvailable")}
				</Radio>
				<Radio value="published" className={catalogueStyles.segment}>
					{m("filterPublished")}
				</Radio>
			</div>
		</RadioGroup>
	);
}
