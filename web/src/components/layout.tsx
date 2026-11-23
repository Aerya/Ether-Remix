import { CollapsibleBlock, CollapsibleSections } from "./CollapsibleSections";
import { PageContainer, SubtabNav as AppKitSubtabNav, cn } from "@braedonsaunders/appkit-ui";
import { useContext, useRef, type ReactNode, type ComponentProps, type RefObject } from "react";
import { OverflowTabs } from "./OverflowTabs";
import { useNavigate, useParams } from "react-router-dom";
import { t } from "../i18n";
import { useFallbackDocumentTitle } from "../lib/document-title";

export interface PageTab {
  key: string;
  label: string;
  count?: number;
}

/**
 * The active tab of a domain page, read from the `:tab` route segment so every
 * tab is deep-linkable (search, health links, notifications all land on it).
 */
export function useRouteTab<K extends string>(tabs: readonly { key: K }[], fallback?: K): K {
  const { tab } = useParams();
  const match = tabs.find((x) => x.key === tab);
  return (match?.key ?? fallback ?? tabs[0]!.key) as K;
}

/**
 * One layout for every multi-section page: header, optional live status strip,
 * URL-backed tabs, then the active tab's content edited in place.
 */
export function DomainPage({
  title,
  description,
  actions,
  status,
  base,
  tabs,
  active,
  tabActions,
  tabHref,
  children,
  className,
  fit = false,
}: {
  title: string;
  description?: string;
  actions?: ReactNode;
  /** Controls for the active tab (filters), on the right of the tab row. */
  tabActions?: ReactNode;
  /** Live state shown above the tabs (health, budgets, now playing). */
  status?: ReactNode;
  /** Route prefix; tabs navigate to `${base}/${key}`. */
  base: string;
  /** Where a tab navigates instead, e.g. to carry a period shared by two tabs. */
  tabHref?: (key: string) => string;
  tabs?: readonly PageTab[];
  active?: string;
  children: ReactNode;
  className?: string;
  /**
   * The active tab fills the viewport height on desktop instead of the page
   * scrolling (dashboards, full-height tables). Children get a flex column
   * that takes the remaining height; give them min heights so a very short
   * window scrolls rather than crushing them.
   */
  fit?: boolean;
}) {
  const navigate = useNavigate();
  const tabsRoot = useRef<HTMLDivElement>(null);
  const activeKey = active ?? tabs?.[0]?.key;
  const activeTabLabel =
    tabs && tabs.length > 1 && activeKey && activeKey !== "overview" && activeKey !== "flows"
      ? tabs.find((x) => x.key === activeKey)?.label
      : undefined;
  useFallbackDocumentTitle([title, activeTabLabel]);
  const head = (
    <>
      <div className="space-y-2">
        <header className="flex flex-wrap items-center justify-between gap-x-3 gap-y-2 sm:items-end sm:gap-x-4">
          <div className="min-w-0 flex-[1_1_12rem] space-y-1">
            <h1 className="truncate text-xl font-semibold text-fg sm:text-2xl">{title}</h1>
            {description ? (
              <p className="hidden text-sm text-fg-muted sm:block">{description}</p>
            ) : null}
          </div>
          {actions ? (
            <div className="flex min-w-0 max-w-full flex-wrap items-center gap-2 sm:ml-auto sm:justify-end">
              {actions}
            </div>
          ) : null}
        </header>
      </div>
      {status}
      {tabs && tabs.length > 1 ? (
        <div
          ref={tabsRoot}
          className="flex flex-wrap items-end gap-x-4 gap-y-2 border-b border-border"
        >
          {/* Narrow screens wrap the tab actions onto their own line before the tabs collapse. */}
          <div className="min-w-[min(100%,14rem)] flex-1">
            <OverflowTabs
              ariaLabel={t("{page} sections", { page: title })}
              active={active ?? tabs[0]!.key}
              onSelect={(key) =>
                keepScroll(tabsRoot, () => navigate(tabHref?.(key) ?? `${base}/${key}`))
              }
              tabs={tabs.map((x) => ({ key: x.key, label: x.label, count: x.count }))}
            />
          </div>
          {tabActions ? (
            <div className="flex max-w-full shrink-0 flex-wrap items-center gap-2 pb-2">
              {tabActions}
            </div>
          ) : null}
        </div>
      ) : null}
    </>
  );
  if (fit) {
    return (
      <div className="app-scroll flex min-h-0 flex-1 flex-col overflow-y-auto">
        <div
          className={cn(
            "reveal mx-auto flex w-full max-w-(--breakpoint-2xl) flex-1 flex-col gap-6 p-4 sm:p-6 lg:min-h-0",
            className,
          )}
        >
          {head}
          <div className="flex flex-1 flex-col lg:min-h-0">{children}</div>
        </div>
      </div>
    );
  }
  return (
    <PageContainer className={cn("space-y-6", className)}>
      {head}
      <div className="space-y-6 pb-4">{children}</div>
    </PageContainer>
  );
}

