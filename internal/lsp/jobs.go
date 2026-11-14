package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/karl-tye/ktml/internal/wasm"
)

// Background jobs let a plugin move long work, such as scanning Go sources,
// off its request path. After loading and after each client notification the
// host asks the plugin for a job ($/ktml/pendingJob). A job is any JSON
// object; the host runs it on a separate worker instance of the same plugin
// ($/ktml/runJob), so requests to the serving instance never wait for it, and
// delivers the result back ($/ktml/jobResult), whose answer is the
// notifications to publish. Each plugin runs at most one job at a time.
const (
	pendingJobMethod = "$/ktml/pendingJob"
	runJobMethod     = "$/ktml/runJob"
	jobResultMethod  = "$/ktml/jobResult"
)

type backgroundJobs struct {
	ctx context.Context
	// mu guards workers and closed against job goroutines.
	mu      sync.Mutex
	closed  bool
	plugins []*wasm.Plugin
	workers []*wasm.Plugin
	running []bool
	done    chan jobDone
	stopped chan struct{}
}

type jobDone struct {
	plugin int
	job    []byte
	result []byte
	err    error
}

func newBackgroundJobs(ctx context.Context, plugins []*wasm.Plugin) *backgroundJobs {
	return &backgroundJobs{
		ctx:     ctx,
		plugins: plugins,
		workers: make([]*wasm.Plugin, len(plugins)),
		running: make([]bool, len(plugins)),
		done:    make(chan jobDone),
		stopped: make(chan struct{}),
	}
}

// pollAll starts the pending job of every idle plugin.
func (jobs *backgroundJobs) pollAll() {
	for index := range jobs.plugins {
		jobs.poll(index)
	}
}

func (jobs *backgroundJobs) poll(index int) {
	if jobs.running[index] || jobs.plugins[index] == nil {
		return
	}
	job, err := jobs.plugins[index].ExecuteLSP(jobs.ctx, pendingJobMethod, []byte(`{}`))
	if err != nil || !isJob(job) {
		return
	}
	jobs.running[index] = true
	go func() {
		worker, err := jobs.worker(index)
		var result []byte
		if err == nil {
			result, err = worker.ExecuteLSP(jobs.ctx, runJobMethod, job)
		}
		select {
		case jobs.done <- jobDone{plugin: index, job: job, result: result, err: err}:
		case <-jobs.stopped:
		case <-jobs.ctx.Done():
		}
	}()
}

// worker returns the plugin's worker instance, starting it on first use.
func (jobs *backgroundJobs) worker(index int) (*wasm.Plugin, error) {
	jobs.mu.Lock()
	worker, closed := jobs.workers[index], jobs.closed
	jobs.mu.Unlock()
	if closed {
		return nil, context.Canceled
	}
	if worker != nil {
		return worker, nil
	}
	worker, err := jobs.plugins[index].NewWorker(jobs.ctx)
	if err != nil {
		return nil, err
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.closed {
		_ = worker.Close(jobs.ctx)
		return nil, context.Canceled
	}
	jobs.workers[index] = worker
	return worker, nil
}

// deliver hands a finished job to its plugin, starts the plugin's next job,
// and returns the plugin's notifications indexed by plugin.
func (jobs *backgroundJobs) deliver(done jobDone) [][]byte {
	jobs.running[done.plugin] = false
	delivery := map[string]any{"job": json.RawMessage(done.job)}
	if done.err != nil {
		delivery["error"] = done.err.Error()
	} else if json.Valid(done.result) && len(bytes.TrimSpace(done.result)) > 0 {
		delivery["result"] = json.RawMessage(done.result)
	}
	payload, _ := json.Marshal(delivery)
	responses := make([][]byte, len(jobs.plugins))
	response, err := jobs.plugins[done.plugin].ExecuteLSP(jobs.ctx, jobResultMethod, payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error delivering a background job to a plugin: %v\n", err)
	} else {
		responses[done.plugin] = response
	}
	jobs.poll(done.plugin)
	return responses
}

func (jobs *backgroundJobs) close() {
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	jobs.closed = true
	close(jobs.stopped)
	for _, worker := range jobs.workers {
		if worker != nil {
			_ = worker.Close(jobs.ctx)
		}
	}
}

// isJob accepts a JSON object that is not a JSON-RPC message.
func isJob(response []byte) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(response, &object) != nil || object == nil {
		return false
	}
	_, isRPC := object["jsonrpc"]
	return !isRPC
}
