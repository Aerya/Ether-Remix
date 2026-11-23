import { Badge, Button, cn, toast } from "@braedonsaunders/appkit-ui";
import { CalendarRange, Pencil, RefreshCw, Tv } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { DragHandle, SortableList } from "../../components/Sortable";
import { ErrorPanel } from "../../components/feedback";
import { t, tn } from "../../i18n";
import { errorMessage } from "../../lib/api";
import { relativeTime } from "../../lib/format";
import { useInterval } from "../../lib/hooks";
import { SourceDrawer } from "../livetv/SourceEditor";
import { ProviderAccountCard } from "../livetv/SourceGroups";
import { dayClock, isChannelSource, plexLineupName, tv, type Source } from "../livetv/model";
import { sourceImportError } from "../livetv/sourceErrors";

const STALE_MS = 6 * 60 * 60 * 1000;

/** The canonical connection editor for provider, playlist and guide sources.
 * Live TV reads and edits the same source records in place over its lineup. */
export function IPTVProviders({ sourceParam = "source" }: { sourceParam?: string } = {}) {
  const [params, setParams] = useSearchParams();
  const [sources, setSources] = useState<Source[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [refreshing, setRefreshing] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  useInterval(() => setNow(Date.now()), 30_000);

  const reload = useCallback(async () => {
    try {
      setSources(await tv.sources());
      setError(null);
    } catch (e) {
      setError(e);
    }
  }, []);
  // Embedded consoles may use `source` to select a category of providers.
  const setEditorParams = (next: URLSearchParams) => {
    if (sourceParam === "source") {
      setParams(next);
      return;
    }
    setParams((current) => {
      const merged = new URLSearchParams(current);
      merged.delete(sourceParam);
      merged.delete("new");
      next.forEach((value, key) => merged.set(key === "source" ? sourceParam : key, value));
      return merged;
    });
  };
  const id = params.get(sourceParam);
  const adding = params.get("new");
  useEffect(() => {
    void reload();
  }, [reload]);
  useEffect(() => {
    // A provider status refresh must not overwrite an owner's open draft.
    if (id || adding) return;
    const timer = window.setInterval(() => void reload(), 30000);
    return () => window.clearInterval(timer);
  }, [reload, id, adding]);
  const close = () => setEditorParams(new URLSearchParams());

  const refresh = async (source: Source) => {
    setRefreshing(source.id);
    const name = source.kind === "plex" ? plexLineupName(source.name) : source.name;
    try {
      await tv.refreshSource(source.id);
      toast.success(t("{name} refreshed", { name }));
      await reload();
    } catch (e) {
      toast.error(t("Refresh failed"), { description: sourceImportError(e) });
    } finally {
      setRefreshing(null);
    }
  };

  return (
    <>
      {error ? <ErrorPanel error={error} onRetry={() => void reload()} /> : null}
      {sources === null ? (
        <p className="text-sm text-fg-muted">{t("Loading…")}</p>
      ) : sources.length === 0 ? (
        <p className="text-sm text-fg-muted">{t("No channel sources yet")}</p>
      ) : (
        <div className="space-y-3">
          {sources.filter(isChannelSource).length > 1 ? (
            <p className="text-xs text-fg-muted">
              {t(
                "Drag providers to set failover priority when channels merge feeds across sources.",
              )}
            </p>
          ) : null}
          <SortableList
            items={sources}
            getId={(s) => s.id}
            onReorder={async (next) => {
              const prev = sources;
              setSources(next);
              try {
                setSources(await tv.saveSourceOrder(next.map((s) => s.id)));
              } catch (e) {
                setSources(prev);
                toast.error(errorMessage(e));
              }
            }}
            className="space-y-3"
            renderItem={(source, handle) => {
              const channelSources = sources.filter(isChannelSource);
              const channelRank = isChannelSource(source)
                ? channelSources.findIndex((s) => s.id === source.id) + 1
                : 0;
              const name = source.kind === "plex" ? plexLineupName(source.name) : source.name;
              const guideOnly = source.hasPlaylist === false && source.auth?.type !== "xtream";
              const urlBacked =
                source.kind === "plex" ||
                source.auth?.type === "xtream" ||
                Boolean(source.playlistUrl || source.guideUrl || source.playlistGuide);
              const busy = refreshing === source.id;
              const until = source.guideUntil ? Date.parse(source.guideUntil) : 0;
              return (
                <article className="flex flex-wrap items-start gap-3 rounded-xl border border-border bg-surface p-4 sm:flex-nowrap">
                  {sources.length > 1 ? (
                    <DragHandle
                      handle={handle}
                      label={t("Reorder {name}", { name: source.name })}
                      className="mt-1.5"
                    />
                  ) : null}
                  <span
                    className={cn(
                      "flex size-11 shrink-0 items-center justify-center rounded-lg",
                      guideOnly ? "bg-info/15 text-info" : "bg-bg-subtle text-fg-muted",
                    )}
                  >
                    {guideOnly ? <CalendarRange size={20} /> : <Tv size={20} />}
                  </span>
                  <div className="min-w-0 flex-1 space-y-2">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="min-w-0 truncate font-semibold text-fg" title={name}>
                        {name}
                      </h3>
                      {channelRank > 0 && channelSources.length > 1 ? (
                        <Badge>{t("Priority #{rank}", { rank: channelRank })}</Badge>
                      ) : null}
                      {source.disabled ? <Badge>{t("Disabled")}</Badge> : null}
                      <Badge>
                        {source.kind?.startsWith("plugin:")
                          ? t("Plugin source")
                          : source.auth?.type === "xtream"
                            ? t("Xtream Codes login")
                            : source.hasPlaylist
                              ? t("M3U playlist")
                              : t("Guide source")}
                      </Badge>
                    </div>
                    <div className="flex flex-wrap gap-x-4 gap-y-1 text-sm text-fg-muted">
                      {source.useLiveTV !== false ? (
                        <>
                          <span>
                            {guideOnly
                              ? tn(
                                  source.guideChannelCount ?? 0,
                                  "{count} guide channel",
                                  "{count} guide channels",
                                )
                              : tn(source.channelCount ?? 0, "{count} channel", "{count} channels")}
                          </span>
                          {!guideOnly && (source.guideChannelCount ?? 0) > 0 ? (
                            <span>
                              {tn(
                                source.guideChannelCount ?? 0,
                                "{count} guide channel",
                                "{count} guide channels",
                              )}
                            </span>
                          ) : null}
                          {until > 0 ? (
                            <span
                              className={cn(
                                (until <= now || until - now < STALE_MS) && "text-warning",
                              )}
                            >
                              {until <= now
                                ? t("Listings have run out")
                                : t("to {time}", { time: dayClock(new Date(until)) })}
                            </span>
                          ) : null}
                        </>
                      ) : null}
                      {source.useVOD ? (
                        <span>
                          {t("Movies & shows index")}:{" "}
                          {tn(source.vodStatus?.movies ?? 0, "{count} movie", "{count} movies")} ·{" "}
                          {tn(source.vodStatus?.series ?? 0, "{count} show", "{count} shows")}
                        </span>
                      ) : null}
                      {source.updatedAt ? (
                        <span>
                          {t("refreshed {when}", { when: relativeTime(source.updatedAt) })}
                        </span>
                      ) : null}
                    </div>
                    {source.logins?.length ? (
                      <details className="text-sm">
                        <summary className="cursor-pointer text-fg-muted">
                          {t("Provider account")}
                        </summary>
                        <div className="mt-3 space-y-3">
                          {[{ id: "primary", account: source.account }, ...source.logins].map(
                            (login, i) => (
                              <div key={login.id} className="space-y-1">
                                <p className="font-medium">
                                  {t("Login {number}", { number: i + 1 })}
                                </p>
                                {login.account ? (
                                  <ProviderAccountCard account={login.account} />
                                ) : null}
                              </div>
                            ),
                          )}
                        </div>
                      </details>
                    ) : null}
                    {source.lastError ? (
                      <p role="alert" className="break-words text-sm text-danger">
                        {sourceImportError(source.lastError)}
                      </p>
                    ) : source.vodStatus?.error ? (
                      <p role="alert" className="text-sm text-danger">
                        {t("Provider unavailable; retry scheduled")}
                      </p>
                    ) : null}
                  </div>
                  <div className="flex shrink-0 flex-wrap items-center gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={busy || !urlBacked || Boolean(source.disabled)}
                      title={
                        urlBacked ? undefined : t("Uploaded files can't be refreshed; upload again")
                      }
                      onClick={() => void refresh(source)}
                    >
                      <RefreshCw size={13} className={cn(busy && "animate-spin")} />
                      {t("Refresh")}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      aria-label={t("Edit {name}", { name: source.name })}
                      onClick={() => setEditorParams(new URLSearchParams({ source: source.id }))}
                    >
                      <Pencil size={13} />
                      {t("Edit")}
                    </Button>
                  </div>
                </article>
              );
            }}
          />
        </div>
      )}
      <SourceDrawer
        sourceId={id}
        adding={adding}
        sources={sources}
        onClose={close}
        onSaved={async (s) => {
          await reload();
          setEditorParams(new URLSearchParams({ source: s.id }));
        }}
        onDeleted={async () => {
          close();
          await reload();
        }}
      />
    </>
  );
}
