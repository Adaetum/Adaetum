package backups

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func enabler(vault *fakeVault, base string, got *Changes) *Enabler {
	return &Enabler{Vault: vault, CloudflareBase: base, Propose: func(_ context.Context, changes Changes) (string, string, error) {
		*got = changes
		return "https://gitea/pulls/1", "b", nil
	}}
}

func TestR2IsMadeForYouAndProposesTheTargetAndSchedule(t *testing.T) {
	server, _ := fakeCloudflare(t, 1)
	vault, got := newVault(), Changes{}
	result, err := enabler(vault, server.URL, &got).Configure(context.Background(), Request{Tier: TierScheduled, Token: "secret-token", Storage: Storage{Provider: "r2"}})
	if err != nil {
		t.Fatal(err)
	}
	stored := vault.written[VaultPath]
	if stored["AWS_ACCESS_KEY_ID"] != "key-id" || stored["AWS_SECRET_ACCESS_KEY"] == "key-value" || stored["AWS_ENDPOINTS"] != "https://acct1.r2.cloudflarestorage.com" || stored["AWS_REGION"] != "auto" {
		t.Errorf("stored: %v", stored)
	}
	if result.ProposalURL == "" || !strings.Contains(got.Create["pods/longhorn-system/backups/backup-target.yaml"], "s3://"+result.Bucket+"@auto/") {
		t.Errorf("proposal: %v", got.Create)
	}
	if !strings.Contains(got.Create[schedulesFile], "backup-daily") {
		t.Error("a first schedule is proposed with the target")
	}
	for path, text := range got.Create {
		if strings.Contains(text, "key-value") || strings.Contains(text, "secret-token") || strings.Contains(text, "key-id") {
			t.Errorf("%s carries a secret", path)
		}
	}
}

