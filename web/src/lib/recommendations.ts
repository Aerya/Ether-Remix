/**
 * Links into the Recommendations designer (/recommendations). The designer
 * reads ?page=<page key> (and ?row=<row id> to open that row's drawer), and
 * ?add=collection:<id> to start a new row from a collection.
 */

/** The page key of a library's rows tab: "library:<id>/<tab>". */
export function libraryPageKey(libraryId: number, tabKey: string): string {
  return `library:${libraryId}/${tabKey}`;
}

/** The designer at one page, with one row's drawer open when rowId is set. */
export function designerHref(page: string, rowId?: string): string {
  const q = new URLSearchParams({ page });
  if (rowId) q.set("row", rowId);
  return `/recommendations?${q}`;
}

/** The designer at a library's rows tab. */
export function libraryRowsHref(libraryId: number, tabKey: string): string {
  return designerHref(libraryPageKey(libraryId, tabKey));
}

/** The designer's New row drawer with this collection as the source; it asks which page. */
export function addToPageHref(collectionId: string): string {
  return `/recommendations?${new URLSearchParams({ add: `collection:${collectionId}` })}`;
}

/** The designer's New row drawer with a row source preselected (a plugin list); it asks which page. */
export function addRowHref(sourceKind: string): string {
  return `/recommendations?${new URLSearchParams({ add: sourceKind })}`;
}
