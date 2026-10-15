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

// The guest-readable delivery encoding: a value is written as a single-quoted
// literal, and these are the value classes that form must carry exactly. The
// Go side (internal/justcode/project_secret_encoding_loaders_test.go) proves
// the encoder emits this form; these tests prove the loaders read it back.
const guestReadableValues = {
  PLAIN: "plainvalue",
  MULTILINE: "-----BEGIN KEY-----\nAAAA\n-----END KEY-----",
  TRAILING: "trailing   ",
  LEADING: "   leading",
  HASH: "hash # inside",
  DQUOTE: 'has " quote',
  BACKSLASH: "back\\slash",
  EMPTY: "",
};

for (const [name, value] of Object.entries(guestReadableValues)) {
  test(`single-quoted ${name} round-trips through dotenv`, () => {
    const parsed = dotenv.parse(`${name}='${value}'\n`);
    assert.equal(parsed[name], value);
  });

  test(`single-quoted ${name} round-trips through dotenv-expand`, () => {
    const parsed = dotenv.parse(`${name}='${value}'\n`);
    const result = expand({ parsed, processEnv: {} });
    assert.equal(result.parsed[name], value);
  });
}

test("a raw multiline value breaks the whole file for the line-oriented parsers", () => {
  // Not confined to one variable: this is why the value is encoded.
  const raw = "TLS_KEY=-----BEGIN KEY-----\nAAAA\n-----END KEY-----\nAPI_KEY=still-here\n";
  const parsed = dotenv.parse(raw);
  assert.notEqual(parsed.TLS_KEY, "-----BEGIN KEY-----\nAAAA\n-----END KEY-----");
});

test("the two refused classes are refused for different reasons", () => {
  // Pinned so a future attempt to "just escape it" has the evidence in front
  // of it. The reasons are not the same, and neither is a Node problem.
  const apostrophe = "it's";
  const dollar = "pre$VAR-post";

  // Node reads the apostrophe fine — dotenv takes the last quote on the line.
  // The refusal is driven by the Go loader, which refuses the whole file; see
  // internal/justcode/project_secret_encoding_loaders_test.go.
  assert.equal(dotenv.parse(`K='${apostrophe}'\n`).K, apostrophe);

  // The dollar case is a genuine conflict between loaders rather than a
  // mangling: the same bytes mean the literal value to plain dotenv and
  // "pre-post" once expansion runs, so no encoding can be exact for both.
  assert.equal(dotenv.parse(`K='${dollar}'\n`).K, dollar);
  assert.equal(expand({ parsed: dotenv.parse(`K='${dollar}'\n`), processEnv: {} }).parsed.K, "pre-post");
});

// Backslashes are the class the encoder refuses in two shapes, and this
// records why it is not Node's doing: Node reads every one of them exactly.
// The refusal is driven by python-dotenv (pair, and trailing) and godotenv
// (trailing, which it refuses as an unterminated quote).
test("Node reads the backslash shapes the encoder refuses", () => {
  const values = {
    interior: "back\\slash",
    leading: "\\leading",
    pair: "a\\\\b",
    trailing: "trailing\\",
  };
  // The realistic shape: the risky value followed by another quoted value,
  // which is what a generated file contains.
  const body = Object.entries(values)
    .map(([name, value]) => `K_${name}='${value}'\n`)
    .join("");
  const parsed = dotenv.parse(`${body}K_AFTER='after'\n`);
  for (const [name, value] of Object.entries(values)) {
    assert.equal(parsed[`K_${name}`], value, `Node must read K_${name} exactly`);
  }
  assert.equal(parsed.K_AFTER, "after");
});
