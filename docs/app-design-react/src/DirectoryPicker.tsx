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
import "./forms-operations.css";

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
	const pathInputRef = useRef<HTMLInputElement>(null);
	const editTriggerRef = useRef<HTMLButtonElement>(null);
	const returnFocusAfterCancel = useRef(false);
	const [path, setPath] = useState(
		directories[initialPath] ? initialPath : "/",
	);
	const [editing, setEditing] = useState(false);
	const [requested, setRequested] = useState(path);
	const [error, setError] = useState("");
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
		setError("");
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
				className="directory-dialog"
				aria-labelledby="directory-picker-title"
				aria-describedby="directory-picker-help"
			>
				<Heading slot="title" id="directory-picker-title">
					Папка на сервере
				</Heading>
				<p id="directory-picker-help">
					Каталоги на сервере MeloTrove — не файлы устройства.
				</p>
				<nav
					className="directory-breadcrumbs"
					aria-label="Навигация по каталогам"
				>
					<Button
						aria-label="На уровень выше"
						isDisabled={path === "/"}
						onPress={() => go(`/${parts.slice(0, -1).join("/")}`)}
					>
						↑
					</Button>
					<Button
						ref={editTriggerRef}
						onPress={() => (path === "/" ? setEditing(true) : go("/"))}
						aria-label={path === "/" ? "Редактировать путь" : undefined}
					>
						Сервер
					</Button>
					{parts.map((part, index) => {
						const current = index === parts.length - 1;
						return (
							<Button
								key={parts.slice(0, index + 1).join("/")}
								ref={current ? editTriggerRef : undefined}
								aria-label={current ? "Редактировать путь" : undefined}
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
								setError(
									"В демонстрационном дереве такого каталога нет. Проверьте путь или выберите папку из списка.",
								);
							else go(next);
						}}
					>
						<TextField
							className="demo-search"
							aria-label="Абсолютный серверный путь"
							value={requested}
							onChange={setRequested}
						>
							<Label>Абсолютный серверный путь</Label>
							<Input
								ref={pathInputRef}
								autoComplete="off"
								spellCheck={false}
								aria-invalid={Boolean(error)}
								onKeyDown={(event) => {
									if (event.key === "Escape") {
										event.stopPropagation();
										event.preventDefault();
										returnFocusAfterCancel.current = true;
										setEditing(false);
										setError("");
									}
								}}
							/>
						</TextField>
						<Button type="submit">Перейти</Button>
					</form>
				)}
				<div aria-live="polite">
					{error ? (
						<p role="alert">Путь не найден. {error}</p>
					) : unreadable ? (
						<p>
							Каталог недоступен. Демо-ответ: сервер не может прочитать этот
							каталог. Выберите другой путь или вернитесь назад.
						</p>
					) : !folders ? (
						<p>
							Путь не найден. В демонстрационном дереве такого каталога нет.
							Проверьте путь или выберите папку из списка.
						</p>
					) : folders.length ? (
						<div className="directory-list-scroll">
							<ul className="demo-directory-list">
								{folders.map((folder) => (
									<li key={folder}>
										<Button
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
						<p>
							В этой папке нет вложенных каталогов. Вы можете выбрать текущую
							папку.
						</p>
					)}
				</div>
				<Toolbar className="directory-footer">
					<Button onPress={onClose}>Отмена</Button>
					<Button
						isDisabled={!folders || unreadable || Boolean(error)}
						onPress={() => onChoose(path)}
					>
						Выбрать папку
					</Button>
				</Toolbar>
			</Dialog>
		</Modal>
	);
}
