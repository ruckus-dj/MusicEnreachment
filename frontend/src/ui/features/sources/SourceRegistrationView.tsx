import { useEffect, useId, useLayoutEffect, useRef } from "react";
import {
	Input,
	Label,
	Radio,
	RadioGroup,
	TextField,
} from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { DirectoryPathFieldView } from "../../components/DirectoryPathFieldView";
import type { BrowseDirectory } from "../../components/DirectoryPickerView";

export type SourceRegistrationMode = "in_place" | "staged";

export type SourceRegistrationErrorKey =
	| "nameRequired"
	| "directoryRequired"
	| "modeRequired"
	| "pathOverlap";

export const sourceRegistrationMessages = {
	en: {
		newSource: "New source",
		name: "Source name",
		namePlaceholder: "For example, Music library",
		serverDirectory: "Server directory",
		onlyDirectory: "Choose a directory; files are not uploaded.",
		processingMode: "Processing mode · choose explicitly",
		inPlace: "In place",
		inPlaceHelp: "Analysis reads the original file without modifying it.",
		staged: "Using a work copy",
		stagedHelp:
			"Processing uses an application-owned audio copy in the shared server work directory. The directory is required for processing, not for adding or scanning a source.",
		sourcePolicy: "Analysis does not modify originals.",
		sourceTableHelp:
			"Analysis leaves original files unchanged. Mode changes apply to future processing.",
		nameRequired: "Enter a source name.",
		directoryRequired: "Choose a server directory.",
		modeRequired:
			"Choose a processing mode explicitly: In place or Using a work copy.",
		pathOverlap:
			"This source directory overlaps the shared work, tools, or publication directory.",
		addSource: "Add source",
		cancel: "Cancel",
	},
	ru: {
		newSource: "Новый источник",
		name: "Имя источника",
		namePlaceholder: "Например, Музыкальная библиотека",
		serverDirectory: "Каталог на сервере",
		onlyDirectory: "Выберите каталог; файлы не загружаются.",
		processingMode: "Режим обработки · выберите явно",
		inPlace: "На месте",
		inPlaceHelp: "Анализ читает исходный файл, не изменяя его.",
		staged: "Через рабочую копию",
		stagedHelp:
			"Для обработки используется принадлежащая приложению рабочая копия аудио в общей рабочей папке сервера. Папка требуется для обработки, но не для добавления или сканирования источника.",
		sourcePolicy: "Анализ не изменяет исходники.",
		sourceTableHelp:
			"Анализ не изменяет исходные файлы. Изменение режима применяется к будущей обработке.",
		nameRequired: "Укажите имя источника.",
		directoryRequired: "Выберите каталог на сервере.",
		modeRequired:
			"Явно выберите режим обработки: «На месте» или «Через рабочую копию».",
		pathOverlap:
			"Каталог источника пересекается с общей рабочей папкой, каталогом инструментов или каталогом публикации.",
		addSource: "Добавить источник",
		cancel: "Отмена",
	},
} as const;

export function sourceRegistrationMessage(
	locale: "en" | "ru",
	key: SourceRegistrationErrorKey,
) {
	return sourceRegistrationMessages[locale][key];
}

export function useSourceRegistrationFocus(open: boolean) {
	const triggerId = useId();
	const returning = useRef(false);
	useLayoutEffect(() => {
		if (open || !returning.current) return;
		returning.current = false;
		document.getElementById(triggerId)?.focus();
	}, [open, triggerId]);
	return {
		triggerId,
		restoreOnClose: () => {
			returning.current = true;
		},
	};
}

export type SourceRegistrationViewProps = {
	locale: "en" | "ru";
	name: string;
	path: string;
	mode: SourceRegistrationMode | "";
	saving?: boolean;
	error?: string;
	browse: BrowseDirectory;
	resolveInitialPath?: (value: string) => string;
	onNameChange: (value: string) => void;
	onPathChange: (value: string) => void;
	onModeChange: (value: SourceRegistrationMode) => void;
	onSubmit: () => void;
	onCancel: () => void;
};

