package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForFileFindsCredentialsThatAppearAfterTheAgentStarted(t *testing.T) {
	dir := t.TempDir()
	missing, later := filepath.Join(dir, "none"), filepath.Join(dir, "rke2.yaml")
	go func() {
		time.Sleep(60 * time.Millisecond) // RKE2 is installed a while after the agent starts
		os.WriteFile(later, []byte("kubeconfig"), 0o600)
	}()
	got, ok := waitForFile(context.Background(), []string{missing, later}, 10*time.Millisecond)
	if !ok || got != later {
		t.Fatalf("got %q ok=%v", got, ok)
	}
}

func TestWaitForFileGivesUpWhenTheAgentStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	go func() {
		_, ok := waitForFile(ctx, []string{filepath.Join(t.TempDir(), "never")}, 5*time.Millisecond)
		done <- ok
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("reported a file that never existed")
		}
	case <-time.After(time.Second):
		t.Fatal("kept waiting after the agent stopped")
	}
}
