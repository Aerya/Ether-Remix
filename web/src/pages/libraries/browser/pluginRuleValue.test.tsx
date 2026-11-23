import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Rule } from "../../../lib/contract";
import { librariesApi, type MediaKind } from "../api";
import { ExtendedCondition, conditionTitle } from "./RuleCondition";
import {
  buildNumericCondition,
  buildTextCondition,
  formatPluginCondition,
  parseNumericCondition,
  parseTextCondition,
} from "./pluginRuleValue";

const BOOK_KIND: MediaKind = {
  key: "book",
  label: "Books",
  media: "book",
  library: { name: "Books" },
  levels: [{ key: "book", label: "Book", role: "book", playable: true }],
  fields: [
    { key: "author", type: "people", label: "Author", indexed: true },
    {
      key: "subject",
      type: "strings",
      label: "Subject",
      indexed: true,
      values: ["Science Fiction", "Space Opera", "Solarpunk"],
    },
    { key: "series", type: "string", label: "Series", indexed: true },
    {
      key: "format",
      type: "enum",
      label: "Format",
      indexed: true,
      enum: ["Hardcover", "EPUB", "Audiobook"],
    },
    { key: "pages", type: "int", label: "Page count", indexed: true },
    { key: "duration", type: "duration", label: "Audio length", indexed: true },
    { key: "published", type: "date", label: "Published", indexed: true },
  ],
  present: {},
  browse: { sorts: ["title"], facets: ["author", "subject"], search: true },
  sync: { mode: "push" },
};

afterEach(() => {
  vi.restoreAllMocks();
});

describe("pluginRuleValue chip combining", () => {
  it("compiles and parses Any of, All of and None of", () => {
    const anyRule = buildTextCondition("subject", "any", ["Science Fiction", "Space Opera"]);
    expect(anyRule).toEqual({
      field: "browse:subject",
      op: "is",
      value: ["Science Fiction", "Space Opera"],
    });
    expect(parseTextCondition(anyRule, BOOK_KIND)).toEqual({
      fieldKey: "subject",
      mode: "any",
      values: ["Science Fiction", "Space Opera"],
      modeExplicit: true,
    });

    const allRule = buildTextCondition("subject", "all", ["Science Fiction", "Space Opera"]);
    expect(allRule).toEqual({
      field: "group",
      op: "is",
      value: [
        { field: "browse:subject", op: "is", value: "Science Fiction" },
        { field: "browse:subject", op: "is", value: "Space Opera" },
      ],
    });
    expect(parseTextCondition(allRule, BOOK_KIND)).toEqual({
      fieldKey: "subject",
      mode: "all",
      values: ["Science Fiction", "Space Opera"],
      modeExplicit: true,
    });
    expect(conditionTitle(allRule, BOOK_KIND)).toBe("Subject");

    const noneRule = buildTextCondition("subject", "none", ["Science Fiction", "Space Opera"]);
    expect(noneRule).toEqual({
      field: "browse:subject",
      op: "isNot",
      value: ["Science Fiction", "Space Opera"],
    });
    expect(parseTextCondition(noneRule, BOOK_KIND)).toEqual({
      fieldKey: "subject",
      mode: "none",
      values: ["Science Fiction", "Space Opera"],
      modeExplicit: true,
    });
  });

  it("parses equivalent group encodings back into chips without absorbing mixed groups", () => {
    const orGroup: Rule = {
      field: "group",
      op: "in",
      value: [
        { field: "browse:author", op: "is", value: "Ursula K. Le Guin" },
        { field: "browse:author", op: "is", value: "Isaac Asimov" },
      ],
    };
    expect(parseTextCondition(orGroup, BOOK_KIND)).toEqual({
      fieldKey: "author",
      mode: "any",
      values: ["Ursula K. Le Guin", "Isaac Asimov"],
      modeExplicit: true,
    });

    const allNegatedGroup: Rule = {
      field: "group",
      op: "is",
      value: [
        { field: "browse:subject", op: "isNot", value: "Cyberpunk" },
        { field: "browse:subject", op: "isNot", value: "Dystopian" },
      ],
    };
    expect(parseTextCondition(allNegatedGroup, BOOK_KIND)).toEqual({
      fieldKey: "subject",
      mode: "none",
      values: ["Cyberpunk", "Dystopian"],
      modeExplicit: true,
    });

    const notOrGroup: Rule = {
      field: "group",
      op: "isNot",
      value: [
        {
          field: "group",
          op: "in",
          value: [
            { field: "browse:subject", op: "is", value: "Cyberpunk" },
            { field: "browse:subject", op: "is", value: "Dystopian" },
          ],
        },
      ],
    };
    expect(parseTextCondition(notOrGroup, BOOK_KIND)).toEqual({
      fieldKey: "subject",
      mode: "none",
      values: ["Cyberpunk", "Dystopian"],
      modeExplicit: true,
    });

    // Multi-child group(isNot) is NAND, not None of — leave it in the general group editor.
    const nandGroup: Rule = {
      field: "group",
      op: "isNot",
      value: [
        { field: "browse:subject", op: "is", value: "Cyberpunk" },
        { field: "browse:subject", op: "is", value: "Dystopian" },
      ],
    };
    expect(parseTextCondition(nandGroup, BOOK_KIND)).toBeNull();

    // Mixed fields stay a general group.
    const mixedGroup: Rule = {
      field: "group",
      op: "is",
      value: [
        { field: "browse:subject", op: "is", value: "Cyberpunk" },
        { field: "browse:series", op: "is", value: "Foundation" },
      ],
    };
    expect(parseTextCondition(mixedGroup, BOOK_KIND)).toBeNull();
  });

  it("formats text and numeric plugin conditions clearly", () => {
    const allRule = buildTextCondition("subject", "all", ["Science Fiction", "Space Opera"]);
    expect(formatPluginCondition(allRule, BOOK_KIND)).toBe(
      "Subject is all of Science Fiction, Space Opera",
    );

    const ltYear = buildNumericCondition("year", "int", "lt", "1980", "").rule;
    expect(formatPluginCondition(ltYear, BOOK_KIND)).toBe("Year is less than 1980");

    const betweenDur = buildNumericCondition("duration", "duration", "between", "20", "60").rule;
    expect(betweenDur.value).toBe("1200..3600");
    expect(formatPluginCondition(betweenDur, BOOK_KIND)).toBe(
      "Audio length between 20 min and 60 min",
    );
  });
});

