package catalog

// Adaptation Remix de la conservation du catalogue Ether. Les exceptions
// explicites de langue restent des intentions du propriétaire. Les index de
// releases et compteurs sont dérivés : ils ne doivent pas retenir à eux seuls
// tous les titres automatiquement parcourus par l’indexeur.
func init() {
	tierTouchSources = append(tierTouchSources, touchSource{table: "pse_lang_exempt", column: "title_key", tierA: true})
	tierUntouchedTables = append(tierUntouchedTables,
		"pse_index_activity", "pse_index_budget", "pse_index_cursor", "pse_index_source_history", "pse_index_titles",
		"pse_music_accounts", "pse_music_addon_registry", "pse_music_items", "pse_music_matches", "pse_music_playlists", "pse_music_plays", "pse_music_scrobble", "pse_music_spotify",
		"pse_playable", "pse_releases", "pse_scrob_links", "pse_settings", "pse_sfr_checked", "pse_state", "pse_theme_langfree", "pse_theme_libraries", "pse_theme_priority", "pse_tmdb_totals",
	)
}
