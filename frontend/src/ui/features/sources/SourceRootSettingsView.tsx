import { useEffect, useId, useRef, useState } from "react";
import { Radio, RadioGroup } from "react-aria-components";
import { AppButton } from "../../components/AppButton";
import { DirectoryPathFieldView } from "../../components/DirectoryPathFieldView";
import type { BrowseDirectory } from "../../components/DirectoryPickerView";
import { sourceRegistrationMessages } from "./SourceRegistrationView";

export type SourceRootChange =
	| { field: "name"; value: string }
	| { field: "path"; value: string }
	| { field: "mode"; value: "in_place" | "staged" };

export type SourceRootSettingsViewProps = {
	locale: "en" | "ru";
	name: string;
	path: string;
	mode: "in_place" | "staged";
	browse: BrowseDirectory;
	resolveInitialPath?: (value: string) => string;
	disabled?: boolean;
	error?: string;
	onSave: (change: SourceRootChange) => Promise<boolean>;
};

const messages = {
	en: {
		title: "Source settings",
		save: "Save",
		nameTooLong: "Use at most 256 characters for the source name.",
		directoryHelp:
			"The inventory stays with the previous folder until a scan of the new folder succeeds.",
		processingMode: "Processing mode",
		modeHelp: "Changes apply to future processing.",
		saving: "Saving…",
		fallbackError: "The source could not be saved.",
	},
	ru: {
		title: "Параметры источника",
		save: "Сохранить",
		nameTooLong: "В имени источника должно быть не больше 256 символов.",
		directoryHelp:
			"Инвентарь остаётся у прежнего каталога, пока не завершится успешное сканирование нового.",
		processingMode: "Режим обработки",
		modeHelp: "Изменение влияет на будущую обработку.",
		saving: "Сохранение…",
		fallbackError: "Не удалось сохранить источник.",
	},
} as const;

const modeChoice =
	"group flex min-h-11 cursor-pointer items-center gap-2.5 rounded-lg border border-[var(--line)] px-3.5 py-2.5 text-sm text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] data-[selected]:border-[var(--accent)] data-[selected]:bg-[var(--accent-soft)] data-[focus-visible]:outline-2 data-[focus-visible]:outline-[var(--accent)] data-[focus-visible]:outline-offset-2";
const modeHelp = "m-0 ml-8 text-xs text-[var(--muted)]";
const radioIndicator =
	"size-4 flex-none rounded-full border border-[var(--control)] bg-[var(--surface)] group-data-[selected]:border-[5px] group-data-[selected]:border-[var(--accent)]";
const textField =
	"h-9 w-full min-w-0 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] hover:border-[var(--control)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0";

export function SourceRootSettingsView({
	locale,
	name,
	path,
	mode,
	browse,
	resolveInitialPath,
	disabled = false,
	error,
	onSave,
}: SourceRootSettingsViewProps) {
	const copy = messages[locale];
	const common = sourceRegistrationMessages[locale];
	const titleId = useId();
	const nameId = useId();
	const nameErrorId = useId();
	const pathId = useId();
	const [nameDraft, setNameDraft] = useState<string | null>(null);
	const [showNameError, setShowNameError] = useState(false);
	const [pending, setPending] = useState<SourceRootChange | null>(null);
	const [failure, setFailure] = useState(false);
	const inFlight = useRef(false);
	const alert = useRef<HTMLParagraphElement>(null);
	const nameField = useRef<HTMLInputElement>(null);

	const shownError = !pending && (error || (failure && copy.fallbackError));
	useEffect(() => {
		if (shownError) alert.current?.focus();
	}, [shownError]);

	const nameValue = nameDraft ?? name;
	const trimmedName = nameValue.trim();
	const nameError = !trimmedName
		? common.nameRequired
		: Array.from(trimmedName).length > 256
			? copy.nameTooLong
			: null;
	const nameChanged = nameValue !== name;
	const busy = disabled || pending !== null;

	async function commit(change: SourceRootChange) {
		if (inFlight.current || disabled) return;
		inFlight.current = true;
		setPending(change);
		setFailure(false);
		try {
			const saved = await onSave(change);
			if (!saved) setFailure(true);
			else if (change.field === "name") {
				setNameDraft((draft) =>
					draft?.trim() === change.value ? null : draft,
				);
			}
		} catch {
			setFailure(true);
		} finally {
			inFlight.current = false;
			setPending(null);
		}
	}

	function saveName() {
		if (nameError) {
			setShowNameError(true);
			nameField.current?.focus();
			return;
		}
		if (trimmedName === name) {
			setNameDraft(null);
			return;
		}
		void commit({ field: "name", value: trimmedName });
	}

	function changePath(next: string) {
		if (next === path) return;
		void commit({ field: "path", value: next });
	}

	function changeMode(next: "in_place" | "staged") {
		if (next === mode) return;
		void commit({ field: "mode", value: next });
	}

	return (
		<section
			aria-labelledby={titleId}
			className="grid min-w-0 gap-4 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5"
		>
			<h2 id={titleId} className="m-0 text-sm font-semibold">
				{copy.title}
			</h2>
			<div className="grid min-w-0 gap-1">
				<label
					className="text-sm font-medium text-[var(--ink)]"
					htmlFor={nameId}
				>
					{common.name}
				</label>
				<div className="flex min-w-0 gap-2">
					<input
						id={nameId}
						ref={nameField}
						value={nameValue}
						disabled={disabled}
						aria-invalid={showNameError && Boolean(nameError)}
						aria-describedby={
							showNameError && nameError ? nameErrorId : undefined
						}
						onChange={(event) => {
							const value = event.target.value;
							setNameDraft(
								value === name && pending?.field !== "name" ? null : value,
							);
							setShowNameError(false);
						}}
						onKeyDown={(event) => {
							if (event.key === "Escape" && !pending && nameDraft !== null) {
								setNameDraft(null);
								setShowNameError(false);
							}
						}}
						className={textField}
					/>
					{nameChanged && (
						<AppButton isDisabled={busy} onPress={saveName}>
							{copy.save}
						</AppButton>
					)}
				</div>
				{showNameError && nameError && (
					<p id={nameErrorId} role="alert" className="m-0 text-[var(--error)]">
						{nameError}
					</p>
				)}
			</div>
			<DirectoryPathFieldView
				id={pathId}
				label={common.serverDirectory}
				value={pending?.field === "path" ? pending.value : path}
				onSelect={changePath}
				disabled={busy}
				description={copy.directoryHelp}
				browse={browse}
				locale={locale}
				resolveInitialPath={resolveInitialPath}
			/>
			<RadioGroup
				className="grid gap-1"
				aria-label={copy.processingMode}
				value={pending?.field === "mode" ? pending.value : mode}
				isDisabled={busy}
				onChange={(value) => changeMode(value as "in_place" | "staged")}
			>
				<Radio className={modeChoice} value="in_place">
					<span aria-hidden="true" className={radioIndicator} />
					{common.inPlace}
				</Radio>
				<Radio className={modeChoice} value="staged">
					<span aria-hidden="true" className={radioIndicator} />
					{common.staged}
				</Radio>
			</RadioGroup>
			<p className={modeHelp}>{copy.modeHelp}</p>
			{pending && <p role="status">{copy.saving}</p>}
			{shownError && (
				<p ref={alert} tabIndex={-1} role="alert" className="m-0">
					{shownError}
				</p>
			)}
		</section>
	);
}
