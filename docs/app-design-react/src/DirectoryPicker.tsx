import { useEffect, useRef, useState } from "react";
import {
	Button,
	Dialog,
	Heading,
	Input,
	Label,
	Modal,
	TextField,
	Toolbar,
} from "./ui";
import { formStyles as styles } from "./formStyles";
import { useI18n } from "./i18n";
import { directoryMessages } from "./i18n/namespaces/directories";

const directories: Record<string, string[]> = {
	"/": ["mnt", "srv", "var"],
	"/mnt": [
		"archive",
		"audio",
		"backup",
		"inbox",
		"music",
		"projects",
		"samples",
		"shared",
		"staging",
	],
	"/mnt/archive": [],
	"/mnt/inbox": ["Northern Lines", "Road Notes", "Small Hours"],
	"/mnt/audio": ["incoming", "masters"],
	"/mnt/music": ["Albums", "Compilations"],
	"/mnt/music/Albums": ["Artist A", "Artist B"],
	"/mnt/music/Compilations": ["Road Notes"],
	"/srv": ["media", "music"],
	"/srv/media": ["library"],
	"/srv/music": ["Albums", "Compilations"],
	"/srv/music/Albums": ["Northern Lines", "Small Hours"],
	"/srv/music/Compilations": ["Road Notes"],
	"/var": ["lib"],
	"/var/lib": ["melotrove"],
	"/var/lib/melotrove": ["cache", "tools"],
	"/var/lib/melotrove/tools": [],
};

export function DirectoryPicker({
	initialPath,
	onChoose,
	onClose,
}: {
	initialPath: string;
	onChoose: (path: string) => void;
	onClose: () => void;
}) {
	const { locale } = useI18n();
	const copy = directoryMessages[locale];
	const pathInputRef = useRef<HTMLInputElement>(null);
	const editTriggerRef = useRef<HTMLButtonElement>(null);
	const returnFocusAfterCancel = useRef(false);
	const [path, setPath] = useState(
		directories[initialPath] ? initialPath : "/",
	);
	const [editing, setEditing] = useState(false);
	const [requested, setRequested] = useState(path);
	const [invalidPath, setInvalidPath] = useState(false);
	useEffect(() => {
		if (editing) pathInputRef.current?.focus();
		else if (returnFocusAfterCancel.current) {
			returnFocusAfterCancel.current = false;
			editTriggerRef.current?.focus();
		}
	}, [editing]);
	const parts = path.split("/").filter(Boolean);
	const unreadable = path === "/mnt/archive";
	const folders = directories[path];
	const go = (next: string) => {
		setPath(next);
		setRequested(next);
		setEditing(false);
		setInvalidPath(false);
	};
	return (
		<Modal
			isOpen
			onOpenChange={(open) => {
				if (!open) onClose();
			}}
			isDismissable
		>
			<Dialog
				className={styles.directoryDialog}
				aria-labelledby="directory-picker-title"
				aria-describedby="directory-picker-help"
			>
				<Heading slot="title" id="directory-picker-title">
					{copy.title}
				</Heading>
				<p id="directory-picker-help">{copy.help}</p>
				<nav
					className={styles.directoryBreadcrumbs}
					aria-label={copy.breadcrumbs}
				>
					<Button
						aria-label={copy.up}
						isDisabled={path === "/"}
						onPress={() => go(`/${parts.slice(0, -1).join("/")}`)}
					>
						↑
					</Button>
					<Button
						ref={editTriggerRef}
						onPress={() => (path === "/" ? setEditing(true) : go("/"))}
						aria-label={path === "/" ? copy.editPath : undefined}
					>
						{copy.server}
					</Button>
					{parts.map((part, index) => {
						const current = index === parts.length - 1;
						return (
							<Button
								key={parts.slice(0, index + 1).join("/")}
								ref={current ? editTriggerRef : undefined}
								aria-label={current ? copy.editPath : undefined}
								aria-current={current ? "location" : undefined}
								onPress={() =>
									current
										? setEditing(true)
										: go(`/${parts.slice(0, index + 1).join("/")}`)
								}
							>
								{part}
								{current ? " ✎" : ""}
							</Button>
						);
					})}
				</nav>
				{editing && (
					<form
						onSubmit={(event) => {
							event.preventDefault();
							const next = requested.trim().replace(/\/+$/, "") || "/";
							if (!next.startsWith("/") || !Object.hasOwn(directories, next))
								setInvalidPath(true);
							else go(next);
						}}
					>
						<TextField
							className={styles.searchField}
							value={requested}
							onChange={setRequested}
						>
							<Label>{copy.absolutePath}</Label>
							<Input
								ref={pathInputRef}
								autoComplete="off"
								spellCheck={false}
								aria-invalid={invalidPath}
								aria-describedby={
									invalidPath ? "directory-path-error" : undefined
								}
								onKeyDown={(event) => {
									if (event.key === "Escape") {
										event.stopPropagation();
										event.preventDefault();
										returnFocusAfterCancel.current = true;
										setEditing(false);
										setInvalidPath(false);
									}
								}}
							/>
						</TextField>
						<Button type="submit">{copy.go}</Button>
					</form>
				)}
				<div aria-live="polite">
					{invalidPath ? (
						<p id="directory-path-error" role="alert">
							{copy.notFound} {copy.invalidPath}
						</p>
					) : unreadable ? (
						<p>{copy.unreadable}</p>
					) : !folders ? (
						<p>
							{copy.notFound} {copy.invalidPath}
						</p>
					) : folders.length ? (
						<div className={styles.directoryList}>
							<ul className="my-0 list-none p-0">
								{folders.map((folder) => (
									<li key={folder}>
										<Button
											className="!block !w-full !justify-start !rounded-none !border-0 !border-b !border-[var(--line)] !bg-transparent !px-[10px] !py-[10px] text-left hover:!bg-[var(--raised)]"
											onPress={() =>
												go(`${path === "/" ? "" : path}/${folder}`)
											}
										>
											{folder} ›
										</Button>
									</li>
								))}
							</ul>
						</div>
					) : (
						<p>{copy.empty}</p>
					)}
				</div>
				<Toolbar className={styles.directoryFooter}>
					<Button onPress={onClose}>{copy.cancel}</Button>
					<Button
						isDisabled={!folders || unreadable || invalidPath}
						onPress={() => onChoose(path)}
					>
						{copy.choose}
					</Button>
				</Toolbar>
			</Dialog>
		</Modal>
	);
}
