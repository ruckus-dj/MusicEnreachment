import { expect, it } from "vitest";
import {
	incomingFilesCount,
	incomingGroupsCount,
	incomingMessage,
} from "./incomingMessages";

it("translates descriptor-based incoming reasons and notices in both locales", () => {
	expect(incomingMessage("en", "incompleteReason")).toBe(
		"Incomplete release; one track is uncertain",
	);
	expect(incomingMessage("ru", "confidentReason", { count: 8, total: 8 })).toBe(
		"8 / 8: все назначения уверенные",
	);
	expect(incomingMessage("en", "automaticNotice", { count: 3 })).toContain(
		"3 tracks",
	);
	expect(incomingMessage("ru", "automaticNotice", { count: 3 })).toContain(
		"3.",
	);
});

it("localizes dynamic group and file counts with locale-specific plural forms", () => {
	expect(incomingGroupsCount("en", 1)).toBe("1 group · source queue");
	expect(incomingGroupsCount("en", 2)).toBe("2 groups · source queue");
	expect(incomingGroupsCount("ru", 2)).toBe("Группы: 2 · очередь источников");
	expect(incomingGroupsCount("ru", 5)).toBe("Групп: 5 · очередь источников");
	expect(incomingFilesCount("ru", 1)).toBe("1 файл");
	expect(incomingFilesCount("ru", 2)).toBe("2 файла");
	expect(incomingFilesCount("ru", 5)).toBe("5 файлов");
});
