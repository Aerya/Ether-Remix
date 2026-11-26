import {
  Alert,
  AlertDescription,
  Badge,
  Button,
  cn,
  promptDialog,
  toast,
} from "@braedonsaunders/appkit-ui";
import {
  ArrowRight,
  ArrowUpToLine,
  Clapperboard,
  Globe,
  History,
  Home,
  Library as LibraryIcon,
  ListOrdered,
  Lock,
  Monitor,
  MonitorSmartphone,
  Pencil,
  Plus,
  Smartphone,
  Tv,
  User,
  X,
} from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { Section } from "../../components/layout";
import { DragHandle, SortableList } from "../../components/Sortable";
import { t } from "../../i18n";
import { api } from "../../lib/api";
import { useAsync } from "../../lib/hooks";
import {
  awaySameAsHome,
  deviceIconGroup,
  fallbackRule,
  fallbackTitle,
  ruleConditions,
  ruleLimits,
  ruleOrigin,
  neverPicksProfile,
  ruleTitle,
  type ConditionKind,
  type FallbackRow,
} from "../../lib/qualityRules";
import type { QualityDevices, QualityRule } from "../../lib/types";
import { OrderButtons, toastError } from "../sources/form";
import { QualityHelp } from "./QualityHelp";
import { RuleEditor, localizedRule, ruleTarget, useRuleNames, type RuleTarget } from "./RuleDrawer";
import { useQuality } from "./useQuality";

function conditionIcon(kind: ConditionKind, rule: QualityRule, devices: QualityDevices | null) {
  switch (kind) {
    case "library":
      return LibraryIcon;
    case "viewer":
      return User;
    case "device": {
      const group = deviceIconGroup(rule.devices, devices);
      if (group === "tv") return Tv;
      if (group === "mobile") return Smartphone;
      if (group === "computer") return Monitor;
      return MonitorSmartphone;
    }
    case "where":
      return rule.where === "away" ? Globe : Home;
    case "kind":
      return Clapperboard;
  }
}

function Outcome({
  profile,
  limits,
  emptyLabel,
}: {
  profile: string;
  limits: string[];
  emptyLabel?: string;
}) {
  return (
    <span className="flex flex-wrap items-center gap-1.5">
      {profile ? (
        <span className="inline-flex items-center gap-1.5 text-sm font-medium text-fg">
          <ArrowRight aria-hidden size={14} className="shrink-0 text-fg-muted" />
          <span>{profile}</span>
        </span>
      ) : (
        <span className="text-sm text-fg-muted">{emptyLabel ?? t("Limit only")}</span>
      )}
      {limits.map((limit) => (
        <Badge key={limit} variant="secondary">
          {limit}
        </Badge>
      ))}
    </span>
  );
}

/** Which 0.3.20 setting a row came from; shown while the upgrade note is up. */
function Origin({ text }: { text: string }) {
  if (!text) return null;
  return (
    <p className="mt-1.5 flex items-center gap-1.5 text-xs text-fg-muted">
      <History aria-hidden size={12} className="shrink-0" />
      {text}
    </p>
  );
}

/**
 * Releases › Rules: the one ordered list that decides who gets which profile.
 * The two fallback rows fixed at the bottom are rules too, and open in the
 * same drawer.
 */
