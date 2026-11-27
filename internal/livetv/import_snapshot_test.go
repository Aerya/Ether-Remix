package livetv

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

// A source import reads and decodes channels before taking the writer.
// Inside its transaction the snapshot stands in for reads only while the
// channel epoch is unchanged, and the IPTV channels it rebuilds from the
// snapshot plus the import's own writes are exactly what the table holds.
func TestImportSnapshotStandsInForTransactionReads(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	put := func(c Channel) []byte {
		t.Helper()
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, c.ID, b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	put(Channel{ID: "a1", SourceID: "a", Kind: "iptv", Name: "News", Number: "2", Enabled: true, StreamURL: "http://a/1"})
	put(Channel{ID: "a2", SourceID: "a", Kind: "iptv", Name: "Sport", Number: "1", Enabled: true, StreamURL: "http://a/2"})
	put(Channel{ID: "b1", SourceID: "b", Kind: "iptv", Name: "News", Number: "2", Enabled: true, StreamURL: "http://b/1"})
	put(Channel{ID: "v1", Kind: "virtual", Name: "Owner", Number: "3", Enabled: true})

	pre, err := readImportSnapshot(ctx, db, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got := pre.sourceRows("a"); len(got) != 2 || got["a1"] == nil || got["a2"] == nil {
		t.Fatalf("source rows: %v", got)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, fresh, err := pre.current(ctx, tx)
	if err != nil || !fresh || len(list) != 4 {
		t.Fatalf("unchanged epoch: fresh=%v %d channels, %v", fresh, len(list), err)
	}
	// The import rewrites a1, drops a2, adds a3; a merge rewrites b1.
	written := map[string][]byte{}
	for _, c := range []Channel{
		{ID: "a1", SourceID: "a", Kind: "iptv", Name: "News", Number: "2", Enabled: true, StreamURL: "http://a/1b"},
		{ID: "a3", SourceID: "a", Kind: "iptv", Name: "Movies", Number: "0", Enabled: true, StreamURL: "http://a/3"},
		{ID: "b1", SourceID: "b", Kind: "iptv", Name: "News", Number: "2", Enabled: false, StreamURL: "http://b/1"},
	} {
		b, _ := json.Marshal(c)
		if _, err := tx.ExecContext(ctx, `INSERT INTO livetv_channels(id,data) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, c.ID, b); err != nil {
			t.Fatal(err)
		}
		written[c.ID] = b
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM livetv_channels WHERE id='a2'`); err != nil {
		t.Fatal(err)
	}
	after, err := pre.iptvAfter("a", written)
	if err != nil {
		t.Fatal(err)
	}
	all, err := channels(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	if want := iptvOnly(all); !reflect.DeepEqual(after, want) {
		t.Fatalf("rebuilt IPTV channels differ\ngot:  %+v\nwant: %+v", after, want)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	// Any channel write after the snapshot (an owner edit) makes the
	// transaction read the table again.
	put(Channel{ID: "b1", SourceID: "b", Kind: "iptv", Name: "News", Number: "9", Enabled: true, StreamURL: "http://b/1"})
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	list, fresh, err = pre.current(ctx, tx)
	if err != nil || fresh {
		t.Fatalf("changed epoch still used the snapshot: %v", err)
	}
	for _, c := range list {
		if c.ID == "b1" && c.Number != "9" {
			t.Fatalf("stale read: %+v", c)
		}
	}
}
