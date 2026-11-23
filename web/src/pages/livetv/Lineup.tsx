export type Selection =
  | { kind: "guide" }
  | { kind: "channel"; id: string }
  | { kind: "new" }
  | { kind: "source"; id: string }
  | { kind: "map"; id: string };