export function RulesTab() {
  const { config, quality, commit } = useQuality();
  const names = useRuleNames();
  const devices = useAsync((signal) => api.qualityDevices(signal), []);
  const [params, setParams] = useSearchParams();
  const [target, setTarget] = useState<RuleTarget | null>(null);
  /** The order being saved, shown until the list reloads. */
  const [moved, setMoved] = useState<QualityRule[] | null>(null);
  const [migratedDismissed, setMigratedDismissed] = useState(false);

  // Links from other pages: ?rule=<id>, ?default=home|away, or
  // ?new=1&library=<id>&viewer=<id>&profile=<name>.
  useEffect(() => {
    if (!quality) return;
    const rules = quality.rules ?? [];
    const ruleId = params.get("rule");
    const row = params.get("default");
    if (ruleId) {
      const index = rules.findIndex((r) => r.id === ruleId);
      if (index >= 0) setTarget({ index, rule: rules[index]! });
    } else if (row === "home" || row === "away") {
      setTarget(ruleTarget(quality, { fallback: row }));
    } else if (params.get("new")) {
      const library = Number(params.get("library")) || undefined;
      const viewer = Number(params.get("viewer")) || undefined;
      setTarget({
        index: rules.length,
        rule: {
          id: "",
          ...(library ? { libraries: [library] } : {}),
          ...(viewer ? { viewers: [viewer] } : {}),
          profile: params.get("profile") ?? "",
        },
        lockLibrary: library,
        lockViewer: viewer,
      });
    } else {
      return;
    }
    setParams({}, { replace: true });
  }, [quality, params, setParams]);

  if (config.error) return <ErrorPanel error={config.error} onRetry={() => void config.reload()} />;
  if (!quality) return <LoadingRows rows={4} />;

  const rules = moved ?? quality.rules ?? [];
  const profiles = quality.profiles ?? [];
  const note = quality.migrationNote;
  const showMigratedNote = Boolean(note && !note.dismissed) && !migratedDismissed;

  function dismissMigratedNote() {
    setMigratedDismissed(true);
    if (!note || note.dismissed) return;
    void api
      .putSection("quality", { migrationNote: { ...note, dismissed: true } })
      .then(() => config.reload())
      .catch(() => {
        // Optimistic dismissal stays in place for this session.
      });
  }

  async function reorder(next: QualityRule[]) {
    if (!quality) return;
    setMoved(next);
    try {
      await commit({ ...quality, rules: next }, t("Rule order saved"));
    } catch (e) {
      toastError(t("Couldn't reorder rules"), e);
    } finally {
      setMoved(null);
    }
  }

  /** Moves the rule at `from` so it becomes rule number `to + 1`. */
  function moveTo(from: number, to: number) {
    if (to === from || to < 0 || to >= rules.length) return;
    const next = [...rules];
    const [rule] = next.splice(from, 1);
    next.splice(to, 0, rule!);
    void reorder(next);
  }

  async function moveToPosition(from: number) {
    const answer = await promptDialog({
      title: t("Move rule {n}", { n: from + 1 }),
      label: t("New position (1 to {max})", { max: rules.length }),
      initialValue: String(from + 1),
      confirmLabel: t("Move"),
    });
    if (answer === null) return;
    const to = Number(answer);
    if (!Number.isInteger(to) || to < 1 || to > rules.length) {
      toast.error(t("Enter a position from 1 to {max}.", { max: rules.length }));
      return;
    }
    moveTo(from, to - 1);
  }

  const fixedRow = (row: FallbackRow) => {
    const same = row === "away" && awaySameAsHome(quality);
    const outcome = fallbackRule(quality, row);
    const WhereIcon = row === "home" ? Home : Globe;
    return (
      <li className="flex flex-wrap items-center gap-x-3 gap-y-2 bg-bg-subtle/40 px-3 py-3 sm:px-5">
        <span className="flex w-10 shrink-0 items-center justify-end pr-0.5 text-fg-muted">
          <Lock aria-hidden size={14} />
        </span>
        <div className="min-w-0 flex-1 basis-56">
          <p className="flex items-center gap-1.5 text-sm font-medium text-fg">
            <WhereIcon aria-hidden size={14} className="shrink-0 text-fg-muted" />
            {fallbackTitle(row)}
          </p>
          <p className="mt-1 text-xs text-fg-muted">{t("Everything no rule above caught")}</p>
          {showMigratedNote && !same ? (
            <Origin
              text={row === "home" ? t("From your default profile") : t("From your away profile")}
            />
          ) : null}
        </div>
        {same ? (
          <span className="text-sm text-fg-muted">{t("Same as at home")}</span>
        ) : (
          <Outcome profile={outcome.profile ?? ""} limits={ruleLimits(outcome)} />
        )}
        <span className="flex items-center gap-1">
          <Button
            variant="outline"
            size="sm"
            onClick={() => setTarget(ruleTarget(quality, { fallback: row }))}
            aria-label={t("Edit {name}", { name: fallbackTitle(row) })}
          >
            <Pencil size={13} /> {t("Edit")}
          </Button>
        </span>
      </li>
    );
  };

  return (
    <>
      {showMigratedNote ? (
        <Alert variant="info" role="status" className="flex items-start justify-between gap-3">
          <AlertDescription>
            <p className="font-medium text-fg">
              {rules.some((r) => r.from?.kind === "awayLimit")
                ? t(
                    "Your quality settings from 0.3.20 were moved into these rules. Your away-from-home bitrate cap now also steers which release is picked.",
                  )
                : t(
                    "Your quality settings from 0.3.20 were moved into these rules. Nothing plays differently.",
                  )}
            </p>
            {rules.some((r) => r.from) ? (
              <p className="mt-1 text-fg-muted">
                {t("Each rule below says which setting it came from.")}
              </p>
            ) : null}
          </AlertDescription>
          <button
            type="button"
            aria-label={t("Dismiss")}
            onClick={dismissMigratedNote}
            className="shrink-0 rounded p-1 text-fg-muted hover:text-fg"
          >
            <X size={14} />
          </button>
        </Alert>
      ) : null}

      <Section
        title={t("Rules")}
        description={
          <QualityHelp
            summary={t(
              "Checked from the top. The first rule with a profile picks it; every matching limit applies.",
            )}
            detail={t(
              "A rule can pick a profile, set limits, or both. Limits only ever lower quality, and the strictest one wins. When no rule picks a profile, the two rows at the bottom do.",
            )}
          />
        }
        actions={
          <Button
            size="sm"
            onClick={() => setTarget({ index: rules.length, rule: { id: "" } })}
            disabled={!profiles.length}
          >
            <Plus size={14} /> {t("Add rule")}
          </Button>
        }
        bodyClassName=""
      >
        {rules.length === 0 ? (
          <p className="border-b border-border px-5 py-4 text-sm text-fg-muted">
            {t("No rules yet. Everything uses the two rows below.")}
          </p>
        ) : (
          <SortableList
            items={rules}
            getId={(r) => r.id || `new-${rules.indexOf(r)}`}
            onReorder={(next) => void reorder(next)}
            renderItem={(raw, handle, i) => {
              const r = localizedRule(raw);
              const conditions = ruleConditions(r, names);
              const title = ruleTitle(r, names);
              const named = Boolean(raw.name?.trim());
              const label = t("rule {n}", { n: i + 1 });
              const never = neverPicksProfile(rules, i, devices.data ?? null);
              return (
                <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border bg-surface px-3 py-3 sm:px-5">
                  <span className="flex w-10 shrink-0 items-center gap-1">
                    <DragHandle handle={handle} label={t("Reorder {name}", { name: label })} />
                    <span className="w-5 text-right text-sm tabular-nums text-fg-muted">
                      {i + 1}
                    </span>
                  </span>
                  <div className="min-w-0 flex-1 basis-56">
                    {/* An unnamed rule's title would only repeat its chips. */}
                    {named ? <p className="truncate text-sm font-medium text-fg">{title}</p> : null}
                    <div
                      className={cn("flex flex-wrap gap-1.5", named && "mt-1.5")}
                      aria-label={named ? undefined : title}
                    >
                      {conditions.length === 0 ? (
                        <Badge variant="outline">{t("Everything")}</Badge>
                      ) : (
                        conditions.map((c) => {
                          const Icon = conditionIcon(c.kind, raw, devices.data ?? null);
                          return (
                            <Badge key={c.kind} variant="outline" className="gap-1">
                              <Icon aria-hidden size={12} />
                              {c.label}
                            </Badge>
                          );
                        })
                      )}
                    </div>
                    {showMigratedNote ? <Origin text={ruleOrigin(raw)} /> : null}
                  </div>
                  <span className="flex flex-wrap items-center gap-2">
                    <Outcome profile={r.profile ?? ""} limits={ruleLimits(r)} />
                    {never ? <Badge variant="warning">{t("Never picks a profile")}</Badge> : null}
                  </span>
                  <span className="flex items-center gap-1">
                    <span className="flex">
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-8"
                        onClick={() => moveTo(i, 0)}
                        disabled={moved !== null || i === 0}
                        aria-label={t("Move {name} to top", { name: label })}
                        title={t("Move to top")}
                      >
                        <ArrowUpToLine size={14} />
                      </Button>
                      <OrderButtons
                        label={label}
                        i={i}
                        n={rules.length}
                        disabled={moved !== null}
                        onMove={(d) => moveTo(i, i + d)}
                      />
                      <Button
                        variant="ghost"
                        size="icon"
                        className="size-8"
                        onClick={() => void moveToPosition(i)}
                        disabled={moved !== null || rules.length < 2}
                        aria-label={t("Move {name} to position…", { name: label })}
                        title={t("Move to position…")}
                      >
                        <ListOrdered size={14} />
                      </Button>
                    </span>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setTarget({ index: i, rule: raw })}
                      aria-label={t("Edit rule {n}", { n: i + 1 })}
                    >
                      <Pencil size={13} /> {t("Edit")}
                    </Button>
                  </span>
                </div>
              );
            }}
          />
        )}
        <ul className="divide-y divide-border">
          {fixedRow("home")}
          {fixedRow("away")}
        </ul>
      </Section>
      <p className="text-sm text-fg-muted">
        {t("What a device can play is set in")}{" "}
        <Link className="text-primary underline" to="/playback/devices">
          {t("Playback › Devices")}
        </Link>
      </p>

      <RuleEditor
        quality={quality}
        commit={commit}
        target={target}
        onClose={() => setTarget(null)}
        onOpenRule={(index) => {
          const rule = (quality.rules ?? [])[index];
          if (rule) setTarget({ index, rule });
        }}
      />
    </>
  );
}
