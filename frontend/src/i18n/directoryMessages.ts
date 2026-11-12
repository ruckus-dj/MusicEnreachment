import type { Locale } from "./locale";

const english = {
  title: "Server folder",
  help: "Choose a folder the MeloTrove server can access.",
  close: "Close",
  navigation: "Folder path",
  up: "Parent folder",
  editPath: "Edit path",
  enterPath: "Enter path",
  pathLabel: "Folder path",
  folders: "Folders",
  serverRoots: "Server roots",
  empty: "This folder has no subfolders.",
  loading: "Loading folders…",
  error: "Could not read the server directory.",
  retry: "Retry",
  cancel: "Cancel",
  choose: "Choose folder",
} as const;

export type DirectoryMessageKey = keyof typeof english;

const russian: Record<DirectoryMessageKey, string> = {
  title: "Папка на сервере",
  help: "Выберите папку, доступную серверу MeloTrove.",
  close: "Закрыть",
  navigation: "Путь к папке",
  up: "На уровень выше",
  editPath: "Изменить путь",
  enterPath: "Ввести путь",
  pathLabel: "Путь к папке",
  folders: "Папки",
  serverRoots: "Корни сервера",
  empty: "В этой папке нет подпапок.",
  loading: "Загрузка папок…",
  error: "Не удалось прочитать каталог сервера.",
  retry: "Повторить",
  cancel: "Отмена",
  choose: "Выбрать папку",
};

export const directoryMessages: Record<
  Locale,
  Record<DirectoryMessageKey, string>
> = {
  en: english,
  ru: russian,
};