describe("pluginRuleValue numeric & date comparisons", () => {
  it("round-trips open, exclusive and closed bounds across int, float, duration and date", () => {
    for (const [op, a, b, wire] of [
      ["is", "1965", "", "1965"],
      ["isNot", "1965", "", "1965"],
      ["lt", "1980", "", "<1980"],
      ["lte", "1980", "", "..1980"],
      ["gt", "1980", "", ">1980"],
      ["gte", "1980", "", "1980.."],
      ["between", "1960", "1989", "1960..1989"],
    ] as const) {
      const built = buildNumericCondition("year", "int", op, a, b);
      expect(built.error).toBeUndefined();
      expect(built.rule.value).toBe(wire);
      expect(parseNumericCondition(built.rule, BOOK_KIND)).toEqual({
        fieldKey: "year",
        valueKind: "int",
        op,
        a,
        b,
      });
    }

    const floatGt = buildNumericCondition("rating", "float", "gt", "8.5", "");
    expect(floatGt.rule.value).toBe(">8.5");
    expect(parseNumericCondition(floatGt.rule, BOOK_KIND)).toMatchObject({
      op: "gt",
      a: "8.5",
    });

    const durLte = buildNumericCondition("duration", "duration", "lte", "45", "");
    expect(durLte.rule.value).toBe("..2700");
    expect(parseNumericCondition(durLte.rule, BOOK_KIND)).toMatchObject({
      op: "lte",
      a: "45",
    });

    // Pre-1970 negative Unix timestamps round-trip cleanly.
    const dateBetween = buildNumericCondition(
      "published",
      "date",
      "between",
      "1965-08-01",
      "1969-03-01",
    );
    expect(dateBetween.error).toBeUndefined();
    expect(parseNumericCondition(dateBetween.rule, BOOK_KIND)).toEqual({
      fieldKey: "published",
      valueKind: "date",
      op: "between",
      a: "1965-08-01",
      b: "1969-03-01",
    });

    const dateGt = buildNumericCondition("released", "date", "gt", "2020-01-15", "");
    expect(parseNumericCondition(dateGt.rule, BOOK_KIND)).toMatchObject({
      op: "gt",
      a: "2020-01-15",
    });

    const inverted = buildNumericCondition("pages", "int", "between", "500", "200");
    expect(inverted.error).toBeTruthy();
    expect(inverted.rule.value).toBe("");
  });
});

function ControlledCondition({ initial, onEmit }: { initial: Rule; onEmit?: (r: Rule) => void }) {
  const [rule, setRule] = useState<Rule>(initial);
  return (
    <ExtendedCondition
      rule={rule}
      kind={BOOK_KIND}
      libraryId={3}
      onChange={(next) => {
        setRule(next);
        onEmit?.(next);
      }}
      onRemove={() => {}}
      renderChild={() => null}
    />
  );
}

