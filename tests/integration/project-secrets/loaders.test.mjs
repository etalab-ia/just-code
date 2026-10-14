import assert from "node:assert/strict";
import test from "node:test";
import dotenv from "dotenv";
import { expand } from "dotenv-expand";

// These are proposed guest-visible markers, not credential values. Exercise
// the real loaders rather than mocking their interpolation behavior.
const markers = [
  "__JUST_CODE_SECRET_COMPONENT_A_API_KEY__",
  "__JUST_CODE_SECRET_COMPONENT_B_API_KEY__",
];

for (const marker of markers) {
  for (const quote of ["", "'", '"']) {
    test(`dotenv preserves opaque marker with ${quote || "no"} quotes`, () => {
      const parsed = dotenv.parse(`API_KEY=${quote}${marker}${quote}\n`);
      assert.equal(parsed.API_KEY, marker);
    });

    test(`dotenv-expand preserves opaque marker with ${quote || "no"} quotes`, () => {
      const parsed = dotenv.parse(`API_KEY=${quote}${marker}${quote}\n`);
      // Do not import, modify or interpolate the host's environment.
      const result = expand({ parsed, processEnv: {} });
      assert.equal(result.parsed.API_KEY, marker);
    });
  }
}

test("same variable in two components retains distinct markers", () => {
  const values = markers.map((marker) =>
    expand({ parsed: dotenv.parse(`API_KEY=${marker}\n`), processEnv: {} })
      .parsed.API_KEY,
  );
  assert.deepEqual(values, markers);
  assert.notEqual(values[0], values[1]);
});

test("default dollar placeholders are not safe through interpolation", () => {
  for (const quote of ["", "'", '"']) {
    const placeholder = "$MSB_JUST_CODE_SECRET_A";
    const parsed = dotenv.parse(`API_KEY=${quote}${placeholder}${quote}\n`);
    assert.equal(parsed.API_KEY, placeholder);
    const result = expand({ parsed, processEnv: {} });
    assert.equal(result.parsed.API_KEY, "");
  }
});
