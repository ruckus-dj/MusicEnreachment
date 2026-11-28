import type { GitHubTokenSimulatedCopy } from "../../../ui/features/tools/GitHubTokenFormView";
import type { GitHubTokenLocale } from "../../../ui/features/tools/toolMessages";

export const toolDemoMessages = {
  en: {
    warning:
      "Prototype: use a placeholder, not a real token. Nothing is sent to GitHub or stored.",
    saved: "Simulated token is configured.",
    verified: "Verification simulated; no GitHub request was made.",
  },
  ru: {
    warning:
      "Макет: введите условное значение, а не настоящий токен. Оно не сохраняется и не отправляется в GitHub.",
    saved: "Сохранение токена сымитировано.",
    verified: "Проверка сымитирована, запрос в GitHub не отправлялся.",
  },
} as const satisfies Record<GitHubTokenLocale, GitHubTokenSimulatedCopy>;
