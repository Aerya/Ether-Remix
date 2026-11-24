# Scheduled playlists and AudioMuse

Under **Content → Music → Playlists → Scheduled**, choose a viewer, name, type, interval from 1 to 168 hours and size from 1 to 200 tracks. Save, then use **Refresh** to generate the first playlist immediately. Each viewer can have up to 20 schedules.

- **Personal mix** interleaves favourites, recent listens and favourites played at most once. Each pool considers up to 1,000 accessible entries and rotates every period; duplicates are removed. Empty history cannot produce a mix. Existing Discoveries and New releases remain separate automatic playlists.
- **Linked playlist** follows an HTTPS Deezer, Spotify or ListenBrainz playlist URL. Spotify uses the viewer's existing linked account. Successful refreshes replace generated contents in source order, up to the chosen size. Missing matches are counted; searches require matching complete artist and title. Missing audio is never downloaded. Sources exceeding the service's import limit are rejected instead of replacing contents with a truncated response.
- **AudioMuse** recommends neighbours of a local seed track using an already configured service and analysed library, as described below.

Generated playlists retain their ID in Plex/Plexamp, Jellyfin and Subsonic. Automatic refreshes require music to be enabled; schedules are checked every ten minutes, starting one minute after startup. Failed attempts retry at the next scheduled interval, or immediately with **Refresh**. Last success, matched and missing counts and errors persist across restarts.

Failures and entirely empty results preserve the last successful contents. Editing a schedule while it runs prevents the obsolete result from overwriting it. **Pause** retains the playlist; **Delete** removes the schedule and generated playlist without touching audio files or other playlists. Local source permissions are enforced both during generation and playlist access.

## Optional AudioMuse service

Set these variables in the Remix server environment, then restart:

| Variable | Value |
|---|---|
| `PSE_AUDIOMUSE_URL` | Your AudioMuse instance URL, without credentials, query or fragment; HTTP or HTTPS. |
| `PSE_AUDIOMUSE_TOKEN` | AudioMuse API token when authentication is enabled. Kept server-side and never returned to the console. |
| `PSE_AUDIOMUSE_SERVER` | The Remix server name configured in AudioMuse. Required to distinguish libraries. |

Use a trusted instance, HTTPS across non-private networks, and Remix music credentials restricted to the relevant local folders. Configure AudioMuse to index this server through OpenSubsonic, then run analysis from AudioMuse. External analysis may read audio files; the Remix connector never starts it.

Choose **AudioMuse** and an authorised, already analysed local seed track. The selector lists up to 1,000 accessible local tracks, sorted by name. iBroadcast and remote catalogue tracks cannot serve as seeds for this connector.

Remix only calls `GET /api/similar_tracks` with `item_id=tr-…`, `n`, `server` and `eliminate_duplicates=true`. Redirects are refused. It never calls analysis, playback or remote playlist creation endpoints. Results must use OpenSubsonic IDs from **this Remix instance** and matching artist/title metadata. IDs from an unrelated Navidrome or Jellyfin library are not automatically converted. Inaccessible, mismatched and missing results are discarded and counted.

Mock tests cover the HTTP contract, failures, response bounds and viewer isolation. Full analysis and recommendation quality against a real AudioMuse installation remain to be validated. An unanalysed seed or incompatible API preserves the previous playlist.

Contract reference: [AudioMuse similarity API](https://github.com/NeptuneHub/AudioMuse-AI/blob/main/app_ivf.py).
