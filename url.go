package ktml

// URL is an opaque URL value constructed only in Go. Generated renderers
// escape it as an attribute value and apply the URL scheme policy, so an
// unsafe scheme renders an inert placeholder instead of the value.
type URL struct{ raw string }

// NewURL wraps a URL string for rendering under the URL policy.
func NewURL(raw string) URL { return URL{raw: raw} }

// String returns the wrapped URL string.
func (u URL) String() string { return u.raw }

// TrustedURL is an opaque URL that bypasses the URL scheme policy. TrustURL is
// an explicit audit boundary: call it only for URLs whose origin Go has
// validated.
type TrustedURL struct{ raw string }

// TrustURL wraps a validated URL string that renders without the URL policy.
func TrustURL(raw string) TrustedURL { return TrustedURL{raw: raw} }

// String returns the wrapped URL string.
func (u TrustedURL) String() string { return u.raw }
