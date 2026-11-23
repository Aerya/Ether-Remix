import {
  Alert,
  AlertDescription,
  AlertTitle,
  Button,
  Textarea,
  cn,
  toast,
} from "@braedonsaunders/appkit-ui";
import { ArrowDown, Sparkles } from "lucide-react";
import { useState } from "react";
import { Link, useInRouterContext } from "react-router-dom";
import { Drawer } from "../../components/Drawer";
import { localized, t, tx } from "../../i18n";
import { ApiError, errorMessage } from "../../lib/api";
import { flowsApi, type DescribeFlowResult } from "../../lib/flowsApi";
import { categoryTone, iconForNode } from "./shared";
import { nodeKindLabel } from "./types";

const EXAMPLES = localized(() => [
  t("Stop video transcodes for viewers away from home"),
  t(
    "Tell me when a viewer can't play something, but not more than once an hour for the same title",
  ),
  t("Send a weekly 'new this week' newsletter on Fridays"),
  t("Dim the cinema lights when a movie starts on the living-room TV"),
  t("Stop streams that have been paused for ages"),
]);

/**
 * “Describe it”: say what should happen in plain words and review the flow
 * Ether drafts — the same prompt-first pattern as Collections' Describe-it.
 * Nothing is saved until the draft is opened in the editor and saved there.
 */
export function DescribeFlowDialog({
  open,
  onClose,
  onDraft,
  applyLabel,
}: {
  open: boolean;
  onClose: () => void;
  onDraft: (draft: DescribeFlowResult) => void;
  applyLabel?: string;
}) {
  const inRouter = useInRouterContext();
  const [prompt, setPrompt] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [draft, setDraft] = useState<DescribeFlowResult | null>(null);

  const aiUnavailable = error instanceof ApiError && error.code === "ai_unavailable";
  const errorFields = error instanceof ApiError ? (error.fields ?? []) : [];

  const build = async (text: string) => {
    const clean = text.trim();
    if (!clean) return;
    setBusy(true);
    setError(null);
    try {
      setDraft(await flowsApi.describe(clean));
    } catch (e) {
      setError(e);
      if (!(e instanceof ApiError && e.code === "ai_unavailable")) {
        toast.error(errorMessage(e));
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={t("Describe it")}
      size="xl"
      footer={
        draft ? (
          <div className="flex flex-wrap justify-end gap-2">
            <Button variant="ghost" onClick={() => setDraft(null)}>
              {t("Describe something else")}
            </Button>
            <Button
              onClick={() => {
                onDraft(draft);
                onClose();
              }}
            >
              {applyLabel ?? t("Open in editor")}
            </Button>
          </div>
        ) : undefined
      }
    >
      <div className="space-y-5">
        {!draft ? (
          <>
            <p className="text-sm text-fg-muted">
              {t(
                "Say what should happen, in your own words. Ether drafts the flow — trigger, checks and actions — and you review it on the canvas before anything runs.",
              )}
            </p>
            <Textarea
              aria-label={t("What should happen?")}
              value={prompt}
              rows={4}
              autoFocus
              placeholder={t("When…, then… (e.g. stop 4K transcodes for viewers away from home)")}
              onChange={(e) => setPrompt(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) void build(prompt);
              }}
            />
            <div className="flex flex-wrap gap-1.5">
              {EXAMPLES.map((ex) => (
                <button
                  key={ex}
                  type="button"
                  onClick={() => setPrompt(ex)}
                  className="rounded-full border border-border px-2.5 py-1 text-xs text-fg-muted transition-colors hover:border-primary hover:text-fg"
                >
                  {ex}
                </button>
              ))}
            </div>
            {aiUnavailable ? (
              <Alert variant="info" role="alert">
                <AlertTitle>{t("Describe it needs a language model")}</AlertTitle>
                <AlertDescription>
                  {tx("Configure AI in {integrations} to describe flows in plain language.", {
                    integrations: inRouter ? (
                      <Link to="/services" className="font-medium underline" onClick={onClose}>
                        {t("Integrations")}
                      </Link>
                    ) : (
                      <a href="/services" className="font-medium underline">
                        {t("Integrations")}
                      </a>
                    ),
                  })}
                </AlertDescription>
              </Alert>
            ) : error ? (
              <Alert variant="destructive" role="alert">
                <AlertTitle>{t("Couldn't draft that flow")}</AlertTitle>
                <AlertDescription>
                  {errorFields.length ? (
                    <ul className="mt-1 list-disc space-y-0.5 pl-5">
                      {errorFields.map((f, i) => (
                        <li key={`${f.field}-${i}`}>
                          <code className="text-xs">{f.field}</code> {f.message}
                        </li>
                      ))}
                    </ul>
                  ) : (
                    errorMessage(error)
                  )}
                </AlertDescription>
              </Alert>
            ) : null}
            <Button disabled={busy || !prompt.trim()} onClick={() => void build(prompt)}>
              <Sparkles size={15} /> {busy ? t("Drafting…") : t("Draft my flow")}
            </Button>
          </>
        ) : (
          <>
            <div>
              <h3 className="text-base font-semibold text-fg">{draft.name}</h3>
              <p className="mt-1 text-sm text-fg-muted">{draft.description}</p>
            </div>
            <div className="rounded-xl bg-primary-subtle/60 px-3.5 py-3">
              <p className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-primary">
                <Sparkles size={13} /> {t("How it works")}
              </p>
              <p className="mt-1.5 text-sm text-fg">{draft.explanation}</p>
            </div>
            <div>
              <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-fg-subtle">
                {t("The draft, step by step")}
              </p>
              <ol className="space-y-1">
                {draft.graph.nodes.map((n, i) => {
                  const Icon = iconForNode(n.kind, String(n.config?.actionType ?? "") || undefined);
                  const tone = categoryTone(n.kind);
                  return (
                    <li key={n.id}>
                      <div className="flex items-center gap-2.5 rounded-lg border border-border bg-surface px-3 py-2">
                        <span
                          className={cn(
                            "grid size-8 shrink-0 place-items-center rounded-lg",
                            tone.chip,
                          )}
                        >
                          <Icon size={15} />
                        </span>
                        <span className="min-w-0">
                          <span className="block truncate text-sm font-medium text-fg">
                            {n.label || nodeKindLabel(n.kind)}
                          </span>
                          <span className="block text-xs text-fg-subtle">
                            {nodeKindLabel(n.kind)}
                          </span>
                        </span>
                      </div>
                      {i < draft.graph.nodes.length - 1 ? (
                        <span className="flex justify-center py-0.5 text-fg-subtle">
                          <ArrowDown size={13} />
                        </span>
                      ) : null}
                    </li>
                  );
                })}
              </ol>
            </div>
            <p className="text-sm text-fg-muted">
              {t(
                "Nothing is saved yet — open it in the editor to adjust the details, test it, and save.",
              )}
            </p>
          </>
        )}
      </div>
    </Drawer>
  );
}
