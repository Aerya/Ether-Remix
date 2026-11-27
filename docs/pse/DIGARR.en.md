# Digarr music discovery

Open **Content → Music → Playlists → Digarr** and select a viewer. **Find in Remix** searches the Deezer catalogue for albums with an exact artist name and, for album recommendations, an exact album title (ignoring case and whitespace). Choose **Create a playlist from this album** to add its tracks to a new personal playlist in Plex/Plexamp, Jellyfin and Subsonic. No audio is downloaded by the import; playback depends on your configured music sources.

Configure [Digarr](https://github.com/iuliandita/digarr) separately. Create a personal **read** token in each Digarr user's **Settings → Account → API tokens**. Set these environment variables on the Remix server, then restart:

- `PSE_DIGARR_URL`: your trusted HTTP(S) Digarr instance, optionally with a path prefix, without credentials, query parameters or fragment.
- `PSE_DIGARR_TOKENS`: JSON mapping Remix viewer IDs to their personal Digarr tokens, e.g. `{"7":"<personal token>","8":"<another personal token>"}`. IDs appear in the viewer selector. There is no shared fallback token. Use distinct Digarr accounts for distinct recommendations.

Protect the environment file and use HTTPS outside a trusted private network. Tokens are never returned by the connector API. This is an administration-console page: its administrator can select viewers, just as with other playlists.

The connector reads pending recommendations through Digarr API v1 (documented 2.3.0 contract), 20 per page, up to offset 10,000. Run discovery and manage approvals in Digarr itself. Remix does not approve/reject recommendations, start scans, trigger Lidarr/slskd downloads or send listening history. Redirects are refused, Digarr calls time out after 20 seconds, and responses are capped at 1 MiB.

Matching examines the first 50 album search results, not an entire discography. Local and iBroadcast libraries are not searched. MusicBrainz IDs are not treated as Deezer IDs. Exact names can still be ambiguous: check the proposed album before importing. Failures and incomplete albums create no playlist and leave existing playlists intact. Each successful import creates a new playlist; after an interrupted request, check your playlists before retrying to avoid duplicates.

HTTP contracts, token isolation, failure handling and imports are tested with simulated services. Compatibility with a live Digarr instance remains to be validated. See also [scheduled playlists and AudioMuse](MUSIC-FLOWS.en.md).
