# News desk

The smallest complete plugin screen: a front page of stories by topic and the topics a viewer follows, with no live feed. Read [`docs/plugin-screens.md`](../../../docs/plugin-screens.md) first.

- `screens/desk.json`: the document. Its `sources` are `front` (a `plugin` source, shared by every viewer, cached 2 minutes and refreshed every minute while open) and `mine` (a `plugin` source with `scope: "viewer"`, loaded lazily).
- `plugin.ts`: answers both sources with `onScreenSource` and runs the `follow-topic` action with `onScreenAction`. Pictures come from the package with `asset()`; a remote picture would use `image(url)` on one of your `permissions.network` hosts.
- `manifest.json`: the `screens` capability, the screen with its page, and the typed `follow-topic` action.

Check it with `ether plugin check sdk/examples/news-desk`, or open it in Studio to watch the live preview while you edit `screens/desk.json`.
