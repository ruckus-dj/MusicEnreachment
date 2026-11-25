export const demoSourcesHref = "#/sources";

export const demoSourceRootHref = (rootId: string) =>
	`${demoSourcesHref}/${encodeURIComponent(rootId)}`;

export const demoSourceLocationHref = (rootId: string, locationId: string) =>
	`${demoSourceRootHref(rootId)}/locations/${encodeURIComponent(locationId)}`;

export function parseDemoSourceRoute(
	path: string,
):
	| { view: "list" }
	| { view: "root"; rootId: string }
	| { view: "location"; rootId: string; locationId: string }
	| undefined {
	const canonical = path.startsWith("#") ? path.slice(1) : path;
	if (canonical === "/sources") return { view: "list" };
	const match = /^\/sources\/([^/]+)(?:\/locations\/([^/]+))?$/.exec(canonical);
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
