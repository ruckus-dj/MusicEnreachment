import { SubmenuTrigger } from "react-aria-components";
import { useI18n } from "../../i18n";
import { Button, Menu, MenuItem, MenuTrigger, Popover } from "../../ui";
import { type IncomingMessage, incomingMessage } from "./incomingMessages";

export type IncomingFileDestination = {
	id: string;
	name: string;
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

const submenuPopoverClass =
	"w-max max-w-[min(22rem,calc(100vw-1rem))] max-h-[min(20rem,calc(100dvh-1rem))] overflow-y-auto";

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
	if (!destinations.length && !canCreateGroup) return null;
	return (
		<MenuTrigger>
			<Button
				aria-label={label}
				className="size-8 min-h-8 min-w-8 justify-center border-transparent! bg-transparent! p-0 text-[var(--muted)] hover:border-[var(--line)]! hover:bg-[var(--raised)]! hover:text-[var(--ink)] pointer-coarse:size-11 pointer-coarse:min-h-11 pointer-coarse:min-w-11"
			>
				<EllipsisIcon />
			</Button>
			<Popover placement="bottom end" className={submenuPopoverClass}>
				<Menu aria-label={label} className="outline-none">
					{destinations.length > 0 && (
						<SubmenuTrigger>
							<MenuItem className={itemClass} textValue={msg("moveTo")}>
								<span className="min-w-0 wrap-anywhere">{msg("moveTo")}</span>
								<ChevronRightIcon />
							</MenuItem>
							<Popover placement="right top" className={submenuPopoverClass}>
								<Menu
									aria-label={msg("moveDestinationForFile", {
										file: fileTitle,
									})}
									className="outline-none"
								>
									{destinations.map((destination) => (
										<MenuItem
											key={destination.id}
											id={destination.id}
											className={itemClass}
											textValue={destination.name}
											onAction={() => onMove(fileId, destination.id)}
										>
											<span className="min-w-0 wrap-anywhere">
												{destination.name}
											</span>
										</MenuItem>
									))}
								</Menu>
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
