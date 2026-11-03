export type ManagedToolPackage = "ffmpeg" | "fpcalc";
export type InstallationStatus = "active" | "installed" | "available";

export interface DemoInstallation {
	id: string;
	package: ManagedToolPackage;
	release: string;
	status: InstallationStatus;
	occupied: boolean;
}

export const initialDemoInstallations: DemoInstallation[] = [
	{
		id: "ffmpeg-8.0",
		package: "ffmpeg",
		release: "8.0",
		status: "active",
		occupied: false,
	},
	{
		id: "fpcalc-1.5.1",
		package: "fpcalc",
		release: "1.5.1",
		status: "active",
		occupied: false,
	},
	{
		id: "ffmpeg-7.1",
		package: "ffmpeg",
		release: "7.1",
		status: "installed",
		occupied: false,
	},
];

export const availableToolReleases: Record<ManagedToolPackage, string[]> = {
	ffmpeg: ["8.0.1", "8.0"],
	fpcalc: ["1.5.1"],
};

export function activateDemoInstallation(
	items: DemoInstallation[],
	id: string,
): DemoInstallation[] {
	const target = items.find(
		(item) => item.id === id && item.status === "installed" && !item.occupied,
	);
	if (!target) return items;
	return items.map((item) =>
		item.package !== target.package
			? item
			: { ...item, status: item.id === id ? "active" : "installed" },
	);
}

export function canDeleteDemoInstallation(item: DemoInstallation): boolean {
	return item.status === "installed" && !item.occupied;
}

export function createDemoInstallation(
	items: DemoInstallation[],
	packageKind: ManagedToolPackage,
	release: string,
): DemoInstallation[] {
	const id = `${packageKind}-${release}`;
	if (items.some((item) => item.id === id)) return items;
	return [
		...items,
		{ id, package: packageKind, release, status: "installed", occupied: false },
	];
}

export function activateOrCreateDemoInstallation(
	items: DemoInstallation[],
	packageKind: ManagedToolPackage,
	release: string,
): DemoInstallation[] {
	const id = `${packageKind}-${release}`;
	const hasTarget = items.some((item) => item.id === id);
	const next: DemoInstallation[] = hasTarget
		? items.map((item) =>
				item.package !== packageKind
					? item
					: {
							...item,
							status: item.id === id ? "active" : "installed",
						},
			)
		: [
				...items.map((item) =>
					item.package === packageKind && item.status === "active"
						? { ...item, status: "installed" as const }
						: item,
				),
				{
					id,
					package: packageKind,
					release,
					status: "active",
					occupied: false,
				},
			];
	return next;
}

export function isAbsoluteDemoServerPath(value: string): boolean {
	return (
		value.startsWith("/") ||
		/^[a-z]:[\\/]/i.test(value) ||
		/^\\\\[^\\]+\\[^\\]+/.test(value)
	);
}

export function movePreflightDemo(
	path: string,
	managedCount: number,
	conflicts: string[] = [],
) {
	const normalized = path.trim();
	if (!isAbsoluteDemoServerPath(normalized))
		throw new Error("Выберите абсолютный путь на сервере.");
	return {
		path: normalized,
		managedCount,
		conflicts,
		token: `demo-preflight-${normalized}-${managedCount}-${conflicts.join("|")}`,
	};
}
