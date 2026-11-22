import { describe, it, expect } from "vitest";
import { money, tokens, duration, gateDuration, waitingFor, shortSha } from "./format";

describe("money", () => {
  it("uses 4 decimals under a dollar and 2 above", () => {
    expect(money(0.0142)).toBe("$0.0142");
    expect(money(0)).toBe("$0.0000");
    expect(money(12.5)).toBe("$12.50");
  });
  it("survives a non-finite value rather than rendering NaN", () => {
    expect(money(NaN)).toBe("$0.00");
  });
});

describe("tokens", () => {
  it("scales units", () => {
    expect(tokens(842)).toBe("842");
    expect(tokens(18400)).toBe("18.4k");
    expect(tokens(2_500_000)).toBe("2.5M");
  });
  it("clamps nonsense to zero", () => {
    expect(tokens(-5)).toBe("0");
    expect(tokens(NaN)).toBe("0");
  });
});

describe("duration", () => {
  it("formats seconds, minutes and hours", () => {
    expect(duration(45_000)).toBe("45s");
    expect(duration(72_000)).toBe("1m12s");
    expect(duration(3_900_000)).toBe("1h05m");
  });
  it("never renders a negative", () => {
    expect(duration(-1)).toBe("0s");
  });
});

describe("gateDuration", () => {
  it("formats a gate's seconds like any duration", () => {
    expect(gateDuration(213.7)).toBe("3m33s");
    expect(gateDuration(4)).toBe("4s");
    expect(gateDuration(0.3)).toBe("<1s");
  });
  it("shows nothing for an unknown or reused gate (B-555)", () => {
    expect(gateDuration(undefined)).toBe("");
    expect(gateDuration(0)).toBe("");
  });
});

describe("waitingFor", () => {
  it("reports how long a run has been waiting", () => {
    const now = new Date("2026-07-26T12:05:00Z");
    expect(waitingFor("2026-07-26T12:00:00Z", now)).toBe("5m00s");
  });
  it("degrades gracefully on an unparseable timestamp", () => {
    expect(waitingFor("not a date")).toBe("just now");
  });
});

describe("shortSha", () => {
  it("truncates to 8", () => {
    expect(shortSha("e60dc7fe1234567890")).toBe("e60dc7fe");
  });
});
