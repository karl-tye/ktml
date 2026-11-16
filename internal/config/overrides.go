package config

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/karl-tye/ktml/internal/configdiag"
)

// TargetPortPlaceholder in dev.command, dev.build, or a dev.env value is
// replaced by the port of dev.proxy_target, so the application listens where
// the proxy forwards even when --proxy-target moves it.
const TargetPortPlaceholder = "{target_port}"

// DevOverrides are ktml dev's command-line overrides of dev keys, applied
// over the chosen profile. A zero field leaves its key unchanged.
type DevOverrides struct {
	// Port overrides dev.port (--port).
	Port int
	// ProxyTarget overrides dev.proxy_target (--proxy-target).
	ProxyTarget string
	// ExplorerAddr overrides dev.explorer.addr (--explorer-addr), serving
	// the Explorer there even without dev.explorer.
	ExplorerAddr string
}

// overrideFlags names the flag that sets each dev key.
var overrideFlags = map[string]string{"port": "--port", "proxy_target": "--proxy-target", "explorer": "--explorer-addr"}

// WithOverrides returns a copy of dev with the overrides applied, validated
// as the configuration is, and with TargetPortPlaceholder substituted. An
// error caused by an override names its flag.
func (d *Dev) WithOverrides(o DevOverrides) (*Dev, error) {
	resolved := *d
	resolved.Command = slices.Clone(d.Command)
	resolved.Build = slices.Clone(d.Build)
	resolved.Env = maps.Clone(d.Env)
	if d.Explorer != nil {
		explorer := *d.Explorer
		resolved.Explorer = &explorer
	}
	set := map[string]bool{}
	if o.Port != 0 {
		resolved.Port = o.Port
		set["port"] = true
	}
	if o.ProxyTarget != "" {
		resolved.ProxyTarget = o.ProxyTarget
		set["proxy_target"] = true
	}
	if o.ExplorerAddr != "" {
		resolved.Explorer = &DevExplorer{Addr: o.ExplorerAddr}
		set["explorer"] = true
	}
	if err := validateDev(&resolved); err != nil {
		var pathError *configdiag.PathError
		if errors.As(err, &pathError) && len(pathError.Path) > 1 && set[pathError.Path[1]] {
			return nil, fmt.Errorf("%s: %s", overrideFlags[pathError.Path[1]], pathError.Message)
		}
		return nil, err
	}
	port := resolved.TargetPort()
	for index, arg := range resolved.Command {
		resolved.Command[index] = strings.ReplaceAll(arg, TargetPortPlaceholder, port)
	}
	for index, arg := range resolved.Build {
		resolved.Build[index] = strings.ReplaceAll(arg, TargetPortPlaceholder, port)
	}
	for key, value := range resolved.Env {
		resolved.Env[key] = strings.ReplaceAll(value, TargetPortPlaceholder, port)
	}
	return &resolved, nil
}

// TargetPort is the port of the validated ProxyTarget, or its scheme's
// default port.
func (d *Dev) TargetPort() string {
	target, err := url.Parse(d.ProxyTarget)
	if err != nil {
		return ""
	}
	if port := target.Port(); port != "" {
		return port
	}
	if target.Scheme == "https" {
		return "443"
	}
	return "80"
}
