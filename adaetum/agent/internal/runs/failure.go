package runs

import (
	"context"
	"errors"
	"fmt"
)

// Failure is a classified error. Steps return these (through the helpers below) so
// the engine knows whether to retry, stop, or ask a person. An error that is not a
// Failure is treated as broken, never as retryable: retrying something the engine
// does not understand is how work gets repeated by accident.
type Failure struct {
	Kind   FailureKind
	Code   string
	Detail string
	Err    error
}

func (f *Failure) Error() string {
	if f.Err != nil {
		return fmt.Sprintf("%s (%s): %v", f.Kind, f.Code, f.Err)
	}
	return fmt.Sprintf("%s (%s): %s", f.Kind, f.Code, f.Detail)
}

func (f *Failure) Unwrap() error { return f.Err }

// Transient marks an error as worth retrying: a network blip, an API that is not
// up yet. The engine retries with backoff and starts again from Check.
func Transient(code string, err error) error {
	return &Failure{Kind: FailTransient, Code: normalizeCode(code), Detail: trimDetail(errText(err)), Err: err}
}

// BrokenBecause marks a failure that retrying will not fix.
func BrokenBecause(code, detail string) error {
	return &Failure{Kind: FailBroken, Code: normalizeCode(code), Detail: trimDetail(detail)}
}

// NeedsHuman stops the run on purpose so a person decides.
func NeedsHuman(code, detail string) error {
	return &Failure{Kind: FailNeedsHuman, Code: normalizeCode(code), Detail: trimDetail(detail)}
}

// asFailure turns any error into a Failure.
func asFailure(err error) *Failure {
	if err == nil {
		return nil
	}
	var f *Failure
	if errors.As(err, &f) {
		return f
	}
	if errors.Is(err, context.Canceled) {
		return &Failure{Kind: FailCancelled, Code: CodeOther, Detail: "cancelled", Err: err}
	}
	return &Failure{Kind: FailBroken, Code: CodeOther, Detail: trimDetail(err.Error()), Err: err}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
