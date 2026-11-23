import { Button, SubtabNav } from "@braedonsaunders/appkit-ui";
import type { ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { useRouteTab } from "../../components/layout";
import { Workspace } from "../../components/workspace";
import { localized, t } from "../../i18n";
import "./metadata.css";

const sections = localized(() => [
  { key: "artwork" as const, label: t("Artwork") },
  { key: "ratings" as const, label: t("Ratings") },
  { key: "episodes" as const, label: t("Episode order") },
]);

export function MetadataWorkspace({
  aside,
  main,
  actions,
  fitPreview = false,
}: {
  aside: ReactNode;
  main: ReactNode;
  actions?: ReactNode;
  fitPreview?: boolean;
}) {
  const active = useRouteTab(sections);
  const navigate = useNavigate();
  return (
    <Workspace
      title={t("Metadata")}
      actions={
        <>
          <SubtabNav
            ariaLabel={t("Metadata sections")}
            active={active}
            onSelect={(next) => navigate(`/metadata/${next}`)}
            tabs={sections}
            className="[&_button]:py-1.5"
          />
          {actions}
          <Button
            size="sm"
            variant="ghost"
            className="lg:hidden"
            onClick={() =>
              document.querySelector(".metadata-canvas")?.scrollIntoView({ block: "start" })
            }
          >
            {t("Preview ↓")}
          </Button>
        </>
      }
      asideLabel={t("Metadata controls")}
      mainLabel={t("Metadata preview")}
      mainClassName={`metadata-canvas ${fitPreview ? "metadata-canvas-fit" : ""}`}
      aside={<div className="metadata-controls">{aside}</div>}
      main={main}
    />
  );
}
