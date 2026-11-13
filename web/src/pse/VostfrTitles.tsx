import { Badge, Button, Switch, toast } from "@braedonsaunders/appkit-ui";
import { SettingsSection } from "../components/CollapsibleSections";
import { X } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
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

const SUBMAKER = "/pse/submaker/vostfr";

/** État des sous-titres d'un titre VOSTFR (internal/pse/submaker/vostfr.go). */
type SubtitleState =
  | "french-audio"
  | "announced"
  | "ready"
  | "translated"
  | "pending"
  | "failed"
  | "none"
  | "unindexed";

interface SubmakerVostfr {
  available: boolean;
  enabled: boolean;
  titles: { key: string; state: SubtitleState }[];
  queued?: number;
}

const STATES: Record<
  SubtitleState,
  { fr: string; en: string; variant: "success" | "secondary" | "warning" }
> = {
  "french-audio": { fr: "VF disponible", en: "French audio", variant: "success" },
  announced: {
    fr: "Sous-titres FR annoncés",
    en: "French subtitles announced",
    variant: "success",
  },
  ready: { fr: "SRT français prêt", en: "French SRT ready", variant: "success" },
  translated: { fr: "SRT traduit (IA)", en: "Translated SRT (AI)", variant: "success" },
  pending: { fr: "SRT en préparation", en: "Preparing SRT", variant: "secondary" },
  failed: { fr: "Échec, nouvel essai plus tard", en: "Failed, retried later", variant: "warning" },
  none: { fr: "Sans sous-titres FR", en: "No French subtitles", variant: "warning" },
  unindexed: { fr: "Aucune release indexée", en: "No release indexed", variant: "secondary" },
};

/** Interrupteur SubMaker de l'onglet VOSTFR ; absent si SubMaker n'est pas chargé. */
function SubmakerSwitch({
  data,
  onChange,
}: {
  data: SubmakerVostfr;
  onChange: (next: SubmakerVostfr) => void;
}) {
  const [busy, setBusy] = useState(false);
  const toggle = async (enabled: boolean) => {
    setBusy(true);
    try {
      const next = await apiFetch<SubmakerVostfr>(SUBMAKER, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      });
      onChange(next);
      if (enabled && next.queued)
        toast.success(
          L(
            `${next.queued} recherche(s) relancée(s) pour préparer les sous-titres`,
            `${next.queued} search(es) queued to prepare subtitles`,
          ),
        );
    } catch (e) {
      toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="flex items-start gap-3 rounded-md border border-border-subtle p-3">
      <Switch
        checked={data.enabled}
        disabled={busy || (!data.available && !data.enabled)}
        onChange={(e) => void toggle(e.currentTarget.checked)}
        aria-label={L(
          "Préparer les sous-titres français avec SubMaker",
          "Prepare French subtitles with SubMaker",
        )}
      />
      <div className="min-w-0 text-sm">
        <p className="font-medium text-fg">
          {L(
            "Préparer les sous-titres français avec SubMaker",
            "Prepare French subtitles with SubMaker",
          )}
        </p>
        <p className="text-xs text-fg-muted">
          {data.available
            ? L(
                "Pour ces titres, SubMaker cherche un SRT français correspondant à la release, ou traduit un SRT, dans la limite de son budget quotidien. Une règle de collection explicite dans SubMaker reste prioritaire.",
                "For these titles, SubMaker looks for a French SRT matching the release, or translates one, within its daily budget. An explicit collection rule in SubMaker still wins.",
              )
            : L(
                "Activez d’abord SubMaker avec un manifeste valide.",
                "Enable SubMaker with a valid manifest first.",
              )}{" "}
          {!data.available ? (
            <Link
              className="text-primary underline"
              to="/remix/sources?source=stremio#submaker-integration"
            >
              {L("Réglages SubMaker", "SubMaker settings")}
            </Link>
          ) : null}
        </p>
      </div>
    </div>
  );
}

interface VostfrData {
  keys: string[];
  max: number;
}

export function VostfrTitles() {
  const doc = useAsync(() => apiFetch<VostfrData>(ENDPOINT), []);
  const subs = useAsync(() => apiFetch<SubmakerVostfr>(SUBMAKER).catch(() => null), []);
  const states = new Map((subs.data?.titles ?? []).map((t) => [t.key, t.state]));
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
      void subs.reload();
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
        {subs.data ? (
          <SubmakerSwitch data={subs.data} onChange={(next) => subs.setData(next)} />
        ) : null}
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
                subtitles={subs.data?.enabled ? states.get(key) : undefined}
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
  subtitles,
  disabled,
  onRemove,
}: {
  keyId: string;
  hint?: TitleCard;
  subtitles?: SubtitleState;
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
      {subtitles ? (
        <Badge variant={STATES[subtitles].variant} className="shrink-0">
          {L(STATES[subtitles].fr, STATES[subtitles].en)}
        </Badge>
      ) : null}
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
