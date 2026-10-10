## REQ-001 — Self-contained local delivery

**Originates from:** INT-001
**Priority:** must

The project shall provide a self-contained `index.html` at the project root, with all application HTML, CSS, JavaScript, and visual assets embedded inline. It shall make no external network requests, require no build step, and operate when served by Python’s `http.server`.

The project shall include `package.json` with a `test` script of `node --test tests/`; automated behaviour tests shall reside in `tests/*.test.mjs`.

## REQ-002 — TI-36X Pro visual replica

**Originates from:** INT-001
**Priority:** must

The application shall render a single-page visual replica of the front face of a Texas Instruments TI-36X Pro calculator, including its dark shaped enclosure, title and Texas Instruments branding, solar panel, LCD, navigation pad, and labeled keys.

Appearance shall match **REF-IMG-6c63e390** when judged at its native 730 × 1500 px scale, with the calculator centered on a white page background and retaining the reference’s proportions, key layout, labels, colors, shadows, display styling, and typography as closely as browser rendering permits.

## REQ-003 — Responsive calculator presentation

**Originates from:** INT-001
**Priority:** must

The calculator shall remain fully visible, proportionally scaled, and operable on viewports narrower or shorter than the reference image. It shall preserve the physical calculator’s portrait aspect ratio and shall not require horizontal scrolling.

**Assumption:** The reference image defines the canonical desktop-scale appearance; responsive scaling may reduce fine visual detail on small screens without rearranging the physical key layout.

## REQ-004 — Interactive physical controls

**Originates from:** INT-001
**Priority:** must

Every visible calculator control required for supported functionality shall be clickable or tappable, provide clear pressed-state feedback, and update calculator state equivalently to its physical TI-36X Pro counterpart. The application shall support keyboard entry for digits, decimal point, arithmetic operators, parentheses, Enter/equals, Backspace/delete, Escape/clear, and arrow navigation where the corresponding on-screen control is supported.

## REQ-005 — Expression entry and display

**Originates from:** INT-001
**Priority:** must

The LCD shall display entered mathematical expressions and evaluated results using calculator-style formatting. It shall support cursor-based insertion, deletion, left/right navigation, clear, prior-answer recall, and a visible angle-unit indicator.

The display shall present errors without crashing or silently producing an invalid result. Clearing an error shall return the calculator to an operable entry state.

## REQ-006 — Core arithmetic

**Originates from:** INT-001
**Priority:** must

The calculator shall correctly evaluate expressions using addition, subtraction, multiplication, division, unary negation, decimals, parentheses, percentage, powers, squares, square roots, reciprocals, factorial, and scientific notation entry.

It shall observe normal mathematical precedence and parentheses, retain sufficient numeric precision for typical scientific-calculator use, and show an appropriate error for invalid operations such as division by zero or a factorial outside its supported domain.

## REQ-007 — Scientific functions and angle modes

**Originates from:** INT-001
**Priority:** must

The calculator shall support sine, cosine, tangent, inverse trigonometric functions, natural logarithm, common logarithm, exponential, ten-to-the-power, and constants π and e.

It shall support degree, radian, and gradian angle modes. Trigonometric calculations and the LCD angle indicator shall use the selected mode. Inverse trigonometric results shall be returned in that same selected mode.

## REQ-008 — Fraction, complex, and numeric-format operations

**Originates from:** INT-001, INT-003
**Priority:** must
The calculator shall support complex numbers as first-class values: entry of the imaginary unit `i`, rectangular `a+bi` and polar `r∠θ` forms, and arithmetic (`+`, `−`, `×`, `÷`, powers) that promotes real operands to complex when either operand is complex. A complex result shall be displayable in rectangular form and convertible between rectangular and polar form via the NUM menu, with the polar angle rendered in the active angle unit; real-to-polar conversion of a real scalar is a valid operation matching the physical TI-36X Pro. A conversion that does not apply to the current result shall surface an LCD error and shall preserve the existing result unchanged.

**Assumption:** Exact symbolic algebra beyond rational fractions is not required; unsupported expressions may be evaluated numerically.

## REQ-009 — Memory, variables, and answer value

**Originates from:** INT-001, INT-003
**Priority:** must
The calculator shall provide the named user variables printed on the keypad (`A`–`F`, `M`, `X`, `Y`, `Z`), with store (`sto→`), recall, and clear-variable operations that clear stored variables to the chosen label without prescribing a confirmation mechanism. Recalling a variable inserts its current value into the entry; storing assigns the current result or entry value to the chosen variable. Stored values shall remain available until explicitly cleared or the page is reloaded; no persistence across reloads is required.

## REQ-010 — Secondary-function access

**Originates from:** INT-001
**Priority:** must

The `2nd` key shall activate the blue secondary function printed for supported controls. The active secondary state shall be visibly indicated and shall apply to the next applicable key press only, then return to normal state.

## REQ-011 — Navigation and mode selection

**Originates from:** INT-001
**Priority:** must
The directional pad and mode-related controls shall permit navigation of calculator menus and selection of supported settings, including angle mode. Menus shall be rendered within the calculator LCD and shall be usable by mouse, touch, and keyboard arrow keys.

## REQ-012 — Unsupported advanced calculator modes

**Originates from:** INT-001
**Priority:** wont

Graphing, persistent storage across page reloads, program execution, statistics/regression workflows, matrix/vector computation, numeric equation solving, polynomial solving, systems solving, distributions, data tables, base-n arithmetic, unit conversion catalogs, and numerical integration/differentiation are out of scope for this delivery.

Their physical labels may be reproduced for visual fidelity, but controls whose sole purpose is an out-of-scope feature need not perform that feature.

## REQ-013 — No external product dependencies

**Originates from:** INT-001
**Priority:** must
The application shall not depend on external fonts, images, scripts, style sheets, APIs, telemetry, analytics, browser extensions, or server-side calculation services.

