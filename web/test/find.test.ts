import assert from "node:assert/strict";
import { test } from "node:test";
import { findMatches, stepMatch, viewportHighlights } from "../src/findhelpers";

test("findMatches finds case-insensitive matches across lines", () => {
  const lines = ["hello world", "Hello again, hello", "nothing here"];
  const m = findMatches(lines, "hello");
  assert.deepEqual(m, [
    { row: 0, col: 0, len: 5 },
    { row: 1, col: 0, len: 5 },
    { row: 1, col: 13, len: 5 },
  ]);
  assert.deepEqual(findMatches(lines, ""), []);
});

test("findMatches accounts for wide CJK columns", () => {
  const lines = ["中文test"];
  const m = findMatches(lines, "test");
  assert.deepEqual(m, [{ row: 0, col: 4, len: 4 }]);
});

test("stepMatch wraps forward and backward", () => {
  assert.equal(stepMatch(-1, 0, 1), -1);
  assert.equal(stepMatch(-1, 3, 1), 0);
  assert.equal(stepMatch(-1, 3, -1), 2);
  assert.equal(stepMatch(2, 3, 1), 0);
  assert.equal(stepMatch(0, 3, -1), 2);
});

test("viewportHighlights filters to visible rows and marks active match", () => {
  const matches = [
    { row: 2, col: 1, len: 3 },
    { row: 12, col: 4, len: 3 },
    { row: 25, col: 0, len: 3 },
  ];
  assert.deepEqual(viewportHighlights(matches, 1, 10, 10), [
    { row: 2, col: 4, len: 3, current: true },
  ]);
});
