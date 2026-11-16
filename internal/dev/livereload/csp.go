package livereload

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
)

// cspHeaders are the policy headers whose script rules the injected client
// must satisfy. Report-only policies are amended too, so the client does not
// fill the console and the report endpoint with violations.
var cspHeaders = []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"}

// newNonce returns a fresh CSP nonce for one response.
func newNonce() string {
	var random [18]byte
	_, _ = rand.Read(random[:])
	return base64.StdEncoding.EncodeToString(random[:])
}

// allowNonce adds 'nonce-<nonce>' to every policy in the response's CSP
// headers that restricts script elements, and reports whether any did, in
// which case the injected scripts must carry the nonce.
func allowNonce(header http.Header, nonce string) bool {
	amended := false
	for _, name := range cspHeaders {
		values := header.Values(name)
		if len(values) == 0 {
			continue
		}
		changed := make([]string, len(values))
		for index, value := range values {
			var ok bool
			changed[index], ok = amendPolicies(value, nonce)
			amended = amended || ok
		}
		header.Del(name)
		for _, value := range changed {
			header.Add(name, value)
		}
	}
	return amended
}

// amendPolicies amends each comma-separated policy of one header value; every
// policy is enforced on its own, so each must allow the client.
func amendPolicies(value, nonce string) (string, bool) {
	policies := strings.Split(value, ",")
	amended := false
	for index, policy := range policies {
		var ok bool
		policies[index], ok = amendPolicy(policy, nonce)
		amended = amended || ok
	}
	return strings.Join(policies, ", "), amended
}

// amendPolicy adds the nonce to the directive that governs script elements:
// script-src-elem, else script-src, else default-src. Other directives are
// kept as they are. A policy that restricts no scripts, or whose directive
// already allows inline scripts ('unsafe-inline' without a nonce, hash, or
// 'strict-dynamic', which would switch it off), is returned unchanged.
func amendPolicy(policy, nonce string) (string, bool) {
	var directives []string
	for _, directive := range strings.Split(policy, ";") {
		if directive = strings.TrimSpace(directive); directive != "" {
			directives = append(directives, directive)
		}
	}
	target := -1
	for _, name := range []string{"script-src-elem", "script-src", "default-src"} {
		for index, directive := range directives {
			if strings.EqualFold(strings.Fields(directive)[0], name) {
				target = index
				break
			}
		}
		if target >= 0 {
			break
		}
	}
	if target < 0 {
		return strings.TrimSpace(policy), false
	}
	fields := strings.Fields(directives[target])
	sources := fields[1:]
	unsafeInline, disablesInline := false, false
	kept := []string{fields[0]}
	for _, source := range sources {
		lower := strings.ToLower(source)
		switch {
		case lower == "'none'":
			// 'none' with any other source is ignored; drop it.
			continue
		case lower == "'unsafe-inline'":
			unsafeInline = true
		case lower == "'strict-dynamic'" || strings.HasPrefix(lower, "'nonce-") || hashSource.MatchString(lower):
			disablesInline = true
		}
		kept = append(kept, source)
	}
	if unsafeInline && !disablesInline {
		return strings.Join(directives, "; "), false
	}
	directives[target] = strings.Join(append(kept, "'nonce-"+nonce+"'"), " ")
	return strings.Join(directives, "; "), true
}

var hashSource = regexp.MustCompile(`^'sha(256|384|512)-`)

// metaCSP finds a policy set by a <meta http-equiv> tag, which the proxy
// cannot safely amend.
var metaCSP = regexp.MustCompile(`(?i)<meta\b[^>]*http-equiv\s*=\s*["']?content-security-policy\b`)

// withNonce puts the nonce on every injected script tag.
func withNonce(injected, nonce string) string {
	return strings.ReplaceAll(injected, "<script data-ktml-client>", `<script nonce="`+nonce+`" data-ktml-client>`)
}
