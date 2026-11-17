package backups

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Each tier (scheduled backups, sync) has its own storage, set up on its own tab, so one can go
// to Cloudflare R2 and the other to AWS, Google or Azure, or both to the same place. Choosing
// storage does these things, in this order, and stops at the first failure:
//
//  1. R2 only: the operator's pasted Cloudflare token (used for this request, never stored)
//     makes a bucket and a key limited to it. Other providers need the bucket to exist and the
//     keys are typed in; the agent does not create cloud accounts or buckets it cannot undo.
//  2. OpenBao: keep the credentials at the tier's path. Kubernetes materialises them through
//     External Secrets, so they never appear in git.
//  3. Scheduled backups only: a pull request declares the Longhorn backup target and the
//     schedules. Sync's storage needs no pull request: each volume put on sync proposes its own.
//
// A repeat after a failed step reuses what OpenBao already holds instead of making a second
// bucket, so retrying cannot leave orphans behind. Once stored, credentials are not changed from
// here (the agent may create them but not overwrite them): to change them, remove the secret in
// OpenBao and choose storage again.

// Tier names which copy a piece of storage serves.
const (
	TierScheduled = "scheduled"
	TierSync      = "sync"
)

// VaultPath is where the scheduled tier's credentials live (the "secret" KV mount); SyncVaultPath
// is the sync tier's.
const VaultPath = "apps/longhorn-system/backup"

func vaultPath(tier string) string {
	if tier == TierSync {
		return SyncVaultPath
	}
	return VaultPath
}

var dns1123 = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

// Vault is the part of OpenBao this needs.
type Vault interface {
	ReadFields(ctx context.Context, path string) (fields map[string]any, version int, found bool, err error)
	WriteFields(ctx context.Context, path string, fields map[string]any, expectedVersion int) error
}

// Changes is one pull request: files to add, files to replace, files to delete.
type Changes struct {
	Prefix, Title, Body string
	Create, Update      map[string]string
	Remove              []string
}

// Proposer opens a pull request on the cluster repository.
type Proposer func(ctx context.Context, changes Changes) (url, branch string, err error)

// Storage is where a tier copies to.
type Storage struct {
	// Provider is "r2" (Cloudflare R2, made for you), "s3" (any S3-compatible service: AWS,
	// Google Cloud Storage through its interoperability keys, Backblaze, MinIO...), or "azure".
	Provider string `json:"provider"`
	// S3 and R2.
	Endpoint        string `json:"endpoint,omitempty"`
	Region          string `json:"region,omitempty"`
	Bucket          string `json:"bucket,omitempty"`
	AccessKeyID     string `json:"accessKeyId,omitempty"`
	SecretAccessKey string `json:"secretAccessKey,omitempty"`
	// Azure Blob Storage.
	Account    string `json:"account,omitempty"`
	Container  string `json:"container,omitempty"`
	AccountKey string `json:"accountKey,omitempty"`
}

// StorageInfo is what the page may know about stored credentials: where, never the keys.
type StorageInfo struct {
	Configured bool   `json:"configured"`
	Provider   string `json:"provider,omitempty"`
	Bucket     string `json:"bucket,omitempty"`
}

func (s Storage) check() error {
	switch s.Provider {
	case "r2", "s3":
		if !dns1123.MatchString(s.Bucket) || len(s.Bucket) < 3 || strings.TrimSpace(s.AccessKeyID) == "" || strings.TrimSpace(s.SecretAccessKey) == "" {
			return errors.New("S3 storage needs a bucket name, an access key id and a secret access key")
		}
		if s.Provider == "s3" && s.Endpoint != "" {
			if u, err := url.Parse(s.Endpoint); err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" && u.Path != "/" {
				return errors.New("the endpoint must be a bare https address such as https://storage.googleapis.com")
			}
		}
		if s.Provider == "s3" && s.Endpoint == "" && s.Region == "" {
			return errors.New("AWS S3 needs a region such as us-east-1")
		}
	case "azure":
		if !dns1123.MatchString(s.Container) || strings.TrimSpace(s.Account) == "" || strings.TrimSpace(s.AccountKey) == "" {
			return errors.New("Azure storage needs an account name, a container and an account key")
		}
	default:
		return errors.New("choose R2, S3-compatible or Azure storage")
	}
	return nil
}

// target is the name of the bucket or container.
func (s Storage) target() string {
	if s.Provider == "azure" {
		return s.Container
	}
	return s.Bucket
}

