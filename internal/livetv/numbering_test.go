package livetv

import (
	"context"
	"testing"
	"time"
)

func TestNumberReservationsSurviveDisappearance(t *testing.T) {
	s := testService(t, Options{})
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	assign := func(cs []Channel, at time.Time) []Channel {
		t.Helper()
		tx, err := s.o.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = assignImportedNumbers(ctx, tx, "s", cs, nil, nil, at, false); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
		return cs
	}
	first := assign([]Channel{{ID: "a", Group: "News"}, {ID: "b", Group: "News"}, {ID: "c", Group: "Sports"}}, now)
	if first[0].Number != "1" || first[1].Number != "2" || first[2].Number != "1" {
		t.Fatalf("first: %+v", first)
	}
	second := assign([]Channel{{ID: "b", Group: "News"}, {ID: "d", Group: "News"}}, now.Add(24*time.Hour))
	if second[0].Number != "2" || second[1].Number != "3" {
		t.Fatalf("second: %+v", second)
	}
	returned := assign([]Channel{{ID: "a", Group: "News"}}, now.Add(2*24*time.Hour))
	if returned[0].Number != "1" {
		t.Fatalf("returned: %+v", returned)
	}
	expired := assign([]Channel{{ID: "e", Group: "News"}}, now.Add(10*24*time.Hour))
	if expired[0].Number != "1" {
		t.Fatalf("expired: %+v", expired)
	}
}

func TestPluginSourceNumbersAvoidOtherSources(t *testing.T) {
	s := testService(t, Options{})
	ctx := context.Background()
	tx, err := s.o.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	others := []Channel{{ID: "bbc1", SourceID: "m3u", Number: "101"}, {ID: "bbc2", SourceID: "m3u", Number: "102"}, {ID: "itv", SourceID: "m3u", Number: "104"}}
	cs := []Channel{{ID: "cpac", Number: "101", ProviderNumber: "101"}, {ID: "gnn", Number: "103", ProviderNumber: "103"}, {ID: "gnt", Number: "104", ProviderNumber: "104"}}
	if err := assignImportedNumbers(ctx, tx, "plugin", cs, nil, others, time.Now(), true); err != nil {
		t.Fatal(err)
	}
	// 103 is free and kept; 101 is taken -> 105 (102, 103, 104 taken); 104 taken -> 106.
	if cs[0].Number != "105" || cs[1].Number != "103" || cs[2].Number != "106" {
		t.Fatalf("numbers %s %s %s", cs[0].Number, cs[1].Number, cs[2].Number)
	}
}
