import type { ReactNode } from "react";
import { Button } from "react-aria-components";
import { muted, secondary } from "../local-release/localReleaseUi";

export function LocalCatalogEditorShell({
	eyebrow,
	title,
	backLabel,
	onBack,
	actions,
	children,
}: {
	eyebrow: string;
	title: string;
	backLabel: string;
	onBack: () => void;
	actions?: ReactNode;
	children: ReactNode;
}) {
	return (
		<section className="mx-auto grid w-full min-w-0 max-w-4xl gap-3 py-3 text-sm">
			<header className="flex min-w-0 flex-wrap items-start justify-between gap-2 border-b border-[var(--line)] pb-3">
				<div className="min-w-0">
					<p className={muted}>{eyebrow}</p>
					<h1 className="my-1 text-xl font-semibold">{title}</h1>
				</div>
				<div className="flex flex-wrap gap-2">
					{actions}
					<Button className={secondary} onPress={onBack}>
						{backLabel}
					</Button>
				</div>
			</header>
			{children}
		</section>
	);
}
