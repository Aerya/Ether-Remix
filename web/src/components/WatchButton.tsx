import { useEffect, useState } from "react";
import { Popover } from "@braedonsaunders/appkit-ui";
import { Check, ChevronDown, Tv } from "lucide-react";
import { apiFetch } from "../lib/api";
import { t } from "../i18n";

type PlayerID = "vela" | "jellyfin";
type Player = { id: PlayerID; url: string };
type Players = { available: boolean; players: Player[]; url?: string };
const preferenceKey = "ether.web-player";
const playerName = (id: PlayerID) => (id === "vela" ? t("Vela") : t("Jellyfin web"));

export function WatchButton({
  app,
  popoverSide = "bottom",
  variant = "ghost",
}: {
  app?: "vela";
  popoverSide?: "top" | "bottom";
  variant?: "ghost" | "outline";
} = {}) {
  const [players, setPlayers] = useState<Player[]>([]);
  const [preferred, setPreferred] = useState<PlayerID>(() => {
    try {
      return localStorage.getItem(preferenceKey) === "jellyfin" ? "jellyfin" : "vela";
    } catch {
      return "vela";
    }
  });
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState(false);
  useEffect(() => {
    let active = true;
    const check = () =>
      apiFetch<Players>("/web-player")
        .then((r) => {
          if (active) setPlayers(r.available ? r.players : []);
        })
        .catch(() => {
          if (active) setPlayers([]);
        });
    void check();
    window.addEventListener("focus", check);
    return () => {
      active = false;
      window.removeEventListener("focus", check);
    };
  }, []);
  const offered = app ? players.filter((player) => player.id === app) : players;
  const selected = offered.find((player) => player.id === preferred) ?? offered[0];
  if (!selected) return null;

  async function launch(id: PlayerID) {
    setOpen(false);
    setPreferred(id);
    if (!app) {
      try {
        localStorage.setItem(preferenceKey, id);
      } catch {
        /* Storage can be disabled. */
      }
    }
    const tab = window.open("about:blank", "_blank");
    if (!tab) {
      setError(true);
      return;
    }
    tab.opener = null;
    setBusy(true);
    setError(false);
    try {
      const result = await apiFetch<Players>(`/web-player?app=${id}`, {
        method: "POST",
        body: "{}",
      });
      const player = result.players.find((candidate) => candidate.id === id);
      if (!player) throw new Error("Player unavailable");
      const target = new URL(player.url);
      const args = new URLSearchParams(target.hash.slice(1));
      args.set("console", window.location.origin + "/ether/");
      target.hash = args.toString();
      tab.location.replace(target.href);
    } catch {
      tab.close();
      setError(true);
    } finally {
      setBusy(false);
    }
  }

  const buttonClass =
    variant === "outline"
      ? "inline-flex h-9 items-center justify-center gap-2 border border-border bg-surface text-sm font-medium text-fg transition-colors hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:opacity-50"
      : "inline-flex h-9 items-center justify-center gap-2 text-sm font-medium hover:bg-bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50 disabled:opacity-50";
  return (
    <div className="inline-flex items-center">
      <button
        type="button"
        disabled={busy}
        className={`${buttonClass} px-3 ${offered.length > 1 ? "rounded-l-md" : "rounded-md"}`}
        title={error ? t("Could not open the web player. Try again.") : playerName(selected.id)}
        aria-label={t("Open the web player")}
        onClick={() => void launch(selected.id)}
      >
        <Tv size={16} aria-hidden />
        {t("Watch")}
      </button>
      {offered.length > 1 && (
        <Popover
          open={open}
          onOpenChange={setOpen}
          align="end"
          side={popoverSide}
          trigger={
            <button
              type="button"
              disabled={busy}
              className={`${buttonClass} rounded-r-md ${variant === "outline" ? "-ml-px" : "border-l border-border"} px-1.5`}
              aria-label={t("Choose web player")}
              aria-haspopup="menu"
              aria-expanded={open}
              onClick={() => setOpen(!open)}
            >
              <ChevronDown size={14} aria-hidden />
            </button>
          }
        >
          <div role="menu" aria-label={t("Choose web player")} className="min-w-40 p-1">
            {offered.map((player) => (
              <button
                key={player.id}
                type="button"
                role="menuitemradio"
                aria-checked={selected.id === player.id}
                disabled={busy}
                className="flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm text-fg hover:bg-bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/50"
                onClick={() => void launch(player.id)}
              >
                <Check
                  size={14}
                  aria-hidden
                  className={selected.id === player.id ? "" : "invisible"}
                />
                {playerName(player.id)}
              </button>
            ))}
          </div>
        </Popover>
      )}
    </div>
  );
}
