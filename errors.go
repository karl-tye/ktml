package ktml

import "fmt"

// ErrorKind classifies a RenderError. Renderer errors are limited to these
// runtime failures; user-displayable failures are Presentation State Variants.
type ErrorKind uint8

const (
	// ErrWrite reports that the underlying io.Writer failed.
	ErrWrite ErrorKind = iota + 1
	// ErrCanceled reports that the render context was canceled.
	ErrCanceled
	// ErrInvalidState reports a nil or unknown Presentation State selector.
	ErrInvalidState
	// ErrMissingContent reports a required content slot that is nil.
	ErrMissingContent
	// ErrDuplicateKey reports a duplicate sibling key in Keyed Repetition.
	ErrDuplicateKey
)

func (kind ErrorKind) String() string {
	switch kind {
	case ErrWrite:
		return "write"
	case ErrCanceled:
		return "canceled"
	case ErrInvalidState:
		return "invalid state"
	case ErrMissingContent:
		return "missing content"
	case ErrDuplicateKey:
		return "duplicate key"
	default:
		return fmt.Sprintf("ErrorKind(%d)", uint8(kind))
	}
}

// RenderError is the typed error generated renderers return. Component is the
// canonical component ID "<package ID>.<Name>"; Input names the KTML input or
// repetition source when one applies.
type RenderError struct {
	Kind      ErrorKind
	Component string
	Input     string
	Key       string
	Partial   bool
	Err       error
}

func (e *RenderError) Error() string {
	message := "ktml: " + e.Kind.String() + " in " + e.Component
	if e.Input != "" {
		message += " input " + e.Input
	}
	if e.Key != "" {
		message += " key " + e.Key
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

// Unwrap returns the underlying writer or context error, if any.
func (e *RenderError) Unwrap() error { return e.Err }