func TestEachProviderGetsItsOwnTargetAndCredentialNames(t *testing.T) {
	cases := map[string]struct {
		storage Storage
		url     string
		secret  string
		restic  string
	}{
		"aws":    {Storage{Provider: "s3", Bucket: "mybucket", Region: "eu-west-1", AccessKeyID: "a", SecretAccessKey: "b"}, "s3://mybucket@eu-west-1/", "AWS_ACCESS_KEY_ID", "s3:s3.eu-west-1.amazonaws.com/mybucket/"},
		"google": {Storage{Provider: "s3", Bucket: "mybucket", Endpoint: "https://storage.googleapis.com", Region: "auto", AccessKeyID: "a", SecretAccessKey: "b"}, "s3://mybucket@auto/", "AWS_ENDPOINTS", "s3:https://storage.googleapis.com/mybucket/"},
		"azure":  {Storage{Provider: "azure", Account: "acct", Container: "mycontainer", AccountKey: "k"}, "azblob://mycontainer@core.windows.net/", "AZBLOB_ACCOUNT_KEY", "azure:mycontainer:/"},
	}
	for name, c := range cases {
		vault, got := newVault(), Changes{}
		if _, err := enabler(vault, "", &got).Configure(context.Background(), Request{Tier: TierScheduled, Storage: c.storage}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(got.Create["pods/longhorn-system/backups/backup-target.yaml"], c.url) || !strings.Contains(got.Create["pods/longhorn-system/backups/external-secret.yaml"], c.secret) {
			t.Errorf("%s: %v", name, got.Create)
		}
		if vault.written[VaultPath]["RESTIC_BASE"] != c.restic {
			t.Errorf("%s: restic base %v", name, vault.written[VaultPath]["RESTIC_BASE"])
		}
	}
}

func TestBadStorageIsRefusedBeforeAnythingIsKept(t *testing.T) {
	for name, storage := range map[string]Storage{
		"no keys":       {Provider: "s3", Bucket: "mybucket", Region: "x"},
		"http endpoint": {Provider: "s3", Bucket: "mybucket", Endpoint: "http://insecure", AccessKeyID: "a", SecretAccessKey: "b"},
		"no region":     {Provider: "s3", Bucket: "mybucket", AccessKeyID: "a", SecretAccessKey: "b"},
		"azure no key":  {Provider: "azure", Container: "c", Account: "a"},
		"unknown":       {Provider: "ftp"},
	} {
		vault, got := newVault(), Changes{}
		if _, err := enabler(vault, "", &got).Configure(context.Background(), Request{Tier: TierScheduled, Storage: storage}); err == nil || len(vault.written) != 0 {
			t.Errorf("%s: %v %v", name, err, vault.written)
		}
	}
}

func TestSyncHasItsOwnStorageAndPasswordAndNoPullRequest(t *testing.T) {
	vault, got := newVault(), Changes{}
	result, err := enabler(vault, "", &got).Configure(context.Background(), Request{Tier: TierSync, Storage: Storage{Provider: "azure", Account: "a", Container: "c", AccountKey: "k"}})
	if err != nil || result.ProposalURL != "" || got.Prefix != "" {
		t.Fatalf("%v %v %v", err, result, got)
	}
	if vault.written[SyncVaultPath]["RESTIC_PASSWORD"] == "" || vault.written[VaultPath] != nil {
		t.Errorf("%v", vault.written)
	}
}

func TestSyncCanReuseTheScheduledStorageButOnlyOnceItExists(t *testing.T) {
	vault, got := newVault(), Changes{}
	if _, err := enabler(vault, "", &got).Configure(context.Background(), Request{Tier: TierSync, Same: true}); err == nil {
		t.Fatal("nothing to reuse yet")
	}
	vault.data[VaultPath] = Storage{Provider: "s3", Bucket: "bucket", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "b"}.fields()
	if _, err := enabler(vault, "", &got).Configure(context.Background(), Request{Tier: TierSync, Same: true}); err != nil {
		t.Fatal(err)
	}
	if vault.written[SyncVaultPath]["BUCKET"] != "bucket" || vault.written[SyncVaultPath]["RESTIC_PASSWORD"] == "" {
		t.Errorf("%v", vault.written)
	}
}

func TestSeveralCloudflareAccountsAskWhichBeforeCreatingAnything(t *testing.T) {
	server, calls := fakeCloudflare(t, 2)
	_, err := enabler(newVault(), server.URL, &Changes{}).Configure(context.Background(), Request{Tier: TierScheduled, Token: "secret-token", Storage: Storage{Provider: "r2"}})
	var choice *AccountChoiceError
	if !errors.As(err, &choice) || len(choice.Accounts) != 2 {
		t.Fatalf("got %v", err)
	}
	for _, call := range *calls {
		if strings.Contains(call, "r2/buckets") {
			t.Error("nothing should be created before the account is chosen")
		}
	}
}

func TestARepeatReusesWhatIsKept(t *testing.T) {
	server, calls := fakeCloudflare(t, 1)
	vault := newVault()
	vault.data[VaultPath] = Storage{Provider: "r2", Bucket: "adaetum-scheduled-1", AccessKeyID: "a", SecretAccessKey: "b", Endpoint: "https://x"}.fields()
	if _, err := enabler(vault, server.URL, &Changes{}).Configure(context.Background(), Request{Tier: TierScheduled}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 || len(vault.written) != 0 {
		t.Errorf("a retry should not touch Cloudflare or write again: %v", *calls)
	}
}

func TestAWrongTokenIsRefusedWithoutEchoingIt(t *testing.T) {
	server, _ := fakeCloudflare(t, 1)
	_, err := enabler(newVault(), server.URL, &Changes{}).Configure(context.Background(), Request{Tier: TierScheduled, Token: "wrong-token", Storage: Storage{Provider: "r2"}})
	if err == nil || strings.Contains(err.Error(), "wrong-token") {
		t.Fatalf("got %v", err)
	}
}

func TestNothingHappensWithoutARecord(t *testing.T) {
	mux := http.NewServeMux()
	(&StorageAPI{Enabler: &Enabler{}}).Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/backups/storage", strings.NewReader(`{"tier":"sync"}`)))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d", rec.Code)
	}
}
