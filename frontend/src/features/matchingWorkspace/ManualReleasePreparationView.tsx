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
      className={`inline-flex min-h-[30px] cursor-pointer items-center justify-center gap-1.5 rounded-[var(--control-radius,4px)] border border-[var(--control)] bg-[var(--surface)] px-[9px] py-[5px] text-[var(--ink)] hover:border-[var(--accent)] hover:bg-[var(--raised)] active:bg-[var(--accent-soft)] disabled:cursor-not-allowed disabled:border-[var(--line)] disabled:bg-[var(--raised)] disabled:text-[var(--muted)] focus-visible:outline-2 focus-visible:outline-[var(--focus)] focus-visible:outline-offset-2 pointer-coarse:min-h-[40px] ${className ?? ""}`}
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
      className="rounded border border-[var(--line)] bg-[var(--surface)] p-4"
      aria-labelledby="manual-release-title"
    >
      <h2 id="manual-release-title" className="font-semibold">
        {message("selectReleaseTitle")}
      </h2>
      <p className="mt-1 text-sm">{message("selectReleaseDescription")}</p>
      <div className="mt-3 flex flex-wrap gap-2">
        <label className="sr-only" htmlFor="manual-release-value">
          {message("releaseMBIDOrURL")}
        </label>
        <input
          id="manual-release-value"
          className="min-w-0 basis-64 flex-1 rounded border border-[var(--control)] bg-[var(--raised)] p-2 text-[var(--ink)] max-[600px]:min-h-10"
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
