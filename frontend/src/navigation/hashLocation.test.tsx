import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  type HashAttempt,
  registerHashNavigationGuard,
  resolveHashNavigation,
  useHashLocation,
} from "./hashLocation";

const guardCleanups: Array<() => void> = [];

function requireAttempt(attempts: HashAttempt[], index: number) {
  const attempt = attempts[index];
  if (!attempt) throw new Error(`Missing navigation attempt ${index}`);
  return attempt;
}

beforeEach(() => {
  window.history.replaceState(null, "", "/#/settings");
});

afterEach(() => {
  cleanup();
  for (const unregister of guardCleanups.splice(0)) unregister();
  window.history.replaceState(null, "", "/#/settings");
});

describe("hash navigation guard", () => {
  it("fences stale browser attempts and restores accepted routes on stay or cleanup", () => {
    const attempts: HashAttempt[] = [];
    const { result } = renderHook(() => useHashLocation());
    const unregister = registerHashNavigationGuard({
      shouldBlock: () => true,
      onBlocked: (attempt) => attempts.push(attempt),
    });
    guardCleanups.push(unregister);

    act(() => {
      window.history.pushState(null, "", "/#/first");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    const firstAttempt = requireAttempt(attempts, 0);

    act(() => {
      window.history.pushState(null, "", "/#/second");
    });
    expect(resolveHashNavigation(firstAttempt.id, "leave")).toBe(false);
    const secondAttempt = requireAttempt(attempts, 1);
    expect(secondAttempt.to).toBe("#/second");

    const anchor = document.createElement("a");
    anchor.href = "#/third";
    document.body.append(anchor);
    const anchorClick = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      button: 0,
    });
    act(() => {
      anchor.dispatchEvent(anchorClick);
    });
    expect(anchorClick.defaultPrevented).toBe(true);
    const anchorAttempt = requireAttempt(attempts, 2);
    expect(anchorAttempt.to).toBe("#/third");
    act(() => {
      expect(resolveHashNavigation(anchorAttempt.id, "stay")).toBe(true);
    });
    expect(window.location.hash).toBe("#/settings");
    expect(result.current).toBe("#/settings");

    act(() => {
      window.history.pushState(null, "", "/#/provisional");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    const oldGuardAttempt = requireAttempt(attempts, 3);
    act(() => {
      unregister();
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    expect(window.location.hash).toBe("#/settings");
    expect(result.current).toBe("#/settings");

    const replacementAttempts: HashAttempt[] = [];
    const unregisterReplacement = registerHashNavigationGuard({
      shouldBlock: () => true,
      onBlocked: (attempt) => replacementAttempts.push(attempt),
    });
    guardCleanups.push(unregisterReplacement);
    unregister();

    act(() => {
      window.history.pushState(null, "", "/#/replacement");
      window.dispatchEvent(new HashChangeEvent("hashchange"));
    });
    expect(replacementAttempts).toHaveLength(1);
    expect(result.current).toBe("#/settings");

    const replacementAttempt = requireAttempt(replacementAttempts, 0);
    act(() => {
      expect(resolveHashNavigation(replacementAttempt.id, "stay")).toBe(true);
    });
    anchor.href = "#";
    const click = new MouseEvent("click", {
      bubbles: true,
      cancelable: true,
      button: 0,
    });
    act(() => anchor.dispatchEvent(click));

    expect(click.defaultPrevented).toBe(true);
    const emptyHashAttempt = requireAttempt(replacementAttempts, 1);
    expect(emptyHashAttempt.to).toBe("");
    act(() => {
      expect(resolveHashNavigation(emptyHashAttempt.id, "leave")).toBe(true);
    });
    expect(result.current).toBe("");
    anchor.remove();
    expect(oldGuardAttempt.to).toBe("#/provisional");
  });
});
