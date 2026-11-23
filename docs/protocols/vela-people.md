# Vela v0 people, library rows, and library collections

All endpoints require normal Vela bearer/device headers (`Authorization: Bearer <token>`, `X-Vela-Device-Id`) and enforce current viewer policy, library publication/access, and device-scoped image capabilities. None of these read paths wait on external metadata providers or scan the full catalogue.

---

## 1. Title detail credit IDs (`GET /vela/v0/titles/{id}`)

`TitleDetail.cast[]` and `TitleDetail.crew[]` include `id` whenever the credit resolves to an authorized person facet on the title. That `id` is accepted by `/vela/v0/people/{id}` and `/vela/v0/people/{id}/titles`.

### Example (`TitleDetail` excerpt)

```json
{
  "title": {
    "id": "000000000000000100000e8d4a51002a",
    "kind": "movie",
    "name": "Arrival"
  },
  "cast": [
    {
      "id": "c84f1e29a7b340129d5e8a1b2c3d4e5f",
      "name": "Amy Adams",
      "role": "Louise Banks",
      "artwork": {
        "id": "8e1c...",
        "kind": "person",
        "tag": "v1",
        "sizes": [
          {
            "width": 185,
            "height": 278,
            "url": "/vela/v0/images/abc123...?h=278&w=185"
          }
        ],
        "blurHash": "",
        "thumbHash": ""
      }
    }
  ],
  "crew": [
    {
      "id": "d91a0f3b8c274e56a1b2c3d4e5f60718",
      "name": "Denis Villeneuve",
      "role": "Director"
    }
  ]
}
```

---

## 2. Person detail (`GET /vela/v0/people/{id}`)

Returns a person visible to the viewer through at least one accessible title credit. Returns `404` (`not_found`) if the person ID is unknown or has no visible credits for the viewer.

- `photo` uses the same viewer/device-scoped capability signing and `ArtworkRef` shape as `TitleDetail.cast[].artwork` (`null` when no portrait is available).
- `roles` lists the canonical departments (`"Acting"`, `"Directing"`, `"Writing"`, `"Crew"`) across the viewer's visible credits, ordered by visible credit count descending.
- `knownFor` reports the primary department (provider department when cached, falling back to `roles[0]`, or `""`).
- `biography` and `birthday` (`YYYY-MM-DD`) are included when non-empty.

### Request

```http
GET /vela/v0/people/c84f1e29a7b340129d5e8a1b2c3d4e5f HTTP/1.1
Authorization: Bearer <access-token>
X-Vela-Device-Id: apple-tv-living-room
```

### Response (`200 OK`)

```json
{
  "id": "c84f1e29a7b340129d5e8a1b2c3d4e5f",
  "name": "Denis Villeneuve",
  "photo": {
    "id": "3f9a...",
    "kind": "person",
    "tag": "v1",
    "sizes": [
      {
        "width": 185,
        "height": 278,
        "url": "/vela/v0/images/XyZ987...?h=278&w=185"
      }
    ],
    "blurHash": "",
    "thumbHash": ""
  },
  "knownFor": "Directing",
  "biography": "Canadian filmmaker.",
  "birthday": "1967-10-03",
  "roles": ["Directing", "Writing"]
}
```

---

## 3. Person filmography (`GET /vela/v0/people/{id}/titles`)

Returns a `Page<RowEntry>` of titles in the viewer's visible Vela libraries where the person is credited as cast or crew, ordered by release date newest first (`released` descending).

### Query parameters

| Parameter | Type | Description |
| --- | --- | --- |
| `libraryId` | `string` (optional) | Restrict results to one visible Vela library ID. Returns `404` if the library is not visible to the viewer. |
| `limit` | `int` (optional) | Page size (`1..100`, default `50`). |
| `cursor` | `string` (optional) | Opaque continuation cursor from `nextCursor` (`409` `cursor_expired` when stale or mismatched). |

