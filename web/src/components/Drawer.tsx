import { Drawer as AppKitDrawer, Spinner, type DrawerProps, cn } from "@braedonsaunders/appkit-ui";
import {
  Suspense,
  createContext,
  useContext,
  useEffect,
  useId,
  useRef,
  type ReactNode,
} from "react";
import { t } from "../i18n";
import { useDrawerTitle } from "../lib/document-title";

const DrawerContext = createContext(false);

/** Reset any horizontal scroll introduced by off-screen focus during drawer transitions. */
export function resetShellHorizontalScroll() {
  if (typeof window === "undefined" || typeof document === "undefined") return;
  if (window.scrollX !== 0) window.scrollTo(0, window.scrollY);
  if (document.documentElement.scrollLeft !== 0) document.documentElement.scrollLeft = 0;
  if (document.body.scrollLeft !== 0) document.body.scrollLeft = 0;
  for (const el of document.querySelectorAll<HTMLElement>(
    ".ether-shell > div, .ether-shell > div > div, .ether-shell main, [data-workspace-scroll]",
  )) {
    if (el.scrollLeft !== 0) el.scrollLeft = 0;
  }
}

/** Associate AppKit's visible record title with the dialog's accessible name. */
function RecordTitle({ children }: { children: ReactNode }) {
  const id = useId();
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const dialog = ref.current?.closest('[role="dialog"]');
    if (dialog) dialog.setAttribute("aria-labelledby", id);
  }, [id]);
  return (
    <span ref={ref} id={id}>
      {children}
    </span>
  );
}

/** Preserve AppKit focus, scroll and Escape handling when editors open a child drawer. */
export function Drawer({
  title,
  documentTitle,
  stacked,
  bodyClassName,
  onExitComplete,
  children,
  ...props
}: DrawerProps & {
  /** When set, appends this title to the browser tab title while the drawer is open. */
  documentTitle?: string | null;
}) {
  const nested = useContext(DrawerContext);
  useDrawerTitle(props.open ? documentTitle : null);
  useEffect(() => {
    if (!props.open) {
      resetShellHorizontalScroll();
      return;
    }
    const timer = window.setTimeout(resetShellHorizontalScroll, 0);
    return () => {
      window.clearTimeout(timer);
      resetShellHorizontalScroll();
      if (typeof requestAnimationFrame === "function") {
        requestAnimationFrame(resetShellHorizontalScroll);
      }
    };
  }, [props.open]);
  return (
    <DrawerContext.Provider value={true}>
      <AppKitDrawer
        {...props}
        stacked={stacked ?? nested}
        onExitComplete={() => {
          resetShellHorizontalScroll();
          onExitComplete?.();
        }}
        bodyClassName={cn(
          "[&_fieldset]:min-w-0",
          bodyClassName ?? "overflow-y-auto px-4 py-5 sm:px-6",
        )}
        title={<RecordTitle>{title ?? t("Editor")}</RecordTitle>}
      >
        <Suspense
          fallback={
            <div className="grid min-h-32 flex-1 place-items-center p-6">
              <Spinner size={20} label={t("Loading editor")} />
            </div>
          }
        >
          {children}
        </Suspense>
      </AppKitDrawer>
    </DrawerContext.Provider>
  );
}
