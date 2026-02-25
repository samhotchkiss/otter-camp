package gateway

import (
	"testing"

	"github.com/samhotchkiss/otter-camp/internal/jobqueue"
)

type testJobRegistrar struct {
	handlers map[string]jobqueue.JobHandler
}

func (r *testJobRegistrar) Register(jobType string, handler jobqueue.JobHandler) {
	if r.handlers == nil {
		r.handlers = make(map[string]jobqueue.JobHandler)
	}
	r.handlers[jobType] = handler
}

func TestRollupWorkerRegisterJobsRegistersRollupUpdateHandler(t *testing.T) {
	registrar := &testJobRegistrar{}
	worker := NewRollupWorker(nil, nil, nil)

	worker.RegisterJobs(registrar)

	handler, ok := registrar.handlers[rollupUpdateJobType]
	if !ok {
		t.Fatalf("handler for %q was not registered", rollupUpdateJobType)
	}
	if handler == nil {
		t.Fatalf("handler for %q is nil", rollupUpdateJobType)
	}
}
