package secrets

import (
	"fmt"
	"strings"
)

// RefScheme is the URI scheme of a secret reference.
const RefScheme = "secret"

// Ref points at a stored secret without carrying it.
//
// The stored form is `secret://<kind>/<id>`, e.g. `secret://llm/1f0c…. The kind
// lets a reader tell a model key from another kind of secret at a glance, and the
// id is opaque.
type Ref struct {
	Scheme string
	Kind   string
	ID     string
}

// String renders the reference in its stored form.
func (r Ref) String() string {
	if r.Scheme == "" {
		return ""
	}
	return r.Scheme + "://" + r.Kind + "/" + r.ID
}

// Validate checks the reference shape.
func (r Ref) Validate() error {
	if !strings.EqualFold(r.Scheme, RefScheme) {
		return fmt.Errorf("%w: scheme %q", ErrInvalidRef, r.Scheme)
	}
	if strings.TrimSpace(r.Kind) == "" {
		return fmt.Errorf("%w: empty kind", ErrInvalidRef)
	}
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidRef)
	}
	if strings.ContainsAny(r.Kind+r.ID, "/\\ ") {
		return fmt.Errorf("%w: %q", ErrInvalidRef, r.String())
	}
	return nil
}

// ParseRef reads a stored reference.
func ParseRef(s string) (Ref, error) {
	trimmed := strings.TrimSpace(s)
	idx := strings.Index(trimmed, "://")
	if idx <= 0 {
		return Ref{}, fmt.Errorf("%w: %q", ErrInvalidRef, s)
	}
	rest := trimmed[idx+3:]
	slash := strings.Index(rest, "/")
	if slash <= 0 || slash == len(rest)-1 {
		return Ref{}, fmt.Errorf("%w: %q", ErrInvalidRef, s)
	}
	ref := Ref{Scheme: trimmed[:idx], Kind: rest[:slash], ID: rest[slash+1:]}
	if err := ref.Validate(); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// refStringFor rebuilds a reference when only the parts are known.
func refStringFor(kind, id string) string {
	return Ref{Scheme: RefScheme, Kind: kind, ID: id}.String()
}
