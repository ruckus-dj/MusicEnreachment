import { type ReactNode, useEffect, useRef, useState } from "react";
import type { LocalReleaseCopy } from "./localReleaseMessages";

export type SearchResult<T> =
	| { kind: "idle" | "pending" | "error" }
	| { kind: "ready"; hits: readonly T[] };

export function useLookup<T>() {
	const [result, setResult] = useState<SearchResult<T>>({ kind: "idle" });
	const generation = useRef(0);
	const controller = useRef<AbortController | null>(null);
	useEffect(
		() => () => {
			generation.current += 1;
			controller.current?.abort();
			controller.current = null;
		},
		[],
	);
	function reset() {
		generation.current += 1;
		controller.current?.abort();
		controller.current = null;
		setResult({ kind: "idle" });
	}
	async function search(load: (signal: AbortSignal) => Promise<readonly T[]>) {
		const request = ++generation.current;
		controller.current?.abort();
		const next = new AbortController();
		controller.current = next;
		setResult({ kind: "pending" });
		try {
			const hits = await load(next.signal);
			if (request === generation.current && !next.signal.aborted)
				setResult({ kind: "ready", hits });
		} catch {
			if (request === generation.current && !next.signal.aborted)
				setResult({ kind: "error" });
		}
	}
	return { result, reset, search };
}

export const button =
	"react-aria-Button ui-button inline-flex min-h-9 pointer-coarse:min-h-10 cursor-pointer items-center justify-center gap-2 rounded-[var(--control-radius)] border px-3 py-1.5 text-sm font-medium no-underline motion-safe:transition-[background-color,border-color,color] motion-safe:duration-150 motion-reduce:transition-none aria-disabled:cursor-not-allowed aria-disabled:bg-[var(--raised)] aria-disabled:text-[var(--muted)] disabled:cursor-not-allowed disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2";
export const panel =
	"min-w-0 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5 max-[600px]:p-4";
export const actions = "flex flex-wrap items-center gap-2";
export const secondary = `${button} max-w-full border-[var(--line)] bg-[var(--surface)] text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] aria-disabled:border-[var(--line)] disabled:border-[var(--line)]`;
export const primary = `${button} max-w-full border-transparent bg-[var(--button)] text-[var(--button-text)] hover:bg-[var(--button-hover)] aria-disabled:border-[var(--line)] aria-disabled:text-[var(--muted)] disabled:border-[var(--line)] disabled:text-[var(--muted)]`;
export const muted = "m-0 text-xs text-[var(--muted)] wrap-anywhere";
export const rows = "m-0 list-none divide-y divide-[var(--line)] p-0";
export const row =
	"flex min-w-0 flex-wrap items-center justify-between gap-2 py-3";
export const errorClass = "m-0 text-xs font-medium text-[var(--error)]";

export function Field({
	label,
	value,
	onChange,
	required,
	placeholder,
	readOnly,
	disabled,
	describedBy,
}: {
	label: string;
	value: string;
	onChange: (value: string) => void;
	required?: boolean;
	placeholder?: string;
	readOnly?: boolean;
	disabled?: boolean;
	describedBy?: string;
}) {
	return (
		<label className="grid min-w-0 gap-1.5 text-sm font-medium">
			{label}
			<input
				className="h-9 w-full min-w-0 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] hover:border-[var(--control)] focus-visible:border-[var(--accent)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0 disabled:bg-[var(--raised)] disabled:text-[var(--muted)]"
				value={value}
				onChange={(event) => onChange(event.target.value)}
				required={required}
				placeholder={placeholder}
				readOnly={readOnly}
				disabled={disabled}
				aria-describedby={describedBy}
			/>
		</label>
	);
}

export function LookupStatus<T>({
	result,
	copy,
	empty,
	children,
}: {
	result: SearchResult<T>;
	copy: LocalReleaseCopy;
	empty: string;
	children: (hits: readonly T[]) => ReactNode;
}) {
	if (result.kind === "pending")
		return (
			<p className={muted} role="status">
				{copy.checking}
			</p>
		);
	if (result.kind === "error")
		return (
			<p className={errorClass} role="alert">
				{copy.checkError}
			</p>
		);
	if (result.kind !== "ready") return null;
	return (
		<>
			{result.hits.length === 0 && (
				<p className={muted} role="status">
					{empty}
				</p>
			)}
			{children(result.hits)}
		</>
	);
}
