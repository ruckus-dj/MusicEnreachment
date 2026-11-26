import { Button, type ButtonProps } from "react-aria-components";
import { twMerge } from "tailwind-merge";

export type AppButtonVariant =
  | "secondary"
  | "primary"
  | "ghost"
  | "danger"
  | "icon";

const base =
  "react-aria-Button ui-button inline-flex min-h-9 pointer-coarse:min-h-10 cursor-pointer items-center justify-center gap-2 rounded-[var(--control-radius)] border px-3 text-sm font-medium no-underline whitespace-nowrap motion-safe:transition-[background-color,border-color,color,box-shadow] motion-safe:duration-150 motion-reduce:transition-none disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] aria-disabled:cursor-not-allowed aria-disabled:border-[var(--line)] aria-disabled:bg-[var(--raised)] aria-disabled:text-[var(--muted)] [&_svg]:size-4 [&_svg]:shrink-0";

const variants: Record<AppButtonVariant, string> = {
  secondary:
    "border-[var(--line)] bg-[var(--surface)] text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] data-[pressed]:bg-[var(--raised)]",
  primary:
    "border-transparent bg-[var(--button)] text-[var(--button-text)] hover:border-transparent hover:bg-[var(--button-hover)] data-[pressed]:bg-[var(--button-hover)]",
  ghost:
    "border-transparent bg-transparent text-[var(--ink)] hover:bg-[var(--raised)] data-[pressed]:bg-[var(--raised)]",
  danger:
    "border-[color-mix(in_srgb,var(--error)_35%,var(--line))] bg-[var(--surface)] text-[var(--error)] hover:border-[var(--error)] hover:bg-[var(--error-soft)]",
  icon: "size-9 min-w-9 border-transparent bg-transparent px-0 text-[var(--muted)] hover:bg-[var(--raised)] hover:text-[var(--ink)]",
};

export type AppButtonProps = ButtonProps & {
  variant?: AppButtonVariant;
  size?: "default" | "sm";
};

export function AppButton({
  className,
  variant = "secondary",
  size = "default",
  ...props
}: AppButtonProps) {
  const classes = twMerge(
    base,
    variants[variant],
    size === "sm" && "min-h-8 px-2.5 text-sm",
  );
  return (
    <Button
      {...props}
      className={
        typeof className === "function"
          ? (values) => twMerge(classes, className(values))
          : twMerge(classes, className)
      }
    />
  );
}
