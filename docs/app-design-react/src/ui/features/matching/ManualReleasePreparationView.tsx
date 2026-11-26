import { Button as AriaButton, type ButtonProps } from "react-aria-components";
import {
  type ManualReleasePreparationMessageKey,
  type ManualReleasePreparationViewProps,
  manualReleasePreparationMessage,
} from "./manualReleasePreparationContract";

function Button({ className, ...props }: ButtonProps) {
  return (
    <AriaButton
      {...props}
      className={`inline-flex h-9 cursor-pointer items-center justify-center gap-1.5 rounded-lg border border-[var(--line)] bg-[var(--surface)] px-3 text-sm font-medium text-[var(--ink)] hover:border-[var(--control)] hover:bg-[var(--raised)] active:bg-[var(--raised)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0 pointer-coarse:h-10 ${className ?? ""}`}
    />
  );
}

export function ManualReleasePreparationView({
  locale,
  value,
  disabled = false,
  pending = false,
  prepared = false,
  prepareDisabled = false,
  errors = [],
  onValueChange,
  onPrepare,
}: ManualReleasePreparationViewProps) {
  const message = (
    key: ManualReleasePreparationMessageKey,
    values?: Record<string, string | number>,
  ) => manualReleasePreparationMessage(locale, key, values);
  const canPrepare = Boolean(
    value.trim() && !pending && !disabled && !prepareDisabled,
  );

  return (
    <section
      className="grid min-w-0 content-start gap-2"
      aria-labelledby="manual-release-title"
    >
      <h2 id="manual-release-title" className="m-0 text-sm font-semibold">
        {message("selectReleaseTitle")}
      </h2>
      <p className="m-0 text-sm text-[var(--muted)]">
        {message("selectReleaseDescription")}
      </p>
      <div className="flex flex-wrap gap-2">
        <label className="sr-only" htmlFor="manual-release-value">
          {message("releaseMBIDOrURL")}
        </label>
        <input
          id="manual-release-value"
          className="h-9 min-w-0 flex-1 rounded-lg border border-[var(--control)] bg-[var(--surface)] px-3 text-sm text-[var(--ink)] placeholder:text-[var(--muted)] hover:border-[var(--ink)] focus-visible:outline-2 focus-visible:outline-[var(--accent)] focus-visible:outline-offset-0 max-[600px]:h-10"
          value={value}
          disabled={pending || disabled}
          onChange={(event) => onValueChange(event.target.value)}
        />
        <Button onPress={onPrepare} isDisabled={!canPrepare}>
          {message("prepareRelease")}
        </Button>
      </div>
      {pending ? (
        <p role="status" className="mt-2">
          {message("preparingRelease")}
        </p>
      ) : null}
      {errors.map((error) =>
        error.onRetry ? (
          <div
            role="alert"
            className="mt-2 space-y-2 text-[var(--error)]"
            key={error.id}
          >
            <p>{error.message}</p>
            <Button onPress={error.onRetry}>
              {message("refreshOperation")}
            </Button>
          </div>
        ) : (
          <p role="alert" className="mt-2 text-[var(--error)]" key={error.id}>
            {error.message}
          </p>
        ),
      )}
      {prepared ? (
        <p role="status" className="mt-2">
          {message("releasePrepared")}
        </p>
      ) : null}
    </section>
  );
}
