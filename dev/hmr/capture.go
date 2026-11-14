package hmr

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/karl-tye/ktml"
	"github.com/karl-tye/ktml/dev/interp"
	"github.com/karl-tye/ktml/internal/storygen"
)

const (
	// CaptureEnv names the directory `ktml dev --capture-stories` captures
	// stories into; CaptureRawEnv is set to 1 by --capture-raw.
	CaptureEnv    = "KTML_DEV_CAPTURE"
	CaptureRawEnv = "KTML_DEV_CAPTURE_RAW"
)

// Capture statuses.
const (
	CaptureOff       = "off"
	CaptureActive    = "capturing"
	CaptureRefused   = "refused"
	captureQueueSize = 64
)

// CaptureOptions configure story capture.
type CaptureOptions struct {
	// Dir is the capture directory, inside the workspace root; empty turns
	// capture off.
	Dir string
	// Raw keeps raw values instead of redacting them.
	Raw bool
	// LocalData is the application's declaration that it reads only local
	// development data. Capture is refused without it.
	LocalData bool
	// KeyPath is the redaction key file; empty means the user's
	// storygen.CaptureKeyPath.
	KeyPath string
}

// CaptureStatus is the answer of POST /__ktml/dev/capture.
type CaptureStatus struct {
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Dir     string `json:"dir,omitempty"`
	Raw     bool   `json:"raw,omitempty"`
	Stories int64  `json:"stories"`
}

// InstallOption configures Install.
type InstallOption func(*installConfig)

type installConfig struct {
	localData bool
}

// LocalData declares whether the application reads only local development
// data, such as fixtures or a database on this machine. Story capture
// (`ktml dev --capture-stories`) writes the props of rendered pages into
// story files, so it runs only in an application that declares local data:
//
//	hmr.Install(handler, hmr.LocalData(*local))
func LocalData(local bool) InstallOption {
	return func(config *installConfig) { config.localData = local }
}

// capture writes stories from the roots the registry tracks. Props are read
// on the request's goroutine, since the application may change them later,
// and written by one worker.
type capture struct {
	status   CaptureStatus
	capturer *storygen.Capturer
	queue    chan interp.Captured
	written  atomic.Int64
	logf     func(format string, args ...any)
}

func newCapture(root string, options CaptureOptions, logf func(format string, args ...any)) *capture {
	c := &capture{status: CaptureStatus{Status: CaptureOff}, logf: logf}
	if options.Dir == "" {
		return c
	}
	refuse := func(reason string) *capture {
		c.status = CaptureStatus{Status: CaptureRefused, Reason: reason, Dir: options.Dir}
		logf("ktml capture: refused: %s", reason)
		return c
	}
	if !options.LocalData {
		return refuse("the application did not declare local data; pass hmr.LocalData(true) to hmr.Install only when it reads local development data")
	}
	var redactor *storygen.Redactor
	if !options.Raw {
		path := options.KeyPath
		if path == "" {
			var err error
			if path, err = storygen.CaptureKeyPath(); err != nil {
				return refuse(fmt.Sprintf("no redaction key: %v", err))
			}
		}
		key, err := storygen.LoadCaptureKey(path)
		if err != nil {
			return refuse(fmt.Sprintf("no redaction key: %v", err))
		}
		redactor = storygen.NewRedactor(key)
	}
	capturer, err := storygen.NewCapturer(root, options.Dir, redactor)
	if err != nil {
		return refuse(err.Error())
	}
	dir := options.Dir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	c.status = CaptureStatus{Status: CaptureActive, Dir: dir, Raw: options.Raw}
	c.capturer = capturer
	c.queue = make(chan interp.Captured, captureQueueSize)
	mode := "redacted"
	if options.Raw {
		mode = "RAW VALUES (--capture-raw): keep these stories local"
	}
	logf("ktml capture: capturing stories into %s, %s", dir, mode)
	go c.run()
	return c
}

func (c *capture) run() {
	for captured := range c.queue {
		result, err := c.capturer.Add(captured)
		switch {
		case err != nil:
			c.logf("ktml capture: %s: %v", captured.Component.Name, err)
		case result.Story != "":
			c.written.Add(1)
			c.logf("ktml capture: wrote %s to %s", result.Story, result.Path)
		}
	}
}

// add reads a tracked root's props and queues them. A full queue drops the
// capture: the next render of the same shape is captured instead.
func (c *capture) add(program *interp.Program, props ktml.Component) {
	if c == nil || c.capturer == nil || program == nil {
		return
	}
	if _, story := props.(interp.StoryProps); story {
		return
	}
	captured, err := program.Capture(props)
	if err != nil {
		return
	}
	select {
	case c.queue <- captured:
	default:
	}
}

func (c *capture) current() CaptureStatus {
	if c == nil {
		return CaptureStatus{Status: CaptureOff}
	}
	status := c.status
	status.Stories = c.written.Load()
	return status
}

// captureOptionsFromEnv reads the capture request `ktml dev` passes to the
// application.
func captureOptionsFromEnv(config installConfig) CaptureOptions {
	return CaptureOptions{Dir: os.Getenv(CaptureEnv), Raw: os.Getenv(CaptureRawEnv) == "1", LocalData: config.localData}
}
