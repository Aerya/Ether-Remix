import { SettingsSection as AppSettingsSection, cn } from "@braedonsaunders/appkit-ui";
import {
  createContext,
  useContext,
  useEffect,
  useRef,
  type ComponentProps,
  type ReactNode,
} from "react";

/** Only the source tabs opt in; nested settings retain their existing layout. */
export const CollapsibleSections = createContext(false);

export function CollapsibleBlock({
  title,
  description,
  children,
  className,
  id,
  open = true,
}: {
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  className?: string;
  id?: string;
  open?: boolean;
}) {
  const root = useRef<HTMLDetailsElement>(null);
  useEffect(() => {
    const reveal = () => {
      const target = document.getElementById(window.location.hash.slice(1));
      if (target && root.current?.contains(target)) root.current.open = true;
    };
    reveal();
    window.addEventListener("hashchange", reveal);
    return () => window.removeEventListener("hashchange", reveal);
  }, []);
  return (
    <details
      ref={root}
      id={id}
      open={open}
      className={cn("rounded-xl border border-border bg-surface", className)}
    >
      <summary className="cursor-pointer select-none px-5 py-4 text-sm font-semibold text-fg focus-visible:outline-2 focus-visible:outline-offset-2">
        <h2 className="inline text-sm font-semibold">{title}</h2>
      </summary>
      <div className="border-t border-border">
        {description ? <p className="px-5 pt-4 text-sm text-fg-muted">{description}</p> : null}
        <CollapsibleSections.Provider value={false}>{children}</CollapsibleSections.Provider>
      </div>
    </details>
  );
}

export function SettingsSection(props: ComponentProps<typeof AppSettingsSection>) {
  const collapsible = useContext(CollapsibleSections);
  if (!collapsible || !props.title) return <AppSettingsSection {...props} />;
  return (
    <CollapsibleBlock
      title={props.title}
      description={props.description}
      className={props.className}
    >
      <div className="divide-y divide-border">{props.children}</div>
      {props.footer ? <div className="border-t border-border px-5 py-3">{props.footer}</div> : null}
    </CollapsibleBlock>
  );
}
