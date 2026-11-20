# Seedstrem: remote torrent playback

[Remix guide](GUIDE.en.md) · [Français](SEEDSTREM.fr.md)

[Seedstrem](https://github.com/kipsilabs/seedstrem) is a self-hosted Stremio addon: it searches Prowlarr and streams files while qBittorrent or Deluge downloads them. Movies, shows and anime depend on the configured indexers and enabled content types.

## Installation

1. Follow the [official repository](https://github.com/kipsilabs/seedstrem) instructions. Configure Prowlarr and a torrent client; Seedstrem needs access to that client’s files through volumes and path mappings.
2. Set its external address, reachable from Remix, preferably over HTTPS. Protect manifest and playback routes: Seedstrem’s admin password alone does not protect its Stremio routes. A private reverse-proxy prefix can be used, with the same prefix in the external address.
3. Use a dedicated torrent category. Explicitly choose seeding, cleanup and disk limits; leave adoption of existing torrents and RSS downloads disabled for an on-demand-only setup.

BitTorrent traffic leaves through the **torrent client**. A VPN in front of Remix alone does not protect this traffic: configure the torrent client’s network accordingly.

## Connecting Remix

Under **Sources → Stremio → Seedstrem**, click **Add my Seedstrem instance** and paste its manifest, for example `https://seedstrem.example/PRIVATE_ACCESS/stremio/manifest.json`. Save the addon and adjust its priority like any other Stremio source. The generic addon form also works.

After saving, **Stremio addons** expands automatically and displays the source. The **Seedstrem** block remains an addition guide; edit or remove the instance in the addon list.

Searches and playback use the configured instance. Remix preserves Seedstrem HTTP links instead of converting their embedded magnet into a request to its debrid accounts. Remix needs no Seedstrem volume; qBittorrent/Deluge and the files can live on another machine.

## When does downloading start?

- Searching for sources does not start a download.
- Opening a Seedstrem playback link can add the torrent and wait for initial data.
- Remix opens Seedstrem links only for playback: searches, tests and availability checks never start downloads, even when opening other addon streams is allowed. An external tool opening a Seedstrem link directly can still start its download.

Startup and seeking depend on peers and available pieces. Seedstrem is separate from Remix’s built-in **Torrent client** source: configuring the same client in both is unnecessary when using its manifest.

## Playback only and last resort

Remix opens Seedstrem links only for an actual playback request or playback failover, never for diagnostics, preloads or availability checks, even when opening other addon streams is globally allowed. A warm-up promoted to actual playback may then open the link.

Enable **Last-resort playback source** in the Stremio source editor to try its results only after normal sources fail, independently of quality and priority. This per-source option defaults to off. An explicitly enforced version or pinned release still takes precedence.

Search and indexing still query Seedstrem without downloading. Results remain candidates, not proof that files are downloaded or playable. Prowlarr can therefore be queried both through Seedstrem and directly if also configured in Remix.


## Quick setup

Under **Remix → Playback → Priorities**, [quick setup](QUICK_SOURCES.en.md) places this source in a strict order. Existing settings are preserved and it is off by default. Searches and availability checks never trigger Seedstrem downloads.
