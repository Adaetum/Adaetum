package inspect

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClusterReportCarriesTheVaultState(t *testing.T) {
	empty := func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("not here") }
	raw := func(context.Context, string) ([]byte, error) { return nil, errors.New("not here") }
	inspector := NewClusterInspector(empty, raw)
	inspector.Now = time.Now
	if report := inspector.CollectCluster(context.Background()); report.RKE2.Vault != nil {
		t.Fatal("without a vault check there is nothing to report")
	}
	inspector.Vault = func(context.Context) *Vault { return &Vault{Reachable: true, Sealed: true} }
	vault := inspector.CollectCluster(context.Background()).RKE2.Vault
	if vault == nil || !vault.Reachable || !vault.Sealed {
		t.Fatalf("got %+v", vault)
	}
}