// fields are what is kept in OpenBao: one name per setting, the credentials under the names the
// copy tools read. RESTIC_BASE is where a restic repository can be made (always ends in "/").
func (s Storage) fields() map[string]any {
	fields := map[string]any{"PROVIDER": s.Provider, "BUCKET": s.target()}
	if s.Provider == "azure" {
		fields["AZURE_ACCOUNT_NAME"] = strings.TrimSpace(s.Account)
		fields["AZURE_ACCOUNT_KEY"] = strings.TrimSpace(s.AccountKey)
		fields["RESTIC_BASE"] = "azure:" + s.Container + ":/"
		return fields
	}
	fields["AWS_ACCESS_KEY_ID"] = strings.TrimSpace(s.AccessKeyID)
	fields["AWS_SECRET_ACCESS_KEY"] = strings.TrimSpace(s.SecretAccessKey)
	region := s.Region
	if s.Provider == "r2" {
		region = "auto"
	}
	if region != "" {
		fields["AWS_REGION"] = region
	}
	endpoint := strings.TrimRight(s.Endpoint, "/")
	if endpoint != "" {
		fields["AWS_ENDPOINTS"] = endpoint
		fields["RESTIC_BASE"] = "s3:" + endpoint + "/" + s.Bucket + "/"
	} else {
		fields["RESTIC_BASE"] = "s3:s3." + region + ".amazonaws.com/" + s.Bucket + "/"
	}
	return fields
}

// Info is the safe summary of stored fields.
func infoOf(fields map[string]any, found bool) StorageInfo {
	if !found {
		return StorageInfo{}
	}
	provider, _ := fields["PROVIDER"].(string)
	bucket, _ := fields["BUCKET"].(string)
	return StorageInfo{Configured: provider != "", Provider: provider, Bucket: bucket}
}

// Enabler holds what choosing storage needs.
type Enabler struct {
	Vault   Vault
	Propose Proposer
	// CloudflareBase is the Cloudflare API root; empty means the real one. Tests point it elsewhere.
	CloudflareBase string
	HTTP           *http.Client
}

// Request is a choice of storage for one tier.
type Request struct {
	Tier string `json:"tier"`
	// Same (sync only) reuses the scheduled tier's storage and credentials.
	Same bool `json:"same"`
	// Token is a Cloudflare token for provider r2; it is used once and not kept.
	Token     string `json:"token"`
	AccountID string `json:"accountId"`
	Storage
}

// Result is what the operator sees afterwards.
type Result struct {
	Bucket      string `json:"bucket"`
	ProposalURL string `json:"proposalUrl,omitempty"`
}

// AccountChoiceError means the token can see several Cloudflare accounts and the operator
// must say which one holds the bucket.
type AccountChoiceError struct{ Accounts []Account }

func (e *AccountChoiceError) Error() string {
	return "This token can use more than one Cloudflare account. Choose which one should hold the backups."
}

// Account is a Cloudflare account the token can see.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Configure stores the tier's credentials and, for scheduled backups, proposes the target.
func (e *Enabler) Configure(ctx context.Context, request Request) (Result, error) {
	if request.Tier != TierScheduled && request.Tier != TierSync {
		return Result{}, errors.New("choose scheduled backups or sync")
	}
	path := vaultPath(request.Tier)
	fields, _, found, err := e.Vault.ReadFields(ctx, path)
	if err != nil {
		return Result{}, fmt.Errorf("could not check OpenBao: %w", err)
	}
	if !found {
		if fields, err = e.store(ctx, request, path); err != nil {
			var choice *AccountChoiceError
			if errors.As(err, &choice) {
				return Result{}, err
			}
			// Cloudflare's error text should never echo a secret, but this is the one place they are held.
			message := err.Error()
			for _, secret := range []string{request.Token, request.SecretAccessKey, request.AccountKey} {
				if strings.TrimSpace(secret) != "" {
					message = strings.ReplaceAll(message, strings.TrimSpace(secret), "[secret]")
				}
			}
			return Result{}, errors.New(message)
		}
	}
	bucket, _ := fields["BUCKET"].(string)
	if bucket == "" {
		return Result{}, errors.New("the stored storage settings in OpenBao are incomplete")
	}
	if request.Tier == TierSync {
		return Result{Bucket: bucket}, nil
	}
	changes, err := scheduledChanges(fields)
	if err != nil {
		return Result{Bucket: bucket}, err
	}
	proposalURL, _, err := e.Propose(ctx, changes)
	if err != nil {
		return Result{Bucket: bucket}, fmt.Errorf("The storage is ready, but the pull request failed (trying again will reuse it): %w", err)
	}
	return Result{Bucket: bucket, ProposalURL: proposalURL}, nil
}

// store works out the storage, keeps it in OpenBao and returns what was kept.
func (e *Enabler) store(ctx context.Context, request Request, path string) (map[string]any, error) {
	var fields map[string]any
	switch {
	case request.Same:
		if request.Tier != TierSync {
			return nil, errors.New("only sync can reuse the scheduled backups' storage")
		}
		scheduled, _, found, err := e.Vault.ReadFields(ctx, VaultPath)
		if err != nil {
			return nil, fmt.Errorf("could not read the scheduled backups' storage: %w", err)
		}
		if !found {
			return nil, errors.New("Set up the scheduled backups' storage first, or choose storage for sync")
		}
		fields = map[string]any{}
		for key, value := range scheduled {
			fields[key] = value
		}
	case request.Provider == "r2":
		if strings.TrimSpace(request.Token) == "" {
			return nil, errors.New("Paste a Cloudflare token that can create R2 buckets and API tokens. It is used once and not kept.")
		}
		made, err := e.provisionR2(ctx, strings.TrimSpace(request.Token), request.AccountID, request.Tier)
		if err != nil {
			return nil, err
		}
		fields = made.fields()
	default:
		if err := request.Storage.check(); err != nil {
			return nil, err
		}
		fields = request.Storage.fields()
	}
	if request.Tier == TierSync {
		var password [32]byte
		if _, err := rand.Read(password[:]); err != nil {
			return nil, err
		}
		// Every synced volume is encrypted with this before it leaves the cluster.
		fields["RESTIC_PASSWORD"] = hex.EncodeToString(password[:])
	}
	if err := e.Vault.WriteFields(ctx, path, fields, 0); err != nil {
		return nil, fmt.Errorf("keeping the credentials in OpenBao failed (anything made at the provider is now unused): %w", err)
	}
	return fields, nil
}

