import { titleScheduleApi } from "../pages/libraries/browser/titleSchedule";
import { useState } from "react";
import { Button } from "@braedonsaunders/appkit-ui";
import { getLocale } from "../i18n";
import { apiFetch } from "../lib/api";
import { useAsync } from "../lib/hooks";
const L = (fr: string, en: string) => getLocale().startsWith("fr") ? fr : en;
export interface ReleaseRow {
 name: string; source: string; protocol: string; size: number; height: number;
 videoCodec: string; audioCodec: string; languages: string[] | null; cached: string[] | null;
 published: number; firstSeen: number; lastSeen: number; state: string;
}
type SortKey = keyof ReleaseRow;
export function sortReleases(rows: ReleaseRow[], key: SortKey, descending: boolean) {
 return [...rows].sort((a,b) => {
  const av=a[key],bv=b[key];
  if(typeof av==="number" && typeof bv==="number") {
   if(!av !== !bv) return av ? -1 : 1;
   return (av-bv)*(descending?-1:1)||a.name.localeCompare(b.name);
  }
  return String(av??"").localeCompare(String(bv??""),getLocale(),{numeric:true})*(descending?-1:1)||a.name.localeCompare(b.name);
 });
}
const date=(n:number)=>n?new Date(n*1000).toLocaleString(getLocale()):"—";
const stateLabel=(s:string)=>({ready:L("Référence disponible","Reference available"),metadata:L("Métadonnées seules","Metadata only"),expired:L("À actualiser","Needs refresh"),failed:L("Échec de lecture","Playback failed"),source_changed:L("Source modifiée","Source changed")}[s]??s);
export function ReleaseLibrary({titleKey,seasons=[]}:{titleKey:string;seasons?:{season:number;title:string}[]}) {
 const [season,setSeason]=useState<number | null>(null);
 const episodeState=useAsync((signal)=>season===null?Promise.resolve([]):titleScheduleApi.season(titleKey,season,signal),[titleKey,season]);
 const episodes=episodeState.data??[];
 const [episode,setEpisode]=useState(""); const key=episode||titleKey;
 const state=useAsync((signal)=>apiFetch<{enabled:boolean;releases:ReleaseRow[]}>(`/pse/release-library?key=${encodeURIComponent(key)}`,{signal}),[key]);
 const [sort,setSort]=useState<SortKey>("height"); const [descending,setDescending]=useState(true);const [query,setQuery]=useState("");
 const rows=sortReleases((state.data?.releases??[]).filter(r=>`${r.name} ${r.source} ${r.videoCodec} ${r.audioCodec} ${(r.languages??[]).join(" ")}`.toLocaleLowerCase().includes(query.toLocaleLowerCase())),sort,descending);
 const columns:[SortKey,string][]=[ ["name","Release"],["height",L("Résolution","Resolution")],["size",L("Taille","Size")],["videoCodec",L("Codec vidéo","Video codec")],["audioCodec",L("Codec audio","Audio codec")],["source","Source"],["protocol",L("Protocole","Protocol")],["languages",L("Langues","Languages")],["cached",L("Cache annoncé","Reported cache")],["state",L("Référence","Reference")],["published",L("Publication","Published")],["firstSeen",L("Première indexation","First indexed")],["lastSeen",L("Dernière observation","Last observed")]];
 function cell(r:ReleaseRow,k:SortKey) {
  if(k==="size")return r.size?`${(r.size/1024**3).toLocaleString(getLocale(),{maximumFractionDigits:2})} GiB`:"—";
  if(k==="height")return r.height?`${r.height}p`:"—";
  if(k==="state")return stateLabel(r.state);
  if(k==="published"||k==="firstSeen"||k==="lastSeen")return date(r[k]);
  return Array.isArray(r[k])?(r[k] as string[]).join(", ")||"—":String(r[k]||"—");
 }
 return <section className="space-y-4 p-5">
  <h3 className="font-semibold">{L("Releases indexées — Bêta","Indexed releases — Beta")}</h3>
  <p className="text-sm text-fg-muted">{L("Activez la bêta dans Remix → Indexation pour réutiliser les références à la lecture. Une référence disponible ou un cache annoncé ne garantit pas la lecture. Les liens privés restent sur le serveur.","Enable beta in Remix → Indexing to reuse references during playback. An available reference or reported cache does not guarantee playback. Private links stay on the server.")}</p>
  {state.data&&<p>{state.data.enabled?L("Bêta activée","Beta enabled"):L("Bêta désactivée","Beta disabled")} · {rows.length} releases</p>}
  {seasons.length>0&&<label>{L("Saison","Season")} <select aria-label={L("Saison","Season")} value={season??""} onChange={e=>{setSeason(e.target.value===""?null:Number(e.target.value));setEpisode("")}}><option value="">{L("Vue série (S01E01)","Series view (S01E01)")}</option>{seasons.map(s=><option key={s.season} value={s.season}>{s.title||`S${s.season}`}</option>)}</select></label>}
  {episodes.length>0&&<label className="block">{L("Épisode","Episode")} <select aria-label={L("Épisode","Episode")} value={episode} onChange={e=>setEpisode(e.target.value)}><option value="">{L("Vue série (indexation initiale S01E01)","Series view (initial S01E01 indexing)")}</option>{episodes.map(e=><option key={e.key} value={e.key}>S{String(e.season).padStart(2,"0")}E{String(e.episode).padStart(2,"0")}</option>)}</select></label>}
  <div className="flex flex-wrap gap-3"><input className="min-w-0 flex-1 rounded border p-2" aria-label={L("Filtrer les releases","Filter releases")} placeholder={L("Nom, source, codec, langue…","Name, source, codec, language…")} value={query} onChange={e=>setQuery(e.target.value)}/><Button onClick={()=>void state.reload()}>{L("Actualiser l’affichage","Refresh display")}</Button></div>
  {Boolean(state.error)&&<p role="alert">{L("Impossible de charger les releases.","Could not load releases.")}</p>}
  {state.loading?<p role="status">{L("Chargement…","Loading…")}</p>:!rows.length?<p>{L("Aucune release indexée pour ce contenu. L’indexation ou une recherche de lecture alimentera cette liste.","No indexed release for this item. Indexing or a playback search will populate this list.")}</p>:<div className="overflow-x-auto"><table className="w-full text-sm"><caption className="sr-only">{L("Releases indexées, colonnes triables","Indexed releases, sortable columns")}</caption><thead><tr>{columns.map(([k,label])=><th key={k} className="whitespace-nowrap p-2 text-left" aria-sort={sort===k?descending?"descending":"ascending":"none"}><button onClick={()=>{setDescending(sort===k?!descending:false);setSort(k)}}>{label}{sort===k?descending?" ↓":" ↑":" ↕"}</button></th>)}</tr></thead><tbody>{rows.map((r,i)=><tr key={`${r.name}-${r.source}-${i}`} className="border-t">{columns.map(([k])=><td key={k} className={`p-2 ${k==="name"?"min-w-64 break-all":"whitespace-nowrap"}`}>{cell(r,k)}</td>)}</tr>)}</tbody></table></div>}
  <p className="text-sm text-fg-muted">{L("Publication : date fournie par la source, sinon inconnue. Première indexation et dernière observation : dates propres à Remix. Le tri du tableau ne change pas vos critères de lecture.","Publication: source-provided date, otherwise unknown. First indexed and last observed: Remix dates. Table sorting does not change playback preferences.")}</p>
 </section>;
}
