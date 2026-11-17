import { expect, it } from "vitest";
import { ApiError, errorMessage } from "./api";
it("shows the rejected collection field instead of the generic validation error", () => {
  const err = new ApiError(400, "validation", "the collection is invalid", [
    { field: "sources[0].params.genre", message: "genre is required" },
  ]);
  expect(errorMessage(err)).toBe("sources[0].params.genre: genre is required");
});
