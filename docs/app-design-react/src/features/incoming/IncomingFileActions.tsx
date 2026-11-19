import { useEffect, useState } from "react";
import { Autocomplete, SubmenuTrigger, useFilter } from "react-aria-components";
import { useI18n } from "../../i18n";
import {
	Button,
	Input,
	Menu,
	MenuItem,
	MenuTrigger,
	Popover,
	SearchField,
} from "../../ui";
import { type IncomingMessage, incomingMessage } from "./incomingMessages";

export type IncomingFileDestination = {
	id: string;
	name: string;
	paths: readonly string[];
};

export type IncomingFileActionsProps = {
	fileId: string;
	fileTitle: string;
	destinations: readonly IncomingFileDestination[];
	canCreateGroup: boolean;
	onMove: (fileId: string, destinationId: string) => void;
	onCreateGroup: (fileId: string) => void;
};

const itemClass =
	"flex min-h-8 cursor-pointer items-center gap-2 rounded-[var(--control-radius)] px-2 py-[7px] text-xs text-[var(--ink)] outline-none data-[disabled]:cursor-not-allowed data-[disabled]:text-[var(--muted)] data-[focus-visible]:shadow-[inset_0_0_0_2px_var(--focus)] data-[focused]:bg-[var(--raised)] data-[open]:bg-[var(--raised)] pointer-coarse:min-h-11";

const menuPopoverClass =
	"w-max max-w-[min(22rem,calc(100vw-1rem))] max-h-[min(20rem,calc(100dvh-1rem))] overflow-y-auto";

const searchPopoverClass =
	"w-72 min-w-0 max-w-[calc(100vw-1rem)] max-h-[min(20rem,calc(100dvh-1rem))] overflow-hidden p-0";

const compactMenuQuery = "(max-width: 600px), (pointer: coarse)";

function useCompactMenuPlacement() {
	const [compact, setCompact] = useState(() =>
		typeof window.matchMedia === "function"
			? window.matchMedia(compactMenuQuery).matches
			: false,
	);
	useEffect(() => {
		if (typeof window.matchMedia !== "function") return;
		const media = window.matchMedia(compactMenuQuery);
		const update = () => setCompact(media.matches);
		update();
		media.addEventListener("change", update);
		return () => media.removeEventListener("change", update);
	}, []);
	return compact;
}

function EllipsisIcon() {
	return (
		<svg
			aria-hidden="true"
			focusable="false"
			viewBox="0 0 16 16"
			className="size-4"
		>
			<circle cx="3.25" cy="8" r="1.15" fill="currentColor" />
			<circle cx="8" cy="8" r="1.15" fill="currentColor" />
			<circle cx="12.75" cy="8" r="1.15" fill="currentColor" />
		</svg>
	);
}

function ChevronRightIcon() {
	return (
		<svg
			aria-hidden="true"
			focusable="false"
			viewBox="0 0 12 12"
			className="ml-auto size-3 shrink-0 text-[var(--muted)]"
		>
			<path
				d="M4.5 2.5 8 6l-3.5 3.5"
				fill="none"
				stroke="currentColor"
				strokeWidth="1.5"
				strokeLinecap="round"
				strokeLinejoin="round"
			/>
		</svg>
	);
}

type MoveDestinationMenuProps = {
	fileId: string;
	fileTitle: string;
	destinations: readonly IncomingFileDestination[];
	onMove: (fileId: string, destinationId: string) => void;
	msg: (
		key: IncomingMessage,
		values?: Record<string, string | number>,
	) => string;
};

function MoveDestinationMenu({
	fileId,
	fileTitle,
	destinations,
	onMove,
	msg,
}: MoveDestinationMenuProps) {
	const destinationsById = new Map(
		destinations.map((destination) => [destination.id, destination]),
	);
	const { contains } = useFilter({ sensitivity: "base" });
	const filter = (
		textValue: string,
		inputValue: string,
		node: { key: string | number },
	) => {
		const query = inputValue.trim();
		if (!query || contains(textValue, query)) return true;
		const destination = destinationsById.get(String(node.key));
		return Boolean(destination?.paths.some((path) => contains(path, query)));
	};
	return (
		<div className="flex max-h-[inherit] min-h-0 flex-col">
			<Autocomplete filter={filter} disableAutoFocusFirst>
				<SearchField
					autoFocus
					aria-label={msg("searchGroup")}
					className="shrink-0 border-b border-[var(--line)] p-2"
				>
					<Input
						className="min-h-7 py-1 text-xs placeholder:text-[var(--muted)]"
						placeholder={msg("searchGroup")}
					/>
				</SearchField>
				<Menu
					aria-label={msg("moveDestinationForFile", { file: fileTitle })}
					autoFocus={false}
					className="max-h-64 min-h-0 flex-1 overflow-y-auto p-1 outline-none"
					renderEmptyState={() => (
						<p className="m-0 px-2 py-3 text-center text-xs text-[var(--muted)]">
							{msg("noGroupsFound")}
						</p>
					)}
				>
					{destinations.map((destination) => (
						<MenuItem
							key={destination.id}
							id={destination.id}
							className={itemClass}
							textValue={destination.name}
							onAction={() => onMove(fileId, destination.id)}
						>
							<span className="min-w-0 wrap-anywhere">{destination.name}</span>
						</MenuItem>
					))}
				</Menu>
			</Autocomplete>
		</div>
	);
}

export function IncomingFileActions({
	fileId,
	fileTitle,
	destinations,
	canCreateGroup,
	onMove,
	onCreateGroup,
}: IncomingFileActionsProps) {
	const { locale } = useI18n();
	const msg = (
		key: IncomingMessage,
		values?: Record<string, string | number>,
	) => incomingMessage(locale, key, values);
	const label = msg("fileActions", { file: fileTitle });
	const compactMenu = useCompactMenuPlacement();
	if (!destinations.length && !canCreateGroup) return null;
	return (
		<MenuTrigger>
			<Button
				aria-label={label}
				className="size-8 min-h-8 min-w-8 justify-center border-transparent! bg-transparent! p-0 text-[var(--muted)] hover:border-[var(--line)]! hover:bg-[var(--raised)]! hover:text-[var(--ink)] pointer-coarse:size-11 pointer-coarse:min-h-11 pointer-coarse:min-w-11"
			>
				<EllipsisIcon />
			</Button>
			<Popover placement="bottom end" className={menuPopoverClass}>
				<Menu aria-label={label} className="outline-none">
					{destinations.length > 0 && (
						<SubmenuTrigger>
							<MenuItem className={itemClass} textValue={msg("moveTo")}>
								<span className="min-w-0 wrap-anywhere">{msg("moveTo")}</span>
								<ChevronRightIcon />
							</MenuItem>
							<Popover
								placement={compactMenu ? "bottom start" : "right top"}
								className={searchPopoverClass}
							>
								<MoveDestinationMenu
									fileId={fileId}
									fileTitle={fileTitle}
									destinations={destinations}
									onMove={onMove}
									msg={msg}
								/>
							</Popover>
						</SubmenuTrigger>
					)}
					{canCreateGroup && (
						<MenuItem
							className={itemClass}
							textValue={msg("createNewGroup")}
							onAction={() => onCreateGroup(fileId)}
						>
							<span className="min-w-0 wrap-anywhere">
								{msg("createNewGroup")}
							</span>
						</MenuItem>
					)}
				</Menu>
			</Popover>
		</MenuTrigger>
	);
}
