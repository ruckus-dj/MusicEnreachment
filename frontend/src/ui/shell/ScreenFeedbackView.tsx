import type { ReactNode } from "react";

export function ScreenFeedbackView({
  title,
  message,
  link,
}: {
  title: string;
  message?: string;
  link: { label: string; href: string };
}) {
  return (
    <section className="grid gap-2">
      <h1 className="text-xl font-semibold">{title}</h1>
      {message && <p>{message}</p>}
      <p>
        <a className="text-[var(--accent)] hover:underline" href={link.href}>
          {link.label}
        </a>
      </p>
    </section>
  );
}

export function ScreenStatus({ children }: { children: ReactNode }) {
  return <p role="status">{children}</p>;
}
