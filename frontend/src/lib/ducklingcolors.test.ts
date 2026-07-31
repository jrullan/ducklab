import { describe, it, expect } from "vitest";
import { assignDucklingColors } from "./colors";

describe("duckling colours", () => {
  // The colour was a position in whatever list the view had: the run's roster
  // in a transcript, the fleet listing on the Ducklings page. One model was blue
  // as an architect and orange as an implementer, so "orange" meant nothing.
  it("gives each duckling one colour regardless of any run's roster", () => {
    const fleet = [{ id: "pato-sonnet" }, { id: "pato-atom" }, { id: "deepseekv4pro" }];
    const a = assignDucklingColors(fleet);
    const b = assignDucklingColors([...fleet].reverse());
    expect(a).toEqual(b);
  });

  it("honours a declared slot", () => {
    const got = assignDucklingColors([
      { id: "pato-atom" },
      { id: "pato-sonnet", color: 4 },
    ]);
    expect(got["pato-sonnet"]).toBe("var(--series-4)");
  });

  // A declared slot is a claim: nobody else may be assigned it, or two
  // ducklings would be the same colour and the preference would be pointless.
  it("never hands a claimed slot to anyone else", () => {
    const got = assignDucklingColors([
      { id: "a" },
      { id: "b" },
      { id: "c", color: 1 },
    ]);
    expect(got["c"]).toBe("var(--series-1)");
    expect(got["a"]).toBe("var(--series-2)");
    expect(got["b"]).toBe("var(--series-3)");
  });

  // Past eight the palette stops clearing the colour-vision floor, so a ninth
  // is muted rather than a colour that only looks distinct.
  it("mutes past the palette", () => {
    const fleet = Array.from({ length: 9 }, (_, i) => ({ id: `d${i}` }));
    const got = assignDucklingColors(fleet);
    expect(got["d8"]).toBe("var(--text-muted)");
  });
});
