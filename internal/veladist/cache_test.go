package veladist

import "testing"

func TestStaticCacheControlClassification(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		immutable bool
		want      string
	}{
		// Stable shell routes and HTML documents — never immutable, never no-store.
		{name: "empty root", path: "", immutable: false, want: CacheControlRevalidate},
		{name: "slash root", path: "/", immutable: false, want: CacheControlRevalidate},
		{name: "vela trailing slash", path: "/vela/", immutable: false, want: CacheControlRevalidate},
		{name: "vela root", path: "/vela", immutable: false, want: CacheControlRevalidate},
		{name: "root index html", path: "index.html", immutable: false, want: CacheControlRevalidate},
		{name: "prefixed index html", path: "/vela/index.html", immutable: false, want: CacheControlRevalidate},
		{name: "offline html", path: "offline.html", immutable: false, want: CacheControlRevalidate},
		{name: "nested installer index html", path: "install/index.html", immutable: false, want: CacheControlRevalidate},
		{name: "deep nested html", path: "install/apple-tv/index.html", immutable: false, want: CacheControlRevalidate},
		{name: "hashed html never immutable", path: "index-a1b2c3d4e5f60718293a4b5c6d7e8f90.html", immutable: false, want: CacheControlRevalidate},
		{name: "hashed htm never immutable", path: "shell.9a4b7c2e.htm", immutable: false, want: CacheControlRevalidate},
		{name: "html inside player digest dir never immutable", path: "player/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/index.html", immutable: false, want: CacheControlRevalidate},
		{name: "html inside versioned skin dir never immutable", path: "skins/cinematic/1.0.0/index.html", immutable: false, want: CacheControlRevalidate},

		// Web manifests — never immutable.
		{name: "manifest webmanifest", path: "manifest.webmanifest", immutable: false, want: CacheControlRevalidate},
		{name: "manifest json", path: "manifest.json", immutable: false, want: CacheControlRevalidate},
		{name: "hashed webmanifest never immutable", path: "app-a1b2c3d4e5f60718293a4b5c6d7e8f90.webmanifest", immutable: false, want: CacheControlRevalidate},
		{name: "hashed manifest json never immutable", path: "manifest-a1b2c3d4e5f60718.json", immutable: false, want: CacheControlRevalidate},
		{name: "release manifest json", path: "vela-0.2.0.manifest.json", immutable: false, want: CacheControlRevalidate},
		{name: "manifest inside versioned skin never immutable", path: "skins/cinematic/1.0.0/manifest.webmanifest", immutable: false, want: CacheControlRevalidate},

		// Service worker and stable shell scripts/metadata.
		{name: "service worker", path: "sw.js", immutable: false, want: CacheControlRevalidate},
		{name: "prefixed service worker", path: "/vela/sw.js", immutable: false, want: CacheControlRevalidate},
		{name: "alt service worker", path: "service-worker.js", immutable: false, want: CacheControlRevalidate},
		{name: "pwa script", path: "pwa.js", immutable: false, want: CacheControlRevalidate},
		{name: "boot handoff script", path: "boot-handoff.js", immutable: false, want: CacheControlRevalidate},
		{name: "routes json", path: "routes.json", immutable: false, want: CacheControlRevalidate},
		{name: "app version json", path: "app.version.json", immutable: false, want: CacheControlRevalidate},
		{name: "engines json", path: "engines.json", immutable: false, want: CacheControlRevalidate},
		{name: "player assets manifest json", path: "player-assets-manifest.json", immutable: false, want: CacheControlRevalidate},
		{name: "skins catalog json", path: "skins/catalog.json", immutable: false, want: CacheControlRevalidate},
		{name: "dotfile inventory", path: ".ether-inventory.json", immutable: false, want: CacheControlRevalidate},

		// Non-hashed icons and mutable static files.
		{name: "icon 192 png", path: "vela-icon-192.png", immutable: false, want: CacheControlRevalidate},
		{name: "icon 512 png", path: "vela-icon-512.png", immutable: false, want: CacheControlRevalidate},
		{name: "icon maskable 192 png", path: "vela-icon-maskable-192.png", immutable: false, want: CacheControlRevalidate},
		{name: "icon maskable 512 png", path: "vela-icon-maskable-512.png", immutable: false, want: CacheControlRevalidate},
		{name: "icon monochrome svg", path: "vela-icon-monochrome.svg", immutable: false, want: CacheControlRevalidate},
		{name: "icon svg", path: "vela-icon.svg", immutable: false, want: CacheControlRevalidate},
		{name: "favicon ico", path: "favicon.ico", immutable: false, want: CacheControlRevalidate},
		{name: "favicon 32 png", path: "favicon-32.png", immutable: false, want: CacheControlRevalidate},
		{name: "apple touch icon", path: "apple-touch-icon.png", immutable: false, want: CacheControlRevalidate},
		{name: "unhashed js", path: "assets/app.js", immutable: false, want: CacheControlRevalidate},
		{name: "unhashed css", path: "assets/styles.css", immutable: false, want: CacheControlRevalidate},
		{name: "unhashed sourcemap", path: "assets/app.js.map", immutable: false, want: CacheControlRevalidate},
		{name: "date digits only not hash", path: "assets/poster-20261120.png", immutable: false, want: CacheControlRevalidate},
		{name: "hex letters word only not hash", path: "assets/deadbeef.js", immutable: false, want: CacheControlRevalidate},
		{name: "traversal rejected", path: "../assets/entry-a1b2c3d4e5f60718293a4b5c6d7e8f90.js", immutable: false, want: CacheControlRevalidate},

		// Content-hashed and version-addressed assets — immutable for 1 year.
		{name: "expo hashed js", path: "_expo/static/js/web/entry-a1b2c3d4e5f60718293a4b5c6d7e8f90.js", immutable: true, want: CacheControlImmutable},
		{name: "prefixed expo hashed js", path: "/vela/_expo/static/js/web/entry-a1b2c3d4e5f60718293a4b5c6d7e8f90.js", immutable: true, want: CacheControlImmutable},
		{name: "expo hashed css", path: "_expo/static/css/web-9c8a7b6e5d4f3a2b1c0d9e8f7a6b5c4d.css", immutable: true, want: CacheControlImmutable},
		{name: "expo hashed font", path: "assets/fonts/Inter.a1b2c3d4e5f67890a1b2c3d4e5f67890.woff2", immutable: true, want: CacheControlImmutable},
		{name: "expo hashed image", path: "assets/node_modules/expo-router/assets/arrow.8c2e4f1a9b3d7e6f5a4b3c2d1e0f9a8b.png", immutable: true, want: CacheControlImmutable},
		{name: "hashed short 8-hex js", path: "assets/chunk-9a4b7c2e.js", immutable: true, want: CacheControlImmutable},
		{name: "hashed short 12-hex css", path: "assets/theme.1a2b3c4d5e6f.css", immutable: true, want: CacheControlImmutable},
		{name: "hashed underscore mjs", path: "assets/vendor_a1b2c3d4e5f6.mjs", immutable: true, want: CacheControlImmutable},
		{name: "hashed sourcemap", path: "assets/chunk-9a4b7c2e.js.map", immutable: true, want: CacheControlImmutable},
		{name: "vela loader 20-hex js", path: "loader-0123456789abcdef0123.js", immutable: true, want: CacheControlImmutable},
		{name: "skins asset 64-hex woff2", path: "skins/assets/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef.woff2", immutable: true, want: CacheControlImmutable},
		{name: "player digest wasm", path: "player/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/engine.wasm", immutable: true, want: CacheControlImmutable},
		{name: "player digest worker js", path: "player/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/worker.js", immutable: true, want: CacheControlImmutable},
		{name: "versioned skin json", path: "skins/cinematic/1.0.0/skin.json", immutable: true, want: CacheControlImmutable},
		{name: "versioned skin preview svg", path: "skins/cinematic/1.2.3/preview.svg", immutable: true, want: CacheControlImmutable},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsImmutableStaticAsset(tc.path); got != tc.immutable {
				t.Fatalf("IsImmutableStaticAsset(%q) = %v, want %v", tc.path, got, tc.immutable)
			}
			if got := StaticCacheControl(tc.path); got != tc.want {
				t.Fatalf("StaticCacheControl(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
