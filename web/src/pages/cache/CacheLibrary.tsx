import {
  Badge,
  Button,
  Input,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@braedonsaunders/appkit-ui";
import { Search, Upload } from "lucide-react";
import { useEffect, useState } from "react";
import { Drawer } from "../../components/Drawer";
import { ErrorPanel, LoadingRows } from "../../components/feedback";
import { ToolbarPicker } from "../../components/ToolbarPicker";
import { t } from "../../i18n";
import { apiFetch, ApiError } from "../../lib/api";
import { formatBytes, formatDateTime, formatNumber } from "../../lib/format";
import { useAsync, useDebouncedValue, useInterval, useMediaQuery } from "../../lib/hooks";

const base = "/cache/vault";
type Release = {
  key: string;
  name: string;
  kind: string;
  size: number;
  added: string;
  lastUsed: string;
  uses: number;
  health: string;
  source: string;
};
type Page = { rows: Release[]; nextCursor?: string };
type Progress = {
  id: string;
  state: "running" | "done" | "failed";
  imported: number;
  failed: number;
};
function health(value: string) {
  return value === "ok" ? t("Healthy") : value === "dead" ? t("Unavailable") : t("Unknown");
}
function fields(r: Release) {
  return [
    [t("Name"), r.name],
    [t("Kind"), r.kind === "usenet" ? t("Usenet") : t("Torrent")],
    [t("Size"), formatBytes(r.size)],
    [t("Added"), formatDateTime(r.added)],
    [
      t("Last used"),
      r.lastUsed && !r.lastUsed.startsWith("0001") ? formatDateTime(r.lastUsed) : "—",
    ],
    [t("Uses"), formatNumber(r.uses)],
    [t("Health"), <Badge variant="secondary">{health(r.health)}</Badge>],
    [t("Source"), r.source || "—"],
  ] as const;
}

export function CacheLibrary() {
  const [search, setSearch] = useState(""),
    [kind, setKind] = useState(""),
    [status, setStatus] = useState("");
  const compact = useMediaQuery("(max-width: 639px)");
  const query = useDebouncedValue(search, 200);
  const [pages, setPages] = useState<string[]>([""]);
  const [row, setRow] = useState<Release | null>(null),
    [importing, setImporting] = useState(false);
  const [busy, setBusy] = useState(false),
    [error, setError] = useState<unknown>(null),
    [confirm, setConfirm] = useState(false);
  const list = useAsync(
    (signal) =>
      apiFetch<Page>(
        base +
          "/releases?" +
          new URLSearchParams({
            q: query,
            kind,
            health: status,
            cursor: pages.at(-1) ?? "",
            limit: "50",
          }),
        { signal },
      ),
    [query, kind, status, pages],
  );
  const [path, setPath] = useState(""),
    [file, setFile] = useState<File | null>(null),
    [job, setJob] = useState("");
  const progress = useAsync(
    (signal) =>
      job
        ? apiFetch<Progress>(base + "/import/" + encodeURIComponent(job), { signal })
        : Promise.resolve(null),
    [job],
  );
  const current = progress.data?.id === job ? progress.data : null;
  const jobExpired = progress.error instanceof ApiError && progress.error.status === 404;
  const tracking = !!job && !jobExpired && (!current || current.state === "running");
  useInterval(() => void progress.reload(), tracking ? 1500 : null);
  useEffect(() => {
    if (current && current.state !== "running") void list.reload();
  }, [current?.state, current?.id, list.reload]);
  function reset() {
    setError(null);
    setConfirm(false);
  }
  async function act(action: "check" | "remove" | "import") {
    setBusy(true);
    setError(null);
    try {
      if (action === "import") {
        let body: FormData | string = JSON.stringify({ path: path.trim() });
        if (file) {
          body = new FormData();
          body.append("file", file);
        }
        const result = await apiFetch<{ id: string }>(base + "/import", { method: "POST", body });
        setJob(result.id);
      } else if (row) {
        const url = base + "/" + encodeURIComponent(row.key);
        if (action === "check") {
          const result = await apiFetch<{ health: string }>(url + "/check", { method: "POST" });
          setRow({ ...row, health: result.health });
        } else {
          await apiFetch(url, { method: "DELETE" });
          setRow(null);
        }
        await list.reload();
      }
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }
  const headings = [
    t("Name"),
    t("Kind"),
    t("Size"),
    t("Added"),
    t("Last used"),
    t("Uses"),
    t("Health"),
    t("Source"),
  ];
  return (
    <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-border bg-surface">
      <div className="flex flex-wrap items-center gap-3 border-b border-border p-4">
        <div className="relative min-w-48 flex-1">
          <Search size={14} className="absolute left-3 top-3 text-fg-subtle" />
          <Input
            className="pl-9"
            aria-label={t("Search releases")}
            placeholder={t("Search releases")}
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPages([""]);
            }}
          />
        </div>
        <ToolbarPicker
          value={kind}
          ariaLabel={t("Kind")}
          onChange={(v) => {
            setKind(v);
            setPages([""]);
          }}
          options={[
            { value: "", label: t("All kinds") },
            { value: "usenet", label: t("Usenet") },
            { value: "torrent", label: t("Torrent") },
          ]}
        />
        <ToolbarPicker
          value={status}
          ariaLabel={t("Health")}
          onChange={(v) => {
            setStatus(v);
            setPages([""]);
          }}
          options={[
            { value: "", label: t("Any health") },
            ...["ok", "unknown", "dead"].map((value) => ({ value, label: health(value) })),
          ]}
        />
        <Button
          variant="secondary"
          onClick={() => {
            reset();
            setImporting(true);
          }}
        >
          <Upload size={15} />
          {t("Import an existing setup")}
        </Button>
      </div>
      <div className="app-scroll min-h-0 flex-1 overflow-auto">
        {list.error ? (
          <ErrorPanel error={list.error} onRetry={list.reload} />
        ) : list.loading ? (
          <LoadingRows />
        ) : compact && list.data?.rows?.length ? (
          <ul className="divide-y divide-border">
            {list.data.rows.map((r) => (
              <li key={r.key}>
                <button
                  className="block w-full space-y-2 p-4 text-left"
                  onClick={() => {
                    reset();
                    setRow(r);
                  }}
                >
                  <span className="block truncate font-medium">{r.name}</span>
                  <span className="flex flex-wrap items-center gap-2 text-xs text-fg-muted">
                    {r.kind === "usenet" ? t("Usenet") : t("Torrent")} · {formatBytes(r.size)}{" "}
                    <Badge variant="secondary">{health(r.health)}</Badge>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                {headings.map((h) => (
                  <TableHead key={h}>{h}</TableHead>
                ))}
              </TableRow>
            </TableHeader>
            <TableBody>
              {list.data?.rows?.map((r) => (
                <TableRow
                  className="cursor-pointer"
                  key={r.key}
                  onClick={() => {
                    reset();
                    setRow(r);
                  }}
                >
                  {fields(r).map(([h, v], i) => (
                    <TableCell key={h} className="whitespace-nowrap">
                      {i === 0 ? (
                        <button className="max-w-96 truncate font-medium" title={r.name}>
                          {r.name}
                        </button>
                      ) : (
                        v
                      )}
                    </TableCell>
                  ))}
                </TableRow>
              ))}
              {!list.data?.rows?.length && (
                <TableRow>
                  <TableCell colSpan={8} className="py-10 text-center text-fg-muted">
                    {t("No releases found")}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        )}
      </div>
      {(pages.length > 1 || list.data?.nextCursor) && (
        <div className="flex justify-end gap-2 border-t border-border p-3">
          <Button
            disabled={list.loading || pages.length === 1}
            onClick={() => setPages(pages.slice(0, -1))}
          >
            {t("Previous")}
          </Button>
          <Button
            disabled={list.loading || !list.data?.nextCursor}
            onClick={() => setPages([...pages, list.data!.nextCursor!])}
          >
            {t("Next")}
          </Button>
        </div>
      )}
      <Drawer
        open={!!row}
        onClose={() => {
          if (!busy) setRow(null);
        }}
        title={row?.name}
      >
        <div className="space-y-4">
          {row &&
            fields(row)
              .slice(1)
              .map(([k, v]) => (
                <div
                  key={k}
                  className="flex justify-between gap-3 border-b border-border pb-2 text-sm"
                >
                  <span className="text-fg-muted">{k}</span>
                  {v}
                </div>
              ))}
          <ErrorPanel error={error} />
          <div className="flex gap-2">
            <Button disabled={busy} onClick={() => void act("check")}>
              {t("Check now")}
            </Button>
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() => (confirm ? void act("remove") : setConfirm(true))}
            >
              {confirm ? t("Confirm removal") : t("Remove")}
            </Button>
          </div>
        </div>
      </Drawer>
      <Drawer
        open={importing}
        onClose={() => {
          if (!busy) setImporting(false);
        }}
        title={t("Import an existing setup")}
      >
        <div className="space-y-5">
          <label className="block space-y-2 text-sm">
            <span>{t("SABnzbd, NZBGet or folder path")}</span>
            <Input value={path} disabled={!!file} onChange={(e) => setPath(e.target.value)} />
          </label>
          <label className="block space-y-2 text-sm">
            <span>{t("ZIP file")}</span>
            <Input
              type="file"
              accept=".zip"
              onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            />
          </label>
          <Button
            disabled={busy || (!file && !path.trim()) || tracking}
            onClick={() => void act("import")}
          >
            {t("Import")}
          </Button>
          <ErrorPanel error={error || progress.error} />
          {job && !jobExpired && (
            <p role="status">
              {current?.state === "done"
                ? t("Complete")
                : current?.state === "failed"
                  ? t("Failed")
                  : t("Importing…")}{" "}
              {current &&
                t("{imported} imported · {failed} failed", {
                  imported: current.imported,
                  failed: current.failed,
                })}
            </p>
          )}
        </div>
      </Drawer>
    </section>
  );
}
