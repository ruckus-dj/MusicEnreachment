import { createNamespace } from "../i18n/createNamespace";

export const shellStartupMessages = createNamespace({
  en: {
    home: "Home",
    loadingServer: "Loading server status…",
    loadingSources: "Loading sources…",
    loadingIncoming: "Opening incoming…",
    loadError: "Could not load setup.",
    sourcesLoadError: "Could not load the source list.",
    retry: "Retry loading",
    diagnose: "Platform diagnostics",
    unsupportedPlatform: "This platform is not supported.",
    refreshDiagnostics: "Refresh diagnostics",
    addSourceTitle: "Add a source to get started",
    addSourceDescription:
      "Register a music folder in Sources to start working with your library. You can do this now or later.",
    addSource: "Add source",
  },
  ru: {
    home: "Главная",
    loadingServer: "Загрузка состояния сервера…",
    loadingSources: "Загрузка источников…",
    loadingIncoming: "Переход к входящим…",
    loadError: "Не удалось загрузить Setup.",
    sourcesLoadError: "Не удалось загрузить список источников.",
    retry: "Повторить загрузку",
    diagnose: "Диагностика платформы",
    unsupportedPlatform: "Платформа не поддерживается.",
    refreshDiagnostics: "Обновить диагностику",
    addSourceTitle: "Для начала работы необходимо добавить источник",
    addSourceDescription:
      "Зарегистрируйте каталог с музыкой в разделе «Источники», чтобы начать работу с медиатекой. Это можно сделать сейчас или позже.",
    addSource: "Добавить источник",
  },
});
