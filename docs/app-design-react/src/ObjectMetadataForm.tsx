import { useRef, useState } from "react";
import { Button } from "./ui";

export type MetadataField = { key: string; label: string; value: string };

export function ObjectMetadataForm({
	label,
	fields,
	readOnlyFields = [],
	required = [],
	onSave,
}: {
	label: string;
	fields: MetadataField[];
	readOnlyFields?: MetadataField[];
	required?: string[];
	onSave: (values: Record<string, string>) => void;
}) {
	const [editing, setEditing] = useState(false);
	const [draft, setDraft] = useState<Record<string, string>>({});
	const [error, setError] = useState("");
	const actionRef = useRef<HTMLButtonElement>(null);
	const beginEdit = () => {
		setDraft(Object.fromEntries(fields.map(({ key, value }) => [key, value])));
		setError("");
		setEditing(true);
	};
	const cancel = () => {
		setEditing(false);
		setError("");
		setDraft({});
		requestAnimationFrame(() => actionRef.current?.focus());
	};
	const save = () => {
		const missing = fields.find(
			({ key }) => required.includes(key) && !draft[key]?.trim(),
		);
		if (missing) {
			setError(`Заполните поле «${missing.label}».`);
			return;
		}
		onSave(
			Object.fromEntries(fields.map(({ key }) => [key, draft[key] ?? ""])),
		);
		setEditing(false);
		setError("");
		setDraft({});
	};
	return (
		<section
			aria-label={label}
			className="grid min-w-0 gap-3 rounded-[var(--control-radius)] border border-[var(--line)] bg-[var(--surface)] p-3"
			onKeyDown={(event) => {
				if (editing && event.key === "Escape") {
					event.preventDefault();
					cancel();
				}
			}}
		>
			<div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
				<h2 className="m-0 text-sm font-semibold">{label}</h2>
				<div className="flex min-h-[30px] items-center gap-2">
					<Button
						ref={actionRef}
						className="w-[132px] shrink-0"
						onPress={editing ? save : beginEdit}
					>
						{editing ? "Сохранить" : "Редактировать"}
					</Button>
					<Button
						className={`w-[132px] shrink-0 ${editing ? "visible" : "invisible"}`}
						isDisabled={!editing}
						aria-hidden={!editing}
						onPress={cancel}
					>
						Отменить
					</Button>
				</div>
			</div>
			<div className="grid min-w-0 grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
				{fields.map(({ key, label: fieldLabel, value }) => (
					<label
						key={key}
						className="grid min-w-0 gap-1 text-xs text-[var(--muted)]"
					>
						<span>{fieldLabel}</span>
						<input
							aria-label={fieldLabel}
							value={editing ? (draft[key] ?? "") : value}
							readOnly={!editing}
							className={`h-8 min-w-0 w-full truncate rounded-[var(--control-radius)] border border-transparent bg-transparent px-2 text-sm text-[var(--ink)] outline-none ${editing ? "border-b-[var(--control)] focus-visible:border-b-[var(--accent)] focus-visible:ring-2 focus-visible:ring-[var(--focus)]" : ""}`}
							onChange={(event) => {
								setDraft((current) => ({
									...current,
									[key]: event.target.value,
								}));
								setError("");
							}}
						/>
					</label>
				))}
				{readOnlyFields.map(({ key, label: fieldLabel, value }) => (
					<label
						key={key}
						className="grid min-w-0 gap-1 text-xs text-[var(--muted)]"
					>
						<span>{fieldLabel}</span>
						<input
							aria-label={fieldLabel}
							value={value}
							readOnly
							className="h-8 min-w-0 w-full truncate rounded-[var(--control-radius)] border border-transparent bg-transparent px-2 text-sm text-[var(--muted)] outline-none"
						/>
					</label>
				))}
			</div>
			<p aria-live="polite" className="m-0 min-h-4 text-xs text-[var(--error)]">
				{error}
			</p>
		</section>
	);
}
