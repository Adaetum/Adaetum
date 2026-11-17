package backups

import (
	"context"
	"strings"
	"testing"
)

func syncChanger(vault *fakeVault, got *Changes) *SyncChanger {
	return &SyncChanger{Vault: vault, Propose: func(_ context.Context, changes Changes) (string, string, error) {
		*got = changes
		return "https://pr", "b", nil
	}}
}

func TestSyncProposesEverythingTheFirstTimeAndUsesTheStoredProvider(t *testing.T) {
	for provider, want := range map[string]string{"s3": "AWS_ACCESS_KEY_ID", "azure": "AZURE_ACCOUNT_KEY"} {
		vault := newVault()
		vault.data[SyncVaultPath] = map[string]any{"PROVIDER": provider, "AWS_REGION": "auto"}
		var got Changes
		if _, err := syncChanger(vault, &got).Change(context.Background(), SyncChange{Namespace: "media", Claim: "library", Enable: true, FirstUse: true}); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"pods/volsync/volsync.app.yaml", "pods/volsync/media-library.app.yaml", "pods/volsync/sync/media-library/replication-source.yaml", "pods/volsync/sync/media-library/restic-secret.yaml"} {
			if got.Create[path] == "" {
				t.Errorf("%s: missing %s", provider, path)
			}
		}
		secret := got.Create["pods/volsync/sync/media-library/restic-secret.yaml"]
		if !strings.Contains(secret, want) || !strings.Contains(secret, "volsync/media/library") || strings.Contains(secret, "RESTIC_PASSWORD: \"s") {
			t.Errorf("%s: %s", provider, secret)
		}
	}
}

func TestSyncNeedsItsStorageChosenFirst(t *testing.T) {
	var got Changes
	if _, err := syncChanger(newVault(), &got).Change(context.Background(), SyncChange{Namespace: "media", Claim: "library", Enable: true}); err != ErrSyncStorage || got.Prefix != "" {
		t.Fatalf("got %v", err)
	}
}

func TestLeavingSyncRemovesFilesAndTheOperatorWhenLast(t *testing.T) {
	var got Changes
	if _, err := syncChanger(newVault(), &got).Change(context.Background(), SyncChange{Namespace: "media", Claim: "library", LastUse: true}); err != nil {
		t.Fatal(err)
	}
	if len(got.Remove) != 5 {
		t.Errorf("removed %v", got.Remove)
	}
}

func TestAVolumeOnBothModesIsReadAsBothUntilTheCopyExists(t *testing.T) {
	c := library(map[string]string{dailyLabel: "enabled"})
	c.noSync, c.synced = false, map[string]bool{"media/library": true}
	state, err := Collect(context.Background(), c.get, mustTime("2026-11-17T00:10:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if v := state.Volumes[0]; v.Mode != "both" || v.State != "protected" || v.Schedule != "daily" {
		t.Errorf("%+v", v)
	}
	c.noSync, c.synced = true, nil
	state, _ = Collect(context.Background(), c.get, mustTime("2026-11-17T00:10:00Z"))
	if state.Volumes[0].Mode != "scheduled" || state.SyncInstalled {
		t.Errorf("%+v", state.Volumes[0])
	}
}
