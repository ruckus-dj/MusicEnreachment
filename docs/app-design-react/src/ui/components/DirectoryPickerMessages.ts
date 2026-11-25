export type DirectoryPickerLocale = "en" | "ru";

const english = {
  title: "Server folder",
  help: "Choose a folder the MeloTrove server can access.",
  close: "Close",
  navigation: "Folder path",
  up: "Parent folder",
  editPath: "Edit path",
  enterPath: "Enter path",
  pathLabel: "Folder path",
  go: "Go",
  folders: "Folders",
  serverRoots: "Server roots",
  empty: "This folder has no subfolders.",
  loading: "Loading folders…",
  error: "Could not read the server directory.",
  notFound: "Folder not found. Check the folder path.",
  unreadable: "The server cannot read this folder.",
  retry: "Retry",
  cancel: "Cancel",
  choose: "Choose folder",
} as const;

export type DirectoryPickerCopy = {
  [Key in keyof typeof english]: string;
};

const russian: DirectoryPickerCopy = {
  title: "Папка на сервере",
  help: "Выберите папку, доступную серверу MeloTrove.",
  close: "Закрыть",
  navigation: "Путь к папке",
  up: "На уровень выше",
  editPath: "Изменить путь",
  enterPath: "Ввести путь",
  pathLabel: "Путь к папке",
  go: "Перейти",
  folders: "Папки",
  serverRoots: "Корни сервера",
  empty: "В этой папке нет подпапок.",
  loading: "Загрузка папок…",
  error: "Не удалось прочитать каталог сервера.",
  notFound: "Папка не найдена. Проверьте путь к папке.",
  unreadable: "Сервер не может прочитать эту папку.",
  retry: "Повторить",
  cancel: "Отмена",
  choose: "Выбрать папку",
};

export const directoryPickerMessages: Record<
  DirectoryPickerLocale,
  DirectoryPickerCopy
> = {
  en: english,
  ru: russian,
};
