const english = {
  heading: "GitHub: release request limit (optional)",
  rateLimit:
    "Without a token, GitHub usually allows 60 requests per hour per shared IP address; with a token, usually 5000 per hour per user. Limits can change and may be shared with other applications.",
  recommendation: [
    "We recommend a fine-grained personal access token: select your resource owner, allow access only to public repositories, and add no permissions — reading public release metadata requires only the implicit Metadata: read, and the ",
    " and ",
    " permissions are not needed. MeloTrove does not verify and cannot confirm that the token has no additional permissions. A free GitHub account is sufficient: no paid subscription is required.",
  ],
  tokenStorage:
    "The token is stored only on the server, in the MeloTrove database, and is included in its backups. Protect backups as carefully as the token itself.",
  tokenTransport:
    "The token is sent only in GitHub API requests: it is not written to logs and is not transmitted when downloading files.",
  createToken: "Create a fine-grained token",
  expiryNote: "90 days is suggested as an example; you can change it",
  guide: "official GitHub guide",
  stepOpenSettings:
    "In GitHub, open Settings → Developer settings → Personal access tokens → Fine-grained tokens and click Generate new token.",
  stepChooseOwner:
    "Select your resource owner, Public repositories, and do not add extra permissions.",
  stepCopyToken: [
    "Copy the token (GitHub shows it only once) and paste it here. Do not add the ",
    " or ",
    " permissions: they are not needed.",
  ],
  tokenLabel: "Personal access token",
  verifyAndSave: "Verify and save",
  removeSaved: "Delete saved token",
  tokenSaved: "Token saved (the value is not displayed).",
  tokenMissing: "No token saved.",
  tokenVerified: "Token verified and saved.",
  requestInProgress: "Request in progress…",
  saveError: "Could not save the GitHub token.",
  deleteError: "Could not delete the GitHub token.",
  refreshError:
    "The token was saved, but the page state could not be refreshed.",
  httpError: (status: number) => `The server returned HTTP ${status}.`,
  redacted: "[redacted]",
  revokeHelp:
    "Deleting from MeloTrove does not revoke the token in GitHub. To revoke it, open",
  revokeLink: "GitHub token settings",
} as const;

const russian = {
  heading: "GitHub: лимит запросов к релизам (необязательно)",
  rateLimit:
    "Без токена GitHub обычно разрешает 60 запросов в час на общий IP-адрес; с токеном — обычно 5000 в час на пользователя. Лимиты могут меняться и быть общими для других приложений.",
  recommendation: [
    "Рекомендуем fine-grained personal access token: выберите свой resource owner, доступ только к публичным репозиториям и не добавляйте разрешения — для чтения метаданных публичных релизов достаточно неявного Metadata: read, а разрешения ",
    " и ",
    " не нужны. MeloTrove не проверяет и не может подтвердить, что у токена нет дополнительных прав. Достаточно бесплатного аккаунта GitHub: платная подписка не требуется.",
  ],
  tokenStorage:
    "Токен хранится только на сервере, в базе данных MeloTrove, и попадает в её резервные копии. Защищайте резервные копии так же, как сам токен.",
  tokenTransport:
    "Токен отправляется только в запросах к GitHub API: он не записывается в журналы и не передаётся при скачивании файлов.",
  createToken: "Создать fine-grained token",
  expiryNote: "срок 90 дней предложен как пример, его можно изменить",
  guide: "официальное руководство GitHub",
  stepOpenSettings:
    "В GitHub откройте Settings → Developer settings → Personal access tokens → Fine-grained tokens и нажмите Generate new token.",
  stepChooseOwner:
    "Выберите свой resource owner, Public repositories и не добавляйте дополнительные permissions.",
  stepCopyToken: [
    "Скопируйте токен (GitHub показывает его один раз) и вставьте сюда. Разрешения ",
    " и ",
    " не добавляйте: они не нужны.",
  ],
  tokenLabel: "Personal access token",
  verifyAndSave: "Проверить и сохранить",
  removeSaved: "Удалить сохранённый токен",
  tokenSaved: "Токен сохранён (значение не отображается).",
  tokenMissing: "Токен не сохранён.",
  tokenVerified: "Токен проверен и сохранён.",
  requestInProgress: "Выполняется запрос…",
  saveError: "Не удалось сохранить токен GitHub.",
  deleteError: "Не удалось удалить токен GitHub.",
  refreshError: "Токен сохранён, но не удалось обновить состояние страницы.",
  httpError: (status: number) => `Сервер вернул HTTP ${status}.`,
  redacted: "[скрыто]",
  revokeHelp:
    "Удаление из MeloTrove не отзывает токен в GitHub. Для отзыва откройте",
  revokeLink: "настройки токенов GitHub",
} as const;

export const toolMessages = { en: english, ru: russian } as const;

export type ToolMessages = (typeof toolMessages)[keyof typeof toolMessages];