/**
 * A titled block of in-place content. Use AppKit `SettingsSection` +
 * `SettingsRow` for label/control rows; use this for free-form content (lists,
 * previews, tools) that still needs the same card chrome.
 */
export function Section({
  title,
  description,
  actions,
  footer,
  children,
  className,
  bodyClassName,
  id,
}: {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  footer?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
  id?: string;
}) {
  const collapsible = useContext(CollapsibleSections);
  if (collapsible && title)
    return (
      <section aria-label={typeof title === "string" ? title : undefined}>
        <CollapsibleBlock title={title} description={description} id={id} className={className}>
          {actions ? (
            <div className="flex flex-wrap items-center gap-2 px-5 pt-4">{actions}</div>
          ) : null}
          <div className={bodyClassName ?? "space-y-4 px-5 py-4"}>{children}</div>
          {footer ? (
            <div className="flex flex-wrap items-center justify-end gap-2 border-t border-border bg-bg-subtle px-5 py-3">
              {footer}
            </div>
          ) : null}
        </CollapsibleBlock>
      </section>
    );
  return (
    <section
      id={id}
      aria-label={typeof title === "string" ? title : undefined}
      className={cn("overflow-hidden rounded-xl border border-border bg-surface", className)}
    >
      {title || description || actions ? (
        <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border px-5 py-4">
          <div className="min-w-0">
            {title ? <h2 className="text-sm font-semibold text-fg">{title}</h2> : null}
            {description ? (
              <p className="mt-0.5 max-w-2xl text-sm text-fg-muted">{description}</p>
            ) : null}
          </div>
          {actions ? (
            <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>
          ) : null}
        </div>
      ) : null}
      <div className={bodyClassName ?? "space-y-4 px-5 py-4"}>{children}</div>
      {footer ? (
        <div className="flex flex-wrap items-center justify-end gap-2 border-t border-border bg-bg-subtle px-5 py-3">
          {footer}
        </div>
      ) : null}
    </section>
  );
}

// All page tabs share spacing and retain their scroll container during selection.
/**
 * Run a tab switch without moving the viewport: every scroll ancestor of
 * `root` keeps its offset, and Chrome's scroll anchoring is turned off on the
 * app scroller so a shorter panel can't pull the page to the top.
 */
function keepScroll(root: RefObject<HTMLElement | null>, change: () => void) {
  const positions: [HTMLElement, number][] = [];
  let node: HTMLElement | null = root.current;
  while (node) {
    if (node.classList.contains("app-scroll")) node.style.overflowAnchor = "none";
    positions.push([node, node.scrollTop]);
    node = node.parentElement;
  }
  change();
  requestAnimationFrame(() => {
    for (const [element, top] of positions) element.scrollTop = top;
  });
}

export function SubtabNav(props: ComponentProps<typeof AppKitSubtabNav>) {
  const root = useRef<HTMLDivElement>(null);
  return (
    <div ref={root} className="mb-6">
      <AppKitSubtabNav
        {...props}
        onSelect={(key) => keepScroll(root, () => props.onSelect?.(key))}
      />
    </div>
  );
}

/** URL-backed sections within a page, on the shared sub-tab template. */
export function PageSubtabs({
  active,
  onSelect,
  tabs,
  ariaLabel,
}: {
  active: string;
  onSelect: (key: string) => void;
  tabs: PageTab[];
  ariaLabel: string;
}) {
  return <SubtabNav active={active} onSelect={onSelect} tabs={tabs} ariaLabel={ariaLabel} />;
}
