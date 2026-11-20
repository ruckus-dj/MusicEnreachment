import { AppButton } from "../../components/AppButton";
import {
  type SetupToolRelease,
  SetupToolReleasePicker,
} from "./SetupToolReleasePicker";

export type SetupToolCardsLocale = "en" | "ru";

export type SetupToolKind = "ffmpeg" | "fpcalc";

export type SetupToolReadyVersion = {
  id: string;
  version: string;
  active: boolean;
};

export type SetupToolAction = {
  /** The live setup checks before installing; the prototype simulates install. */
  kind: "checkInstall" | "install";
  disabled: boolean;
  onPress: () => void;
};

export type SetupToolVerification =
  | { state: "available" }
  | { state: "verified"; release: string };

export type SetupToolCard =
  | {
      kind: SetupToolKind;
      state: "ready";
      /** Exact live ready installations; a ready package must offer no download. */
      versions: readonly SetupToolReadyVersion[];
    }
  | {
      kind: SetupToolKind;
      state: "install";
      /** Catalog notice supplied by the server. */
      notice?: string;
      releases: readonly SetupToolRelease[];
      selected: string | null;
      pending?: boolean;
      onSelect: (identity: string | null) => void;
      action: SetupToolAction;
      /** Prototype verification result; omit when the host has no such state. */
      verification?: SetupToolVerification;
      /** Executable names to verify; omit when the host does not present them. */
      executables?: readonly string[];
    };

export type SetupToolCardsViewProps = {
  locale: SetupToolCardsLocale;
  tools: readonly SetupToolCard[];
  /** Package that still needs verification; omit when every package is ready. */
  nextTool?: SetupToolKind;
};

const messages = {
  en: {
    package: { ffmpeg: "FFmpeg package", fpcalc: "fpcalc" },
    toolName: { ffmpeg: "FFmpeg", fpcalc: "fpcalc" },
    active: "Active",
    ready: "Ready",
    status: "Status",
    available: "Available",
    verified: "Installed and verified",
    nextTool: "Next tool",
    checkInstall: "Check {kind} installation",
    install: "Install",
  },
  ru: {
    package: { ffmpeg: "Пакет FFmpeg", fpcalc: "fpcalc" },
    toolName: { ffmpeg: "FFmpeg", fpcalc: "fpcalc" },
    active: "Активна",
    ready: "Готова",
    status: "Состояние",
    available: "Доступна",
    verified: "Установлено и проверено",
    nextTool: "Следующий инструмент",
    checkInstall: "Проверить установку {kind}",
    install: "Установить",
  },
} as const;

const cardsClass = "grid gap-2";
const cardClass = "grid gap-2 border-b border-[var(--line)] pb-3";
const headingClass = "m-0 font-semibold";
const paragraphClass = "m-0";
const noteClass = "m-0 text-xs text-[var(--muted)]";

function actionText(
  copy: (typeof messages)[SetupToolCardsLocale],
  tool: SetupToolCard & { state: "install" },
) {
  return tool.action.kind === "checkInstall"
    ? copy.checkInstall.replace("{kind}", tool.kind)
    : copy.install;
}

export function SetupToolCardsView({
  locale,
  tools,
  nextTool,
}: SetupToolCardsViewProps) {
  const copy = messages[locale];
  return (
    <div className={cardsClass}>
      {tools.map((tool) => {
        const packageName = copy.package[tool.kind];
        return (
          <section
            key={tool.kind}
            className={cardClass}
            aria-label={packageName}
          >
            <h3 className={headingClass}>{packageName}</h3>
            {tool.state === "ready" ? (
              tool.versions.map((version) => (
                <p key={version.id} role="status" className={paragraphClass}>
                  {version.version}: {version.active ? copy.active : copy.ready}
                </p>
              ))
            ) : (
              <>
                {tool.notice && <p className={paragraphClass}>{tool.notice}</p>}
                <SetupToolReleasePicker
                  locale={locale}
                  kind={tool.kind}
                  releases={tool.releases}
                  selected={tool.selected}
                  pending={tool.pending}
                  onChange={tool.onSelect}
                />
                {tool.verification && (
                  <p className={paragraphClass}>
                    {copy.status}:{" "}
                    {tool.verification.state === "verified"
                      ? `${copy.verified} · ${tool.verification.release}`
                      : copy.available}
                  </p>
                )}
                {tool.executables && tool.executables.length > 0 && (
                  <p className={noteClass}>{tool.executables.join(", ")}</p>
                )}
                <AppButton
                  className="justify-self-start"
                  isDisabled={tool.action.disabled}
                  onPress={tool.action.onPress}
                >
                  {actionText(copy, tool)}
                </AppButton>
              </>
            )}
          </section>
        );
      })}
      {nextTool && (
        <p role="status" className={paragraphClass}>
          {copy.nextTool}: {copy.toolName[nextTool]}
        </p>
      )}
    </div>
  );
}
