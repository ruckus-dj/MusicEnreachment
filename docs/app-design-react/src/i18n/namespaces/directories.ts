import { createNamespace } from "../createNamespace";

export const directoryMessages = createNamespace({
	en: {
		title: "Server directory",
		help: "Directories on the MeloTrove server, not files on this device.",
		breadcrumbs: "Directory navigation",
		up: "Parent directory",
		editPath: "Edit path",
		server: "Server",
		absolutePath: "Absolute server path",
		go: "Go",
		notFound: "Path not found.",
		invalidPath:
			"This directory is not in the demo tree. Check the path or choose a directory from the list.",
		unreadable:
			"Directory unavailable. Demo response: the server cannot read this directory. Choose another path or go back.",
		empty:
			"This directory has no subdirectories. You can choose the current directory.",
		cancel: "Cancel",
		choose: "Choose directory",
	},
	ru: {
		title: "Папка на сервере",
		help: "Каталоги на сервере MeloTrove — не файлы устройства.",
		breadcrumbs: "Навигация по каталогам",
		up: "На уровень выше",
		editPath: "Редактировать путь",
		server: "Сервер",
		absolutePath: "Абсолютный серверный путь",
		go: "Перейти",
		notFound: "Путь не найден.",
		invalidPath:
			"В демонстрационном дереве такого каталога нет. Проверьте путь или выберите папку из списка.",
		unreadable:
			"Каталог недоступен. Демо-ответ: сервер не может прочитать этот каталог. Выберите другой путь или вернитесь назад.",
		empty:
			"В этой папке нет вложенных каталогов. Вы можете выбрать текущую папку.",
		cancel: "Отмена",
		choose: "Выбрать папку",
	},
});
