import http.server
import json
import threading
import unittest

from ether_plugin import (
    HEADER_IDEMPOTENCY_KEY,
    create_mock_host_client,
    create_remote_host_client,
    create_test_harness,
    plugin,
)


SMART_LIGHTS_MANIFEST = {
    "id": "community.example.lights",
    "name": "Smart Lights",
    "version": "1.0.0",
    "ether": {
        "protocol": 1,
        "kind": "extension",
        "summary": "Controls room lighting scenes",
        "runtime": {"kind": "remote"},
        "capabilities": ["events", "flow_actions"],
        "contributes": {
            "events": {
                "subscribe": ["playback.session.started"],
                "publish": [
                    {
                        "key": "scene.changed",
                        "label": "Scene changed",
                        "fields": [{"name": "scene", "type": "string", "label": "Scene"}],
                    }
                ],
            },
            "flowActions": [
                {
                    "key": "set_scene",
                    "label": "Set scene",
                    "route": "/ether/v1/flow-actions/set_scene",
                    "idempotent": True,
                    "sideEffect": True,
                },
                {
                    "key": "custom_dim",
                    "label": "Custom dim",
                    "route": "/ether/custom/dim",
                },
            ],
        },
    },
}


class FlowsSDKTest(unittest.TestCase):
    def test_flow_action_route_idempotency_and_publish(self) -> None:
        mock_host = create_mock_host_client()

        def handle_flow_action(req, ctx):
            ctx.ether.events.publish(
                "scene.changed",
                {"scene": req.get("params", {}).get("scene")},
                {"key": "room:den"},
            )
            return {
                "output": {
                    "key": req.get("key"),
                    "scene": req.get("params", {}).get("scene"),
                    "idempotencyKey": req.get("idempotencyKey"),
                    "dryRun": req.get("dryRun"),
                }
            }

        instance = plugin(
            SMART_LIGHTS_MANIFEST,
            {"flow_action": handle_flow_action},
            host=mock_host,
        )
        harness = create_test_harness(instance)

        resp = harness.flow_action(
            "set_scene",
            {
                "flowId": "flow-1",
                "runId": "run-1",
                "nodeId": "node-1",
                "idempotencyKey": "run-1:node-1:0",
                "params": {"scene": "cinema"},
            },
        )
        self.assertEqual(resp["status"], 200)
        out = resp["body"]["output"]
        self.assertEqual(out["key"], "set_scene")
        self.assertEqual(out["scene"], "cinema")
        self.assertEqual(out["idempotencyKey"], "run-1:node-1:0")
        self.assertEqual(len(mock_host.published_events), 1)
        self.assertEqual(mock_host.published_events[0]["key"], "scene.changed")

        # Custom declared route + idempotency key from header.
        resp2 = instance.dispatch(
            {
                "method": "POST",
                "path": "/ether/custom/dim",
                "headers": {HEADER_IDEMPOTENCY_KEY: "idem-hdr-7"},
                "body": json.dumps({"dryRun": True, "params": {"scene": "warm"}}),
            }
        )
        self.assertEqual(resp2["status"], 200)
        out2 = resp2["body"]["output"]
        self.assertEqual(out2["key"], "custom_dim")
        self.assertEqual(out2["idempotencyKey"], "idem-hdr-7")
        self.assertTrue(out2["dryRun"])

    def test_remote_host_client_publish_event(self) -> None:
        captured: dict = {}

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_POST(self) -> None:
                length = int(self.headers.get("Content-Length", "0"))
                captured["path"] = self.path
                captured["cred"] = self.headers.get("X-Ether-Plugin-Credential")
                captured["body"] = json.loads(self.rfile.read(length).decode("utf-8"))
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.end_headers()
                self.wfile.write(b'{"ok":true}')

            def log_message(self, format: str, *args) -> None:
                pass

        server = http.server.HTTPServer(("127.0.0.1", 0), Handler)
        port = server.server_address[1]
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            client = create_remote_host_client(f"http://127.0.0.1:{port}", "cred-py-1")
            client.events.publish("scene.changed", {"scene": "bright"}, {"key": "room:den"})
            self.assertEqual(captured["path"], "/ether/plugin-host/events/publish")
            self.assertEqual(captured["cred"], "cred-py-1")
            self.assertEqual(captured["body"]["key"], "scene.changed")
            self.assertEqual(captured["body"]["payload"], {"scene": "bright"})
            self.assertEqual(captured["body"]["correlation"], {"key": "room:den"})
        finally:
            server.shutdown()
            server.server_close()

    def test_mock_host_client_publish_event(self) -> None:
        mock_host = create_mock_host_client()
        mock_host.events.publish("scene.changed", {"scene": "off"})
        self.assertEqual(len(mock_host.published_events), 1)
        self.assertEqual(mock_host.published_events[0]["key"], "scene.changed")


if __name__ == "__main__":
    unittest.main()
