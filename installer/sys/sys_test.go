package sys

import (
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestRunStreamHandsLinesOverWhileTheProgramRunsAndKeepsTheOrder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var mu sync.Mutex
	var lines []string
	res, err := Exec{}.RunStream(context.Background(), "", nil, "", "sh",
		[]string{"-c", "echo first; echo second >&2; echo third"}, func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() })
	if err != nil || !res.OK() {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Join(lines, ",") != "first,second,third" {
		t.Fatalf("lines = %v", lines)
	}
	if !strings.Contains(res.Stdout, "second") {
		t.Fatalf("the result lost stderr: %q", res.Stdout)
	}
}

func TestRunStreamReportsAnExitCodeAndPassesStdinAndEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var got string
	res, err := Exec{}.RunStream(context.Background(), "", []string{"GREETING=hello"}, "from-stdin\n", "sh",
		[]string{"-c", "read x; echo \"$GREETING $x\"; exit 3"}, func(l string) { got = l })
	if err != nil || res.ExitCode != 3 || got != "hello from-stdin" {
		t.Fatalf("res=%+v err=%v got=%q", res, err, got)
	}
}
