import { Label, Radio, RadioGroup } from "./ui";
import type { CatalogFilter as CatalogFilterValue } from "./availabilityRows";

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
			className="catalogue-filter catalogue-radio-filter"
		>
			<Label>Состояние</Label>
			<div className="catalogue-segments">
				<Radio value="all" className="catalogue-segment">
					Все
				</Radio>
				<Radio value="missing" className="catalogue-segment">
					Не хватает
				</Radio>
				<Radio value="available" className="catalogue-segment">
					В наличии
				</Radio>
				<Radio value="published" className="catalogue-segment">
					Опубликовано
				</Radio>
			</div>
		</RadioGroup>
	);
}
