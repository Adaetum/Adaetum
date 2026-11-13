package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/Adaetum/Adaetum/agent/internal/consolebanner"
)

// startConsoleBanner keeps the console login banner (where this host's agent can be
// reached) current, and returns the function that stops it. It does nothing off Linux or
// as a normal user, which cannot write the banner or touch the console anyway. The caller
// only starts it for an agent that listens beyond this machine, because the banner would
// otherwise advertise an address nothing answers on.
func startConsoleBanner(listen string) func() {
	_, port, err := net.SplitHostPort(listen)
	if runtime.GOOS != "linux" || os.Geteuid() != 0 || err != nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	keeper := &consolebanner.Keeper{
		Machine: consolebanner.NewHost(),
		Port:    port,
		Log:     func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		keeper.Run(ctx)
	}()
	return func() {
		cancel()
		<-finished
	}
}
