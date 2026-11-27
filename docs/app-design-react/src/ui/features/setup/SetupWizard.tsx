import { type ReactNode, useEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";
import { AppIcon } from "../../components/AppIcon";
import { SetupFeedbackView } from "./SetupFeedbackView";
import { setupSteps } from "./setupStepRoute";

export { setupSteps } from "./setupStepRoute";

const pageClassName = "mx-auto grid w-full max-w-[1040px] gap-6";
const headerClassName = "grid gap-1.5 border-b border-[var(--line)] pb-5";
const titleClassName =
  "m-0 text-[26px]/[1.2] font-semibold tracking-[-.02em] [text-wrap:balance]";
const descriptionClassName = "m-0 max-w-[75ch] text-sm text-[var(--muted)]";
const stepperClassName =
  "m-0 grid list-none gap-3 p-0 [&>li]:flex [&>li]:items-center [&>li]:gap-2.5 [&>li]:rounded-lg [&>li]:border [&>li]:border-[var(--line)] [&>li]:bg-[var(--surface)] [&>li]:px-3.5 [&>li]:py-2.5 [&>li]:text-sm [&>li]:text-[var(--muted)] [&>li[aria-current=step]]:border-[var(--accent)] [&>li[aria-current=step]]:bg-[var(--accent-soft)] [&>li[aria-current=step]]:font-medium [&>li[aria-current=step]]:text-[var(--accent-strong)]";
const stepIndexClassName =
  "grid size-6 flex-none place-items-center rounded-full border border-[var(--line)] bg-[var(--surface)] text-xs tabular-nums";
const panelClassName =
  "min-w-0 rounded-xl border border-[var(--line)] bg-[var(--surface)] p-5 [&>h2]:mb-3 [&>h2]:text-lg [&>h2]:font-semibold [&>h3]:mb-2 [&>h3]:text-sm [&>h3]:font-semibold [&>p]:my-2 [&>p]:text-sm";
const bodyClassName = "grid min-w-0 gap-4";
const helpClassName =
  "my-1 max-w-[75ch] rounded-lg bg-[var(--raised)] p-4 text-sm";
const footerClassName =
  "flex justify-between gap-2 border-t border-[var(--line)] pt-4";

type Locale = "en" | "ru";
const messages = {
  en: {
    title: "First run",
    subtitle: "Library configuration and connected services",
    stepsLabel: "Setup steps",
    step: {
      directories: "Directories",
      tools: "Tools",
      publication: "Publication",
      summary: "Summary",
    },
    back: "Back",
    continue: "Continue",
    finish: "Finish setup",
    pending: "Request in progress…",
    aboutStep: "About this step",
    important: "Important to know",
    server: "Server",
    platformSupported: "This platform is supported.",
  },
  ru: {
    title: "Первый запуск",
    subtitle: "Параметры библиотеки и её подключений",
    stepsLabel: "Шаги настройки",
    step: {
      directories: "Каталоги",
      tools: "Инструменты",
      publication: "Публикация",
      summary: "Итог",
    },
    back: "Назад",
    continue: "Продолжить",
    finish: "Завершить настройку",
    pending: "Выполняется запрос…",
    aboutStep: "О текущем шаге",
    important: "Важно знать",
    server: "Сервер",
    platformSupported: "Платформа поддерживается.",
  },
} as const;

export interface SetupWizardProps {
  locale: Locale;
  step: number;
  help?: string;
  children: ReactNode;
  error?: string;
  notice?: string;
  pending?: boolean;
  onBack: () => void;
  onNext: () => void;
  nextDisabled?: boolean;
  backDisabled?: boolean;
}

export function SetupWizard({
  locale,
  step,
  help,
  children,
  error,
  notice,
  pending,
  onBack,
  onNext,
  nextDisabled,
  backDisabled,
}: SetupWizardProps) {
  const steps = setupSteps;
  const copy = messages[locale];
  const headingRef = useRef<HTMLHeadingElement>(null);
  const alertRef = useRef<HTMLParagraphElement>(null);
  const stepperColumnsClassName =
    steps.length > 4
      ? "grid-cols-6 max-[900px]:grid-cols-3 max-[600px]:grid-cols-2"
      : "grid-cols-4 max-[600px]:grid-cols-2";

  useEffect(() => {
    if (step >= 0) headingRef.current?.focus();
  }, [step]);
  useEffect(() => {
    if (error) alertRef.current?.focus();
  }, [error]);

  return (
    <section className={pageClassName} aria-labelledby="setup-title">
      <header className={headerClassName}>
        <h1 className={titleClassName} id="setup-title">
          {copy.title}
        </h1>
        <p className={descriptionClassName}>{copy.subtitle}</p>
      </header>
      <nav aria-label={copy.stepsLabel}>
        <ol className={`${stepperClassName} ${stepperColumnsClassName}`}>
          {steps.map((name, index) => (
            <li key={name} aria-current={index === step ? "step" : undefined}>
              <span className={stepIndexClassName}>
                {index < step ? (
                  <AppIcon
                    name="check"
                    className="size-3.5 text-[var(--accent-strong)]"
                  />
                ) : (
                  index + 1
                )}
              </span>
              <span>{copy.step[name]}</span>
            </li>
          ))}
        </ol>
      </nav>
      <section className={panelClassName} aria-labelledby="setup-step-title">
        <h2
          ref={headingRef}
          id="setup-step-title"
          tabIndex={-1}
          className="mb-3 text-lg font-semibold"
        >
          {copy.step[steps[step]]}
        </h2>
        {help && (
          <aside className={helpClassName} aria-label={copy.aboutStep}>
            <h3 className="m-0 mb-1 text-sm font-semibold text-[var(--ink)]">
              {copy.important}
            </h3>
            <p className="m-0">{help}</p>
          </aside>
        )}
        <div className={bodyClassName}>
          {children}
          <SetupFeedbackView
            error={error}
            errorRef={alertRef}
            notice={notice}
            pending={pending ? copy.pending : null}
          />
          <div className={footerClassName}>
            {step > 0 && (
              <AppButton isDisabled={backDisabled} onPress={onBack}>
                {copy.back}
              </AppButton>
            )}
            <AppButton
              variant="primary"
              isDisabled={nextDisabled}
              onPress={onNext}
            >
              {step === steps.length - 1 ? copy.finish : copy.continue}
            </AppButton>
          </div>
        </div>
      </section>
    </section>
  );
}

export interface SetupPlatformStepProps {
  locale: Locale;
  goos: string;
  goarch: string;
  supported: boolean;
  reason?: string;
}

export function SetupPlatformStep({
  locale,
  goos,
  goarch,
  supported,
  reason,
}: SetupPlatformStepProps) {
  const copy = messages[locale];
  return (
    <p className="m-0">
      {copy.server}: {goos}/{goarch}.{" "}
      {supported ? copy.platformSupported : reason}
    </p>
  );
}
