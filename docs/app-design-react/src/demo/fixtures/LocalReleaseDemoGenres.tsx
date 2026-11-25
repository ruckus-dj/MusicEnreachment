import type { LocalReleaseGenreFieldProps } from "../../ui/features/local-release/LocalReleaseForm";
import { GenrePicker } from "../providers/library/metadata/GenrePicker";

type Props = LocalReleaseGenreFieldProps & {
	loaded: boolean;
	onLoad?: () => void | Promise<void>;
	options?: readonly { id: string; name: string }[];
};

export function LocalReleaseDemoGenres({
	value,
	onChange,
	disabled,
	loaded,
	onLoad,
	options,
}: Props) {
	return (
		<GenrePicker
			value={[...value]}
			readOnly={disabled}
			loaded={loaded}
			onLoad={onLoad}
			options={options}
			onChange={(next) =>
				onChange(
					next.map(({ id, name }) => ({
						id: id ?? name,
						name,
						source_name:
							value.find((previous) => previous.id === id)?.source_name ?? name,
					})),
				)
			}
		/>
	);
}
