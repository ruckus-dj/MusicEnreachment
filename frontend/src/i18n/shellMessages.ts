import type { Locale } from "./locale";

const english = {
  skip: "Skip to content",
  navigation: "Main navigation",
  sections: "Application sections",
  home: "Home",
  incoming: "Incoming",
  library: "Library",
  ownCatalog: "Own catalog",
  sources: "Sources",
  operations: "Operations",
  settings: "Settings",
  brandHome: "MeloTrove — home",
  loadingServer: "Loading server status…",
  loadingSources: "Loading sources…",
  loadingIncoming: "Opening incoming…",
  loadError: "Could not load setup.",
  sourcesLoadError: "Could not load the source list.",
  retry: "Retry loading",
  diagnose: "Platform diagnostics",
  unsupportedPlatform: "This platform is not supported.",
  refreshDiagnostics: "Refresh diagnostics",
  homeTitle: "Home",
  addSourceTitle: "Add a source to get started",
  addSourceDescription:
    "Register a music folder in Sources to start working with your library. You can do this now or later.",
  addSource: "Add source",
} as const;

export type ShellMessageKey = keyof typeof english;

const russian: Record<ShellMessageKey, string> = {
  skip: "Перейти к содержимому",
  navigation: "Основная навигация",
  sections: "Разделы приложения",
  home: "Главная",
  incoming: "Входящие",
  library: "Медиатека",
  ownCatalog: "Собственный каталог",
  sources: "Источники",
  operations: "Операции",
  settings: "Настройки",
  brandHome: "MeloTrove — главная",
  loadingServer: "Загрузка состояния сервера…",
  loadingSources: "Загрузка источников…",
  loadingIncoming: "Переход к входящим…",
  loadError: "Не удалось загрузить Setup.",
  sourcesLoadError: "Не удалось загрузить список источников.",
  retry: "Повторить загрузку",
  diagnose: "Диагностика платформы",
  unsupportedPlatform: "Платформа не поддерживается.",
  refreshDiagnostics: "Обновить диагностику",
  homeTitle: "Главная",
  addSourceTitle: "Для начала работы необходимо добавить источник",
  addSourceDescription:
    "Зарегистрируйте каталог с музыкой в разделе «Источники», чтобы начать работу с медиатекой. Это можно сделать сейчас или позже.",
  addSource: "Добавить источник",
};

export const shellMessages: Record<Locale, Record<ShellMessageKey, string>> = {
  en: english,
  ru: russian,
};