Each `RowEntry` has `kind: "title"`, `title` (`Title`), `role` (the person's character or job on that title, e.g. `"Louise Banks"` or `"Director, Writer"`), and `receiptToken: ""`.

### Request

```http
GET /vela/v0/people/c84f1e29a7b340129d5e8a1b2c3d4e5f/titles?libraryId=1&limit=20 HTTP/1.1
Authorization: Bearer <access-token>
X-Vela-Device-Id: apple-tv-living-room
```

### Response (`200 OK`)

```json
{
  "items": [
    {
      "id": "000000000000000100000e8d4a51002a",
      "kind": "title",
      "role": "Director, Writer",
      "receiptToken": "",
      "title": {
        "id": "000000000000000100000e8d4a51002a",
        "kind": "movie",
        "name": "Dune: Part Two",
        "summary": "Paul Atreides unites with Chani and the Fremen.",
        "year": 2024,
        "genres": ["Science Fiction", "Adventure"],
        "artwork": [],
        "watch": {
          "positionSeconds": 0,
          "played": false,
          "favorite": false,
          "watchlisted": true
        }
      }
    }
  ],
  "continuation": "more",
  "nextCursor": "eyJ..."
}
```

---

## 4. Library-scoped recommendation rows (`GET /vela/v0/rows`)

`RowsQuery` accepts optional `libraryId` when `placement=library`, returning recommendation/continue/genre rows scoped to that library (including custom filtered/collection libraries).

- Passing `libraryId` with `placement` other than `library` (or repeating `libraryId`) returns `400` (`invalid_request`).
- Passing a `libraryId` not present in the viewer's visible Vela libraries returns `404` (`not_found`).
- Individual rows continue to paginate through `GET /vela/v0/rows/{id}?cursor=...` using each row's `nextCursor`.

### Request

```http
GET /vela/v0/rows?placement=library&libraryId=23&limit=10 HTTP/1.1
Authorization: Bearer <access-token>
X-Vela-Device-Id: apple-tv-living-room
```

### Response (`200 OK`)

```json
{
  "items": [
    {
      "id": "rv_01j...",
      "name": "Recently Added Movies",
      "purpose": "recent",
      "receiptToken": "imp_01j...",
      "style": "shelf",
      "items": [
        {
          "id": "000000000000000100000e8d4a51002a",
          "kind": "title",
          "title": {
            "id": "000000000000000100000e8d4a51002a",
            "kind": "movie",
            "name": "Arrival",
            "summary": "...",
            "genres": ["Science Fiction"],
            "artwork": [],
            "watch": {
              "positionSeconds": 0,
              "played": false,
              "favorite": false,
              "watchlisted": false
            }
          },
          "receiptToken": "ent_01j..."
        }
      ],
      "continuation": "more",
      "nextCursor": "eyJ..."
    }
  ],
  "continuation": "exhausted"
}
```

---

## 5. Collections per library & collection browse filter

### Listing collections in a library (`GET /vela/v0/libraries/{id}/collections`)

Returns `Page<RowEntry>` of visible collections that have at least one visible member in library `{id}`, with `itemCount` scoped to that library and signed poster/backdrop `artwork`.

- Supports `limit` (`1..100`, default `50`) and `cursor`.
- Returns `404` (`not_found`) if library `{id}` is not visible to the viewer.

#### Request

```http
GET /vela/v0/libraries/1/collections?limit=20 HTTP/1.1
Authorization: Bearer <access-token>
X-Vela-Device-Id: apple-tv-living-room
```

#### Response (`200 OK`)

```json
{
  "items": [
    {
      "id": "0000000000000001000048c27395000a",
      "kind": "collection",
      "receiptToken": "",
      "collection": {
        "id": "0000000000000001000048c27395000a",
        "name": "Dune Collection",
        "summary": "Science fiction saga.",
        "itemCount": 2,
        "artwork": [
          {
            "id": "91ab...",
            "kind": "poster",
            "tag": "v1",
            "sizes": [
              {
                "width": 1000,
                "height": 1500,
                "url": "/vela/v0/images/def456...?h=1500&w=1000"
              }
            ],
            "blurHash": "",
            "thumbHash": ""
          }
        ]
      }
    }
  ],
  "continuation": "exhausted"
}
```

### Browsing a collection's titles (`GET /vela/v0/libraries/{id}/items?collectionId={guid}`)

Native libraries advertise `"collectionId": true` under `library.browse.filters` in `GET /vela/v0/libraries`. Passing `collectionId=0000000000000001000048c27395000a` to `GET /vela/v0/libraries/{id}/items` returns a `LibraryBrowsePage` of that collection's visible titles in library `{id}`, supporting existing `sort`, `direction`, `genre`, `year`, `played`, `favorite`, `limit`, and `cursor` parameters.
