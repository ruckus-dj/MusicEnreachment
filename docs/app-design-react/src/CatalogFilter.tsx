import { Label, Radio, RadioGroup } from "./ui";
import type { CatalogFilter as CatalogFilterValue } from "./availabilityRows";
import { catalogueStyles } from "./catalogueStyles";

export function CatalogFilter({
	value,
	onChange,
}: {
	value: CatalogFilterValue;
	onChange: (value: CatalogFilterValue) => void;
}) {
	return (
		<RadioGroup
			orientation="horizontal"
			value={value}
			onChange={(next) => onChange(next as CatalogFilterValue)}
			className={catalogueStyles.filter}
		>
			<Label>Состояние</Label>
			<div className={catalogueStyles.segments}>
				<Radio value="all" className={catalogueStyles.segment}>
					Все
				</Radio>
				<Radio value="missing" className={catalogueStyles.segment}>
					Не хватает
				</Radio>
				<Radio value="available" className={catalogueStyles.segment}>
					В наличии
				</Radio>
				<Radio value="published" className={catalogueStyles.segment}>
					Опубликовано
				</Radio>
			</div>
		</RadioGroup>
	);
}
