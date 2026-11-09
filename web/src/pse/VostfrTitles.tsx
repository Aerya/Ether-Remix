import { Button, SettingsSection, toast } from "@braedonsaunders/appkit-ui";
import { X } from "lucide-react";
import { useState } from "react";
import { Artwork } from "../components/Artwork";
import { TitleSearch } from "../components/pickers";
import { titleSubtitle } from "../components/Poster";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import type { TitleCard } from "../lib/contract";
import { useAsync } from "../lib/hooks";
import { useTitle } from "../lib/titles";

/**
 * Titres en VOSTFR (internal/pse/themes, vostfr.go) : on cherche un film ou
 * une série, on l'ajoute, et Ether-Remix le montre dans Films et Séries même sans
 * VF, ainsi que dans la bibliothèque « VOSTFR » de Plex et Jellyfin.
 */

const ENDPOINT = "/pse/themes/vostfr";
const L = (fr: string, en: string) => (getLocale().toLowerCase().startsWith("fr") ? fr : en);

interface VostfrData {
  keys: string[];
  max: number;
}

export function VostfrTitles() {
  const doc = useAsync(() => apiFetch<VostfrData>(ENDPOINT), []);
  const [keys, setKeys] = useState<string[] | null>(null);
  const [known, setKnown] = useState<Record<string, TitleCard>>({});
  const [busy, setBusy] = useState(false);
  const list = keys ?? doc.data?.keys ?? [];

  const send = async (key: string, type: string, add: boolean) => {
    setBusy(true);
    try {
      const result = await apiFetch<{ queued?: number; searchWarning?: string }>(ENDPOINT, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key, type, add }),
      });
      const next = await apiFetch<VostfrData>(ENDPOINT);
      setKeys(next.keys);
      return result;
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
      return null;
    } finally {
      setBusy(false);
    }
  };

  return (
    <SettingsSection
      title={L("Titres en VOSTFR à la demande", "On-demand subtitled titles")}
      description={L(
        "Une exception volontaire à vos critères FR/MULTi. Ajoutez un film, une série ou un animé précis : il apparaît dans Films et Séries même sans VF, et dans la bibliothèque « VOSTFR » de Plex et Jellyfin. Chaque ajout priorise une recherche de releases, si l’indexation est active, dans les limites des budgets et des pauses de lecture.",
        "An explicit exception to your French/MULTi criteria. Add a specific movie, show or anime: it appears in Movies and Shows even without a French version, and in the “VOSTFR” library in Plex and Jellyfin. Each addition queues a priority release search when indexing is enabled, subject to budgets and playback pauses.",
      )}
    >
      <div className="space-y-3 px-4 py-3">
        <TitleSearch
          label={L("Chercher un film ou une série", "Search a movie or show")}
          placeholder={L("Chercher un film ou une série…", "Search a movie or show…")}
          excludeKeys={new Set(list)}
          onPick={(title) => {
            if (busy) return;
            if (title.type !== "movie" && title.type !== "show") {
              toast.error(
                L(
                  "Seuls les films et les séries peuvent être ajoutés.",
                  "Only movies and shows can be added.",
                ),
              );
              return;
            }
            setKnown((k) => ({ ...k, [title.key]: title }));
            void send(title.key, title.type, true).then((result) => {
              if (!result) return;
              toast.success(
                L(
                  `« ${title.title} » ajouté en VOSTFR${result.queued ? " · recherche priorisée" : ""}`,
                  `“${title.title}” added${result.queued ? " · priority search queued" : ""}`,
                ),
              );
              if (result.searchWarning) toast.warning(result.searchWarning);
            });
          }}
        />
        {list.length === 0 ? (
          <p className="text-xs text-fg-muted">
            {doc.loading && !keys
              ? L("Chargement…", "Loading…")
              : L(
                  "Aucun titre pour l'instant. Cherchez-en un ci-dessus.",
                  "No titles yet. Search above to add one.",
                )}
          </p>
        ) : (
          <ul className="space-y-1.5">
            {list.map((key) => (
              <VostfrRow
                key={key}
                keyId={key}
                hint={known[key]}
                disabled={busy}
                onRemove={(type) => void send(key, type, false)}
              />
            ))}
          </ul>
        )}
      </div>
    </SettingsSection>
  );
}

function VostfrRow({
  keyId,
  hint,
  disabled,
  onRemove,
}: {
  keyId: string;
  hint?: TitleCard;
  disabled: boolean;
  onRemove: (type: string) => void;
}) {
  const loaded = useTitle(hint ? undefined : keyId);
  const card = hint ?? loaded;
  const name = card?.title ?? keyId;
  return (
    <li className="flex items-center gap-2 rounded-md border border-border-subtle bg-bg-subtle p-1.5">
      <Artwork src={card?.poster} alt="" title="" className="h-12 w-8 shrink-0 rounded" />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm text-fg">{name}</span>
        {card ? (
          <span className="block truncate text-xs text-fg-muted">{titleSubtitle(card)}</span>
        ) : null}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="size-7 hover:text-danger"
        disabled={disabled}
        onClick={() => onRemove(card?.type ?? "")}
        aria-label={L(`Retirer ${name}`, `Remove ${name}`)}
      >
        <X size={14} />
      </Button>
    </li>
  );
}