describe("ExtendedCondition plugin UI", () => {
  it("renders short-list chips with counts, declared 0-count values, custom values and All of mode", async () => {
    vi.spyOn(librariesApi, "fieldValues").mockResolvedValue({
      field: "subject",
      values: [
        { value: "Science Fiction", count: 42 },
        { value: "Space Opera", count: 27 },
        { value: "Solarpunk", count: 0 },
      ],
      more: false,
      truncated: false,
    });

    let latest: Rule | undefined;
    render(
      <ControlledCondition
        initial={{ field: "browse:subject", op: "is", value: "" }}
        onEmit={(r) => {
          latest = r;
        }}
      />,
    );

    const sciFi = await screen.findByRole("button", { name: /Science Fiction/ });
    expect(sciFi).toHaveTextContent("42");
    const solarpunk = screen.getByRole("button", { name: /Solarpunk/ });
    expect(solarpunk).toHaveTextContent("0");

    // Pick two chips (including declared zero-count value).
    fireEvent.click(sciFi);
    fireEvent.click(solarpunk);
    expect(latest).toEqual({
      field: "browse:subject",
      op: "is",
      value: ["Science Fiction", "Solarpunk"],
    });

    // Switch mode to All of -> compiles to group(is).
    fireEvent.click(screen.getByRole("button", { name: "Comparison" }));
    fireEvent.click(await screen.findByRole("option", { name: "All of" }));
    expect(latest).toEqual({
      field: "group",
      op: "is",
      value: [
        { field: "browse:subject", op: "is", value: "Science Fiction" },
        { field: "browse:subject", op: "is", value: "Solarpunk" },
      ],
    });

    // Add an unlisted value.
    const customInput = screen.getByLabelText("Add another value…");
    fireEvent.change(customInput, { target: { value: "Biopunk" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(screen.getByRole("button", { name: "Biopunk" })).toHaveAttribute("aria-pressed", "true");
    expect(latest).toEqual({
      field: "group",
      op: "is",
      value: [
        { field: "browse:subject", op: "is", value: "Science Fiction" },
        { field: "browse:subject", op: "is", value: "Solarpunk" },
        { field: "browse:subject", op: "is", value: "Biopunk" },
      ],
    });
  });

  it("switches to searchable long-list picker when tally reports more values", async () => {
    const spy = vi
      .spyOn(librariesApi, "fieldValues")
      .mockImplementation(async (_id, _field, opts) => {
        if (opts?.q) {
          return {
            field: "author",
            values: [{ value: "Octavia E. Butler", count: 14 }],
            more: false,
            truncated: false,
          };
        }
        return {
          field: "author",
          values: [
            { value: "Ursula K. Le Guin", count: 28 },
            { value: "Isaac Asimov", count: 25 },
          ],
          more: true,
          truncated: true,
        };
      });

    let latest: Rule | undefined;
    render(
      <ControlledCondition
        initial={{ field: "browse:author", op: "is", value: "" }}
        onEmit={(r) => {
          latest = r;
        }}
      />,
    );

    const search = await screen.findByPlaceholderText("Search values or type a new one…");
    fireEvent.focus(search);
    fireEvent.click(await screen.findByRole("button", { name: /Ursula K\. Le Guin/ }));
    expect(latest).toEqual({
      field: "browse:author",
      op: "is",
      value: "Ursula K. Le Guin",
    });

    // Search for another author.
    fireEvent.change(search, { target: { value: "Octavia" } });
    await waitFor(() => {
      expect(spy).toHaveBeenCalledWith(
        3,
        "author",
        expect.objectContaining({ q: "Octavia" }),
        expect.anything(),
      );
    });
    fireEvent.click(await screen.findByRole("button", { name: /Octavia E\. Butler/ }));
    expect(latest).toEqual({
      field: "browse:author",
      op: "is",
      value: ["Ursula K. Le Guin", "Octavia E. Butler"],
    });

    // Add an unlisted value via the "Use ..." option.
    fireEvent.change(search, { target: { value: "Becky Chambers" } });
    fireEvent.click(await screen.findByRole("button", { name: 'Use "Becky Chambers"' }));
    expect(latest).toEqual({
      field: "browse:author",
      op: "is",
      value: ["Ursula K. Le Guin", "Octavia E. Butler", "Becky Chambers"],
    });

    // Remove one chip with ×.
    const chipGroup = screen.getByRole("group", { name: "Author" });
    fireEvent.click(within(chipGroup).getByRole("button", { name: "Remove Octavia E. Butler" }));
    expect(latest).toEqual({
      field: "browse:author",
      op: "is",
      value: ["Ursula K. Le Guin", "Becky Chambers"],
    });
  });

  it("edits numeric between ranges without snapping while partially filled", async () => {
    let latest: Rule | undefined;
    render(
      <ControlledCondition
        initial={{ field: "browse:pages", op: "is", value: "" }}
        onEmit={(r) => {
          latest = r;
        }}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "Comparison" }));
    fireEvent.click(await screen.findByRole("option", { name: "is between" }));

    const minInput = screen.getByLabelText("Minimum");
    const maxInput = screen.getByLabelText("Maximum");
    fireEvent.change(minInput, { target: { value: "250" } });
    expect(minInput).toHaveValue(250);
    expect(latest?.value).toBe("");

    fireEvent.change(maxInput, { target: { value: "600" } });
    expect(latest).toEqual({ field: "browse:pages", op: "is", value: "250..600" });
  });
});
