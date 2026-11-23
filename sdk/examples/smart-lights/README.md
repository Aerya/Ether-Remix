# Smart lights (Ether Flows example plugin)

A minimal hosted TypeScript plugin demonstrating Ether Flows integration:

- **Published event (`contributes.events.publish`):** emits `plugin:org.ether.smart-lights/scene.changed` with typed, filterable fields (`room`, `scene`, `brightness`, `previousScene`) via `ether.events.publish(...)`.
- **Canonical event subscription (`contributes.events.subscribe`):** listens to `playback.session.started` and `playback.session.stopped`.
- **Flow action (`contributes.flowActions`):** contributes `plugin:org.ether.smart-lights/set_scene` at `POST /ether/v1/flow-actions/set_scene`, honoring `X-Ether-Idempotency-Key` deduplication and `dryRun`.

## Example Flow

1. **Trigger:** `playback.session.started` (or `plugin:org.ether.smart-lights/scene.changed`)
2. **Action:** `plugin:org.ether.smart-lights/set_scene` with `room: "Living room"`, `scene: "cinema"`, `brightness: 20`
3. **Downstream:** Any subsequent flow subscribed to `plugin:org.ether.smart-lights/scene.changed` receives the validated payload.
