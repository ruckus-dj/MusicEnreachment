export const demoSourceLocationHref = (rootId: string, locationId: string) =>
	`#sources/${encodeURIComponent(rootId)}/locations/${encodeURIComponent(locationId)}`;

export function parseDemoSourceRoute(
	path: string,
):
	| { view: "list" }
	| { view: "root"; rootId: string }
	| { view: "location"; rootId: string; locationId: string }
	| undefined {
	if (path === "sources") return { view: "list" };
	const match = /^sources\/([^/]+)(?:\/locations\/([^/]+))?$/.exec(path);
	if (!match) return undefined;
	try {
		const rootId = decodeURIComponent(match[1]);
		return match[2]
			? { view: "location", rootId, locationId: decodeURIComponent(match[2]) }
			: { view: "root", rootId };
	} catch {
		return undefined;
	}
}
