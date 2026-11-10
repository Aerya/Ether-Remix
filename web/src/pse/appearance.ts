// Owned by Remix: upstream continues to provide the light/dark theme engine.
export const PALETTE_KEY = "ether-remix.palette";
export const palettes = [
  {
    id: "default",
    name: "Ether",
    dark: ["#020617", "#0f172a", "#f1f5f9", "#14b8a6"],
    light: ["#ffffff", "#f8fafc", "#0f172a", "#0f766e"],
  },
  {
    id: "nord",
    name: "Nord",
    dark: ["#2e3440", "#3b4252", "#eceff4", "#88c0d0"],
    light: ["#eceff4", "#e5e9f0", "#2e3440", "#436576"],
  },
  {
    id: "catppuccin",
    name: "Catppuccin",
    dark: ["#1e1e2e", "#313244", "#cdd6f4", "#94e2d5"],
    light: ["#eff1f5", "#e6e9ef", "#4c4f69", "#16776f"],
  },
  {
    id: "solarized",
    name: "Solarized",
    dark: ["#002b36", "#073642", "#b1c1c1", "#5cc4bc"],
    light: ["#fdf6e3", "#eee8d5", "#475b61", "#176f69"],
  },
  {
    id: "gruvbox",
    name: "Gruvbox",
    dark: ["#282828", "#3c3836", "#ebdbb2", "#b8bb26"],
    light: ["#fbf1c7", "#ebdbb2", "#3c3836", "#626800"],
  },
  {
    id: "rose-pine",
    name: "Rosé Pine",
    dark: ["#191724", "#26233a", "#e0def4", "#9ccfd8"],
    light: ["#faf4ed", "#f2e9e1", "#575279", "#286983"],
  },
] as const;
export type PaletteID = (typeof palettes)[number]["id"];
export function normalizePalette(value: string | null): PaletteID {
  return palettes.find((p) => p.id === value)?.id ?? "default";
}
export function paletteScript() {
  return `try{var p=localStorage.getItem(${JSON.stringify(PALETTE_KEY)});document.documentElement.dataset.palette=${JSON.stringify(palettes.map((p) => p.id))}.includes(p)?p:"default"}catch{}`;
}
