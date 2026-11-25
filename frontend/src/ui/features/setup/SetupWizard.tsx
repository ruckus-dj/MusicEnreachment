import { type ReactNode, useEffect, useRef } from "react";
import { AppButton } from "../../components/AppButton";
import { SetupFeedbackView } from "./SetupFeedbackView";

const pageClassName = "grid gap-[14px]";
const headerClassName =
  "grid gap-[5px] mb-[18px] pb-[14px] border-b border-[var(--line)] max-[600px]:flex max-[600px]:flex-wrap max-[600px]:items-center max-[600px]:justify-between";
const titleClassName =
  "m-0 text-[clamp(21px,2.5vw,28px)] leading-[1.18] [text-wrap:balance]";
const descriptionClassName = "m-0 max-w-[75ch] text-[var(--muted)]";
const stepperClassName =
  "m-0 grid list-none gap-px overflow-hidden rounded-[7px] border border-[var(--line)] bg-[var(--line)] p-0 [&>li]:flex [&>li]:items-center [&>li]:gap-2 [&>li]:bg-[var(--raised)] [&>li]:p-[10px] [&>li]:text-xs [&>li]:text-[var(--muted)] [&>li[aria-current=step]]:bg-[var(--accent-soft)] [&>li[aria-current=step]]:font-bold [&>li[aria-current=step]]:text-[var(--accent)]";
const stepIndexClassName =
  "grid size-6 flex-none place-items-center rounded-full border border-[var(--line)] bg-[var(--surface)]";
const panelClassName =
  "min-w-0 mb-[10px] rounded-[8px] border border-[var(--line)] bg-[var(--surface)] p-[10px] [&>h2]:mb-2 [&>h2]:text-sm [&>h2]:font-semibold [&>h3]:mb-2 [&>h3]:text-sm [&>h3]:font-semibold [&>p]:my-2";
const bodyClassName =
  "grid min-w-0 gap-3 rounded-md border border-[var(--line)] bg-[var(--surface)] p-[14px]";
const helpClassName = "my-2 max-w-[75ch] text-[var(--muted)]";
const footerClassName =
  "flex justify-between gap-2 border-t border-[var(--line)] pt-3";

type Locale = "en" | "ru";
export const setupSteps = [
  "directories",
  "tools",
  "publication",
  "summary",
] as const;
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
                {index < step ? "✓" : index + 1}
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
          className="mb-2 text-sm font-semibold"
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
            <AppButton isDisabled={nextDisabled} onPress={onNext}>
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
