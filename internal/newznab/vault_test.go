package newznab

import (
	"context"
	"github.com/braedonsaunders/ether/internal/vault"
	"sync"
)

type testVault struct {
	vault.Store
	records sync.Map
}

func (v *testVault) Put(_ context.Context, r vault.PutRequest) (vault.Key, error) {
	k := vault.Key(r.Release.IndexerID + "\x00" + r.Release.ID)
	v.records.Store(k, &vault.Record{XML: append([]byte(nil), r.NZB...)})
	return k, nil
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
