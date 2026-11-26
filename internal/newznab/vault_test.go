package newznab

import (
	"context"
	"sync"

	"github.com/braedonsaunders/ether/internal/vault"
)

type testVault struct {
	vault.Store
	records sync.Map
}

func (v *testVault) Put(_ context.Context, r vault.PutRequest) (vault.Key, error) {
	rec := &vault.Record{XML: append([]byte(nil), r.NZB...)}
	var primary vault.Key
	storeAlias := func(id string) {
		if id == "" {
			return
		}
		k := vault.Key(r.Release.IndexerID + "\x00" + id)
		if primary == "" {
			primary = k
		}
		v.records.Store(k, rec)
	}
	storeAlias(r.Release.ID)
	for _, alias := range r.Aliases {
		storeAlias(alias)
	}
	return primary, nil
}
func (v *testVault) ByAlias(_ context.Context, source, id string) (vault.Key, bool, error) {
	k := vault.Key(source + "\x00" + id)
	_, ok := v.records.Load(k)
	return k, ok, nil
}
func (v *testVault) Get(_ context.Context, k vault.Key) (*vault.Record, error) {
	r, ok := v.records.Load(k)
	if !ok {
		return nil, vault.ErrPayloadMissing
	}
	return r.(*vault.Record), nil
}
func (*testVault) Touch(context.Context, vault.Key, bool) error { return nil }
