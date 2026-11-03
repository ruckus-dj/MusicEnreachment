import { useI18n } from "./i18n";
import { shell } from "./i18n/namespaces/shell";
import {
	Button,
	ListBox,
	ListBoxItem,
	Popover,
	Select,
	SelectValue,
} from "./ui";

export function LanguageControl() {
	const { locale, preference, setPreference, persistenceError } = useI18n();
	const copy = shell[locale];
	return (
		<div className="flex min-w-0 items-center gap-2">
			<Select
				aria-label={copy.language}
				selectedKey={preference}
				onSelectionChange={(key) => {
					if (key === "system" || key === "en" || key === "ru")
						setPreference(key);
				}}
			>
				<Button className="w-[122px] justify-between text-xs max-[599px]:w-[112px]">
					<SelectValue />
					<svg
						aria-hidden="true"
						viewBox="0 0 16 16"
						className="size-3 shrink-0"
						fill="none"
						stroke="currentColor"
						strokeWidth="1.5"
					>
						<path d="m4 6 4 4 4-4" />
					</svg>
				</Button>
				<Popover>
					<ListBox>
						<ListBoxItem id="system" textValue={copy.system}>
							{copy.system}
						</ListBoxItem>
						<ListBoxItem id="en" textValue="English">
							<span lang="en">English</span>
						</ListBoxItem>
						<ListBoxItem id="ru" textValue="Русский">
							<span lang="ru">Русский</span>
						</ListBoxItem>
					</ListBox>
				</Popover>
			</Select>
			{persistenceError && (
				<span
					role="status"
					className="max-w-[220px] text-xs text-[var(--warning)]"
				>
					{persistenceError}
				</span>
			)}
		</div>
	);
}
