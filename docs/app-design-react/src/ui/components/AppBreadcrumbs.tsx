import { Breadcrumb, Breadcrumbs, Link } from "react-aria-components";
import { AppIcon } from "./AppIcon";

export type AppBreadcrumbItem = {
  id: string;
  label: string;
  href?: string;
};

export function AppBreadcrumbs({
  items,
  label,
}: {
  items: readonly AppBreadcrumbItem[];
  label: string;
}) {
  return (
    <Breadcrumbs aria-label={label} className="min-w-0">
      {items.map((item, index) => {
        const current = index === items.length - 1;
        return (
          <Breadcrumb
            key={item.id}
            className="inline-flex min-w-0 items-center gap-1.5 text-sm text-[var(--muted)]"
          >
            {item.href ? (
              <Link
                href={item.href}
                className="rounded-sm text-[var(--muted)] underline-offset-2 hover:text-[var(--ink)] hover:underline focus-visible:outline-2 focus-visible:outline-[var(--accent)]"
              >
                {item.label}
              </Link>
            ) : (
              <span
                aria-current={current ? "page" : undefined}
                className={
                  current ? "font-medium text-[var(--ink)]" : undefined
                }
              >
                {item.label}
              </span>
            )}
            {current ? null : (
              <AppIcon
                name="chevronRight"
                className="size-3.5 shrink-0 text-[var(--control)]"
              />
            )}
          </Breadcrumb>
        );
      })}
    </Breadcrumbs>
  );
}
