import { ArrowRight, Rocket } from "lucide-react";
import { Link } from "react-router-dom";
import { getLocale } from "../i18n";

const L = (fr: string, en: string) => (getLocale().startsWith("fr") ? fr : en);

export function QuickStartHint() {
  return (
    <aside className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-border bg-surface p-4">
      <p className="text-sm text-fg-muted">
        {L(
          "La recherche automatique est désactivée. Les recherches à la lecture continuent d’alimenter l’index.",
          "Automatic search is disabled. Playback searches still populate the index.",
        )}
      </p>
      <Link
        className="inline-flex items-center gap-2 font-medium text-accent underline"
        to="/remix/apercu?view=demarrage"
      >
        {L("Démarrage rapide", "Quick start")}
        <ArrowRight size={16} aria-hidden="true" />
      </Link>
    </aside>
  );
}

export function QuickStart({ indexing }: { indexing: boolean }) {
  const steps = [
    {
      title: L("Ajouter et tester vos sources", "Add and test your sources"),
      text: L(
        "Dans Sources, ajoutez vos addons Stremio, indexeurs ou autres fournisseurs, testez-les puis enregistrez-les et vérifiez qu’ils sont activés. Remix ne fournit pas les comptes ni les manifestes.",
        "Under Sources, add your Stremio addons, indexers or other providers, test and save them, then check they are enabled. Remix does not supply accounts or manifests.",
      ),
      help: L(
        "Un addon qui fournit déjà des liens de lecture avec son propre compte débrideur n’exige pas de recopier sa clé pour les recherches. Configurez un compte dans Debrid si Remix doit contacter lui-même ce service.",
        "An addon already supplying playback links through its own debrid account does not require copying its key for searches. Configure a Debrid account when Remix itself needs to contact that service.",
      ),
      to: "/remix/sources",
      action: L("Configurer les sources", "Configure sources"),
    },
    {
      title: L("Préparer les releases automatiquement", "Prepare releases automatically"),
      text: L(
        "Dans Indexation, activez Rechercher automatiquement des releases puis enregistrez. Les résultats sont conservés et réutilisés sans second réglage.",
        "Under Indexing, enable Automatically search for releases and save. Results are stored and reused without a second setting.",
      ),
      help: L(
        "Le mode Automatique utilise les addons Stremio activés. Passez en Personnalisé pour inclure Prowlarr/Jackett ou choisir aucune source. Conservez une cadence et des quotas adaptés aux fournisseurs ; l’indexation se met en pause pendant les lectures.",
        "Automatic mode uses enabled Stremio addons. Switch to Custom to include Prowlarr/Jackett or choose no sources. Keep the pace and quotas appropriate for your providers; indexing pauses during playback.",
      ),
      to: "/remix/indexation#pse-pse_indexer",
      action: L("Régler l’indexation", "Configure indexing"),
      status: indexing
        ? L("Réglage enregistré : activée", "Saved setting: enabled")
        : L("Réglage enregistré : désactivée", "Saved setting: disabled"),
    },
    {
      title: L("Choisir vos langues et votre qualité", "Choose your languages and quality"),
      text: L(
        "Vérifiez les langues, résolutions et formats acceptés. Si vous activez un profil de qualité, ses critères remplacent les critères de releases du catalogue « lisible uniquement ».",
        "Check the accepted languages, resolutions and formats. If you enable a quality profile, its criteria replace the release criteria of the playable-only catalogue.",
      ),
      help: L(
        "Le catalogue « lisible uniquement » peut rester vide au départ : il faut attendre des résultats qui respectent vos filtres et le seuil de releases configuré. Les titres TMDB du catalogue local ne sont pas une preuve de disponibilité.",
        "The playable-only catalogue may initially be empty: wait for results matching your filters and configured release threshold. Local TMDB catalogue entries do not prove availability.",
      ),
      to: "/remix/lecture?view=qualite",
      action: L("Vérifier les critères", "Check criteria"),
    },
    {
      title: L("Vérifier que les recherches démarrent", "Check that searches start"),
      text: L(
        "Dans Vue d’ensemble → Activité, regardez Sources et activité, Dernières recherches et Titres cherchés. Le catalogue se remplit progressivement, pas immédiatement après l’installation.",
        "In Overview → Activity, check Sources and activity, Recent searches and Searched titles. The catalogue fills progressively, not immediately after installation.",
      ),
      help: L(
        "Si rien ne bouge : vérifiez l’activation, les sources sélectionnées, les quotas, une lecture en cours et les erreurs. Les statistiques d’utilisation et de lecture sont distinctes de celles de l’indexation.",
        "If nothing changes, check activation, selected sources, quotas, ongoing playback and errors. Usage and playback statistics are separate from background indexing.",
      ),
      to: "/remix/apercu?view=indexation",
      action: L("Voir l’activité", "View activity"),
    },
    {
      title: L("Organiser vos contenus, si besoin", "Organize content, if needed"),
      text: L(
        "Les bibliothèques et collections personnalisées sont facultatives. Contenus permet de préparer Films, Séries, Animes ou Musique ; la configuration JukeBox peut vous aider à démarrer.",
        "Custom libraries and collections are optional. Content lets you prepare Movies, Shows, Anime or Music; JukeBox setup can help you get started.",
      ),
      help: L(
        "Pour la musique ou les fichiers locaux uniquement, l’indexation vidéo n’est pas un prérequis : configurez les sources correspondantes. Les dossiers locaux et comptes iBroadcast sont privés par défaut ; choisissez leur partage si nécessaire.",
        "For music or local files only, video indexing is not required: configure the relevant sources. Local folders and iBroadcast accounts are private by default; choose sharing if needed.",
      ),
      to: "/remix/contenus",
      action: L("Choisir les contenus", "Choose content"),
    },
    {
      title: L(
        "Connecter une application et essayer une lecture",
        "Connect an app and try playback",
      ),
      text: L(
        "Activez le protocole de votre client dans Applications, puis utilisez les accès du spectateur concerné. Pour un client musical Subsonic, retrouvez l’adresse et les accès dans Contenus → Musique.",
        "Enable your client’s protocol in Apps, then use the appropriate viewer’s credentials. For a Subsonic music client, find the address and access details in Content → Music.",
      ),
      help: L(
        "Essayez un titre et contrôlez son lancement. Une source enregistrée ou un test de connexion réussi ne garantit pas la lecture de tous ses contenus. Scrob, notifications et sous-titres externes peuvent être configurés ensuite.",
        "Try a title and check playback starts. A saved source or successful connection test does not guarantee every item will play. Scrob, notifications and external subtitles can be configured later.",
      ),
      to: "/apps",
      action: L("Connecter un client", "Connect a client"),
    },
  ];
  return (
    <section className="space-y-6" aria-labelledby="quick-start-title">
      <header className="rounded-xl border border-border bg-surface p-5">
        <h2 id="quick-start-title" className="flex items-center gap-2 text-lg font-semibold">
          <Rocket className="text-accent" size={22} aria-hidden="true" />
          {L("Démarrage rapide", "Quick start")}
        </h2>
        <p className="mt-2 text-sm text-fg-muted">
          {L(
            "Les étapes essentielles pour utiliser Remix. Suivez les quatre premières pour préparer votre catalogue vidéo ; adaptez ensuite les contenus et le client à votre usage. Ce guide reste accessible à tout moment et ne modifie aucun réglage à votre place.",
            "The essentials for using Remix. Follow the first four steps to prepare your video catalogue, then choose content and a client for your needs. This guide remains available at any time and does not change settings for you.",
          )}
        </p>
      </header>
      <ol className="grid gap-4 lg:grid-cols-2">
        {steps.map((step, i) => (
          <li
            key={step.to}
            className="flex flex-col gap-3 rounded-xl border border-border bg-surface p-5"
          >
            <h3 className="flex items-start gap-3 font-semibold">
              <span className="flex size-7 shrink-0 items-center justify-center rounded-full border border-border text-sm text-accent">
                {i + 1}
              </span>
              {step.title}
            </h3>
            {step.status && <p className="text-sm font-medium">{step.status}</p>}
            <p className="text-sm">{step.text}</p>
            <p className="text-sm text-fg-muted">{step.help}</p>
            <Link
              className="mt-auto inline-flex items-center gap-2 pt-2 text-sm font-medium text-accent underline"
              to={step.to}
            >
              {step.action}
              <ArrowRight size={16} aria-hidden="true" />
            </Link>
          </li>
        ))}
      </ol>
    </section>
  );
}
