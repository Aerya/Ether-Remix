package config

import (
	"reflect"
	"testing"
)

func TestQuickSourcesPersistWithoutChangingAdvancedSettings(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get().Resolver.SourceOrder.Quick.Enabled {
		t.Fatal("enabled by default")
	}
	order := SourceOrder{Families: []string{"usenet", "stremio"}, Content: map[string][]string{"anime": {"a"}}, Quick: QuickSources{Enabled: true, Order: []string{"seedstrem", "alko", "usenet", "torrent"}, SeedstremID: "my-seed"}}
	if err := s.Update(func(c *Config) error { c.Resolver.SourceOrder = order; return nil }); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reopened.Get().Resolver.SourceOrder, order) {
		t.Fatal("configuration did not survive reopening")
	}
	if err := reopened.Update(func(c *Config) error { c.Resolver.SourceOrder.Quick.Enabled = false; return nil }); err != nil {
		t.Fatal(err)
	}
	order.Quick.Enabled = false
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Get().Resolver.SourceOrder, order) {
		t.Fatal("disabling changed advanced settings or selection")
	}
}
