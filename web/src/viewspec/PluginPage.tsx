/**
 * A full plugin page at /p/:plugin/:page. The chrome is Ether's DomainPage
 * with the title and icon the plugin's loader sent and a small plugin badge;
 * the body is the plugin's ViewSpec document rendered natively. Extra path
 * segments and query values become view parameters for the loader.
 */
import { Badge } from "@braedonsaunders/appkit-ui";
import { Puzzle } from "lucide-react";
import { useState } from "react";
import { useLocation, useParams } from "react-router-dom";
import { t } from "../i18n";
import { DomainPage } from "../components/layout";
import { usePageTitle } from "../lib/document-title";
import { PluginView, pageHeaderText, type PluginViewDoc } from "./PluginView";

export default function PluginPage() {
  const { plugin = "", page = "", "*": rest = "" } = useParams();
  const location = useLocation();
  const pageKey = `${plugin}/${page}`;
  const [loaded, setLoaded] = useState<{ key: string; doc: PluginViewDoc } | null>(null);
  const doc = loaded?.key === pageKey ? loaded.doc : null;

  const params: Record<string, string> = {};
  for (const [key, value] of new URLSearchParams(location.search)) {
    params[key] = value;
  }
  if (rest) params.path = rest;

  // A plugin's own page header becomes the page title, shown once.
  const header = doc ? pageHeaderText(doc) : {};
  const docTitle = header.title || doc?.title;
  usePageTitle(docTitle ? [docTitle] : null);
  const title = docTitle || page;
  return (
    <DomainPage
      title={title}
      description={header.description}
      base={`/p/${encodeURIComponent(plugin)}/${encodeURIComponent(page)}`}
      actions={
        <Badge variant="secondary">
          {doc?.icon ? (
            <img src={doc.icon} alt="" className="size-3.5 rounded-sm" />
          ) : (
            <Puzzle size={12} aria-hidden />
          )}
          {t("From {plugin}", { plugin })}
        </Badge>
      }
    >
      <PluginView
        key={pageKey}
        plugin={plugin}
        page={page}
        params={params}
        onDoc={(nextDoc) => setLoaded({ key: pageKey, doc: nextDoc })}
        liftPageHeader
      />
    </DomainPage>
  );
}
