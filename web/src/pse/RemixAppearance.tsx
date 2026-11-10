import { Button, useTheme } from "@braedonsaunders/appkit-ui";
import { Palette } from "lucide-react";
import { useEffect, useState } from "react";
import { getLocale } from "../i18n";
import { PALETTE_KEY, palettes, normalizePalette } from "./appearance";

export function RemixAppearance() {
  const { resolvedTheme, setTheme } = useTheme();
  const [open, setOpen] = useState(false);
  const [palette, setPalette] = useState(() => {
    try {
      return normalizePalette(localStorage.getItem(PALETTE_KEY));
    } catch {
      return "default" as const;
    }
  });
  const fr = getLocale().startsWith("fr");
  useEffect(() => {
    document.documentElement.dataset.palette = palette;
    try {
      localStorage.setItem(PALETTE_KEY, palette);
    } catch {
      /* Private browsing */
    }
  }, [palette]);
  useEffect(() => {
    const sync = (e: StorageEvent) => {
      if (e.key === PALETTE_KEY || e.key === null) setPalette(normalizePalette(e.newValue));
    };
    window.addEventListener("storage", sync);
    return () => window.removeEventListener("storage", sync);
  }, []);
  return (
    <div className="relative">
      <Button
        variant="ghost"
        size="sm"
        aria-label={fr ? "Apparence" : "Appearance"}
        aria-expanded={open}
        onClick={() => setOpen(!open)}
      >
        <Palette size={18} />
      </Button>
      {open && (
        <>
          <button
            className="fixed inset-0 z-40 cursor-default"
            aria-label={fr ? "Fermer l’apparence" : "Close appearance"}
            onClick={() => setOpen(false)}
          />
          <section
            aria-label={fr ? "Apparence" : "Appearance"}
            onKeyDown={(e) => {
              if (e.key === "Escape") setOpen(false);
            }}
            className="absolute right-0 z-50 mt-2 w-80 max-w-[90vw] space-y-3 rounded-xl border border-border bg-elevated p-4 shadow-lg"
          >
            <h2 className="font-semibold">{fr ? "Apparence" : "Appearance"}</h2>
            <div className="flex gap-2">
              {(["light", "dark"] as const).map((mode) => (
                <Button
                  key={mode}
                  size="sm"
                  variant={resolvedTheme === mode ? "default" : "outline"}
                  aria-pressed={resolvedTheme === mode}
                  onClick={() => setTheme(mode)}
                >
                  {mode === "light" ? (fr ? "Clair" : "Light") : fr ? "Sombre" : "Dark"}
                </Button>
              ))}
            </div>
            <div className="grid grid-cols-2 gap-2">
              {palettes.map((p) => {
                const [bg, surface, fg, accent] = p[resolvedTheme];
                return (
                  <button
                    key={p.id}
                    aria-pressed={palette === p.id}
                    onClick={() => setPalette(p.id)}
                    className="rounded-lg border-2 p-3 text-left text-sm focus-visible:outline-2 focus-visible:outline-offset-2"
                    style={{
                      background: bg,
                      color: fg,
                      borderColor: palette === p.id ? accent : surface,
                    }}
                  >
                    <span className="mb-2 flex gap-1" aria-hidden>
                      {[surface, fg, accent].map((color) => (
                        <span
                          key={color}
                          className="h-3 w-3 rounded-full"
                          style={{ background: color }}
                        />
                      ))}
                    </span>
                    {p.name}
                  </button>
                );
              })}
            </div>
            <p className="text-xs text-fg-muted">
              {fr
                ? "Aperçu immédiat · Choix mémorisé sur ce navigateur."
                : "Instant preview · Saved in this browser."}
            </p>
          </section>
        </>
      )}
    </div>
  );
}
