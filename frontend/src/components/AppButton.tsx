import { Button, type ButtonProps } from "react-aria-components";

const buttonClassName =
  "react-aria-Button ui-button inline-flex min-h-[30px] pointer-coarse:min-h-[40px] cursor-pointer items-center justify-center gap-1.5 rounded-[4px] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-[5px] text-[var(--ink)] no-underline motion-safe:transition-[background-color,border-color,color] motion-safe:duration-[120ms] motion-reduce:transition-none hover:border-[var(--accent)] hover:bg-[var(--raised)] active:bg-[var(--accent-soft)] aria-disabled:cursor-not-allowed aria-disabled:border-[var(--line)] aria-disabled:bg-[var(--raised)] aria-disabled:text-[var(--muted)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-2";

export function AppButton({ className, ...props }: ButtonProps) {
  return (
    <Button
      {...props}
      className={
        typeof className === "function"
          ? (values) => `${buttonClassName} ${className(values)}`
          : `${buttonClassName}${className ? ` ${className}` : ""}`
      }
    />
  );
}