func (e *Enabler) provisionR2(ctx context.Context, token, accountID, tier string) (Storage, error) {
	c := &cloudflare{base: e.CloudflareBase, token: token, http: e.HTTP}
	if accountID == "" {
		accounts, err := c.accounts(ctx)
		if err != nil {
			return Storage{}, err
		}
		switch len(accounts) {
		case 0:
			return Storage{}, errors.New("Cloudflare shows no account for that token")
		case 1:
			accountID = accounts[0].ID
		default:
			return Storage{}, &AccountChoiceError{Accounts: accounts}
		}
	}
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		return Storage{}, err
	}
	bucket := "adaetum-" + tier + "-" + hex.EncodeToString(random[:])
	if err := c.do(ctx, http.MethodPost, "/accounts/"+url.PathEscape(accountID)+"/r2/buckets", map[string]string{"name": bucket}, nil); err != nil {
		return Storage{}, fmt.Errorf("creating the bucket: %w", err)
	}
	keyID, secret, err := c.bucketKey(ctx, accountID, bucket)
	if err != nil {
		return Storage{}, fmt.Errorf("creating the bucket's key (the empty bucket %s was left in place): %w", bucket, err)
	}
	return Storage{Provider: "r2", Bucket: bucket, AccessKeyID: keyID, SecretAccessKey: secret, Endpoint: "https://" + accountID + ".r2.cloudflarestorage.com"}, nil
}

// cloudflare is the few Cloudflare API calls this uses.
type cloudflare struct {
	base, token string
	http        *http.Client
}

func (c *cloudflare) do(ctx context.Context, method, path string, body, into any) error {
	base := c.base
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "adaetum-agent")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := c.http
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Cloudflare: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var envelope struct {
		Success bool `json:"success"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	_ = json.Unmarshal(raw, &envelope)
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("Cloudflare refused that token. It needs Account API Tokens Write and Workers R2 Storage Write on the account")
	}
	if resp.StatusCode >= 400 || !envelope.Success {
		message := fmt.Sprintf("Cloudflare answered HTTP %d", resp.StatusCode)
		if len(envelope.Errors) > 0 {
			message += ": " + envelope.Errors[0].Message
		}
		return errors.New(message)
	}
	if into != nil {
		if err := json.Unmarshal(envelope.Result, into); err != nil {
			return errors.New("Cloudflare's answer was unreadable")
		}
	}
	return nil
}

func (c *cloudflare) accounts(ctx context.Context) ([]Account, error) {
	var accounts []Account
	if err := c.do(ctx, http.MethodGet, "/accounts", nil, &accounts); err != nil {
		return nil, err
	}
	return accounts, nil
}

// bucketKey makes an account API token that can read and write objects in this one bucket and
// returns it as an S3 key pair. R2's S3 API takes the token's id as the access key id and the
// SHA-256 of the token's value as the secret.
func (c *cloudflare) bucketKey(ctx context.Context, accountID, bucket string) (id, secret string, err error) {
	var groups []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	account := "/accounts/" + url.PathEscape(accountID)
	if err := c.do(ctx, http.MethodGet, account+"/tokens/permission_groups", nil, &groups); err != nil {
		return "", "", err
	}
	var permissions []map[string]string
	for _, group := range groups {
		if group.Name == "Workers R2 Storage Bucket Item Read" || group.Name == "Workers R2 Storage Bucket Item Write" {
			permissions = append(permissions, map[string]string{"id": group.ID})
		}
	}
	if len(permissions) != 2 {
		return "", "", errors.New("Cloudflare does not list the bucket read and write permissions")
	}
	policy := []map[string]any{{
		"effect":            "allow",
		"permission_groups": permissions,
		"resources":         map[string]string{"com.cloudflare.edge.r2.bucket." + accountID + "_default_" + bucket: "*"},
	}}
	var made struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	}
	if err := c.do(ctx, http.MethodPost, account+"/tokens", map[string]any{"name": "adaetum-" + bucket, "policies": policy}, &made); err != nil {
		return "", "", err
	}
	if made.ID == "" || made.Value == "" {
		return "", "", errors.New("Cloudflare made the token but did not return it")
	}
	sum := sha256.Sum256([]byte(made.Value))
	return made.ID, hex.EncodeToString(sum[:]), nil
}
