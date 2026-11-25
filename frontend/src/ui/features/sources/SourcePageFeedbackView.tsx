import { AppButton } from "../../components/AppButton";

const alert = "rounded bg-[var(--error-soft)] p-2 text-[var(--error)]";

export function SourcePageFeedbackView({
  status,
  listError,
  scanErrors,
  discoveryError,
  retryLabel,
}: {
  status?: string;
  listError?: { message: string; retry: () => void };
  scanErrors: readonly { id: string; name: string; message: string }[];
  discoveryError?: { message: string; retry: () => void };
  retryLabel: string;
}) {
  return (
    <>
      {status && (
        <p role="status" className="m-0 text-xs text-[var(--muted)]">
          {status}
        </p>
      )}
      {listError && (
        <div
          role="alert"
          className={`my-2 flex flex-wrap items-center gap-2 ${alert}`}
        >
          <p className="m-0">{listError.message}</p>
          <AppButton onPress={listError.retry}>{retryLabel}</AppButton>
        </div>
      )}
      {scanErrors.length > 0 && (
        <div role="alert" className={`my-2 grid gap-1 ${alert}`}>
          {scanErrors.map(({ id, name, message }) => (
            <p key={id} className="m-0">
              {name}: {message}
            </p>
          ))}
        </div>
      )}
      {discoveryError && (
        <div className="flex flex-wrap items-center gap-2">
          <p role="alert" className={`m-0 ${alert}`}>
            {discoveryError.message}
          </p>
          <AppButton onPress={discoveryError.retry}>{retryLabel}</AppButton>
        </div>
      )}
    </>
  );
}

export function MissingSourceView({
  title,
  id,
  back,
}: {
  title: string;
  id: string;
  back: { label: string; href: string };
}) {
  return (
    <section className="grid min-w-0 gap-2">
      <header>
        <h1 className="text-xl font-semibold">{title}</h1>
        <p className="text-[var(--muted)]">
          <code className="wrap-anywhere">{id}</code>
        </p>
      </header>
      <p>
        <a href={back.href} className="text-[var(--accent)] hover:underline">
          {back.label}
        </a>
      </p>
    </section>
  );
}

export function SourceNoticeView({ message }: { message: string }) {
  return (
    <p
      role="status"
      className="rounded border border-[var(--line)] bg-[var(--raised)] p-2 text-sm"
    >
      {message}
    </p>
  );
}

export function SourceWorkDirectoryView({
  label,
  path,
  link,
  inline,
}: {
  label: string;
  path?: string;
  link: { label: string; href: string };
  inline?: boolean;
}) {
  const content = (
    <>
      {label}: {path || "—"} ·{" "}
      <a href={link.href} className="text-[var(--accent)] hover:underline">
        {link.label}
      </a>
    </>
  );
  return inline ? content : <p>{content}</p>;
}