const modeChoice =
	"group flex min-h-11 cursor-pointer items-center gap-2.5 rounded-lg border border-[var(--line)] px-3.5 py-2.5 text-sm text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] data-[selected]:border-[var(--accent)] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";
const modeHelp = "m-0 ml-8 text-xs text-[var(--muted)]";
const radioIndicator =
	"size-4 flex-none rounded-full border border-[var(--control)] bg-[var(--surface)] group-data-[selected]:border-[5px] group-data-[selected]:border-[var(--accent)]";

export function SourceRegistrationView({
	locale,
	name,
	path,
	mode,
	saving = false,
	error,
	browse,
	resolveInitialPath,
	onNameChange,
	onPathChange,
	onModeChange,
	onSubmit,
	onCancel,
}: SourceRegistrationViewProps) {
	const copy = sourceRegistrationMessages[locale];
	const titleId = useId();
	const nameId = useId();
	const pathId = useId();
	const nameField = useRef<HTMLInputElement>(null);
	const alert = useRef<HTMLParagraphElement>(null);

	useLayoutEffect(() => {
		nameField.current?.focus();
	}, []);

	useEffect(() => {
		if (error) alert.current?.focus();
	}, [error]);

	return (
		<form
			aria-labelledby={titleId}
			className="grid min-w-0 gap-4 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5"
			onSubmit={(event) => {
				event.preventDefault();
				onSubmit();
			}}
		>
			<h2 id={titleId} className="m-0 text-base font-semibold">
				{copy.newSource}
			</h2>
			<TextField
				value={name}
				onChange={onNameChange}
				isDisabled={saving}
				validationBehavior="aria"
				className="grid min-w-0 gap-1.5"
			>
				<Label className="text-sm font-medium text-[var(--ink)]">
					{copy.name}
				</Label>
				<Input
					id={nameId}
					ref={nameField}
					placeholder={copy.namePlaceholder}
					className="h-9 w-full min-w-0 rounded-lg border border-[var(--control)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] placeholder:text-[var(--muted)] hover:border-[var(--control)] focus-visible:border-[var(--accent)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0 disabled:bg-[var(--raised)] disabled:text-[var(--muted)] pointer-coarse:h-10"
				/>
			</TextField>
			<DirectoryPathFieldView
				id={pathId}
				label={copy.serverDirectory}
				value={path}
				onSelect={onPathChange}
				disabled={saving}
				description={copy.onlyDirectory}
				browse={browse}
				locale={locale}
				resolveInitialPath={resolveInitialPath}
			/>
			<RadioGroup
				className="grid gap-1"
				aria-label={copy.processingMode}
				value={mode}
				isDisabled={saving}
				onChange={(value) => onModeChange(value as SourceRegistrationMode)}
			>
				<Radio className={modeChoice} value="in_place">
					<span aria-hidden="true" className={radioIndicator} />
					{copy.inPlace}
				</Radio>
				<p className={modeHelp}>{copy.inPlaceHelp}</p>
				<Radio className={modeChoice} value="staged">
					<span aria-hidden="true" className={radioIndicator} />
					{copy.staged}
				</Radio>
				<p className={modeHelp}>{copy.stagedHelp}</p>
			</RadioGroup>
			<p className="m-0 text-sm text-[var(--muted)]">{copy.sourcePolicy}</p>
			{error && (
				<p
					ref={alert}
					tabIndex={-1}
					role="alert"
					className="m-0 text-sm font-medium text-[var(--error)]"
				>
					{error}
				</p>
			)}
			<div className="flex flex-wrap items-center gap-2">
				<AppButton type="submit" variant="primary" isDisabled={saving}>
					{copy.addSource}
				</AppButton>
				<AppButton onPress={onCancel} isDisabled={saving}>
					{copy.cancel}
				</AppButton>
			</div>
		</form>
	);
}
