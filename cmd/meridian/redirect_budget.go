package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

const dynamicRedirectChainTimeout = 60 * time.Second

var errDynamicRedirectTimeout = errors.New("dynamic redirect chain timed out")

// The timer covers redirect discovery only. A context.WithTimeout would also
// cancel the final response body, cutting off otherwise healthy video streams.
type dynamicRedirectBudget struct {
	parent   context.Context
	ctx      context.Context
	cancel   context.CancelCauseFunc
	timer    *time.Timer
	mu       sync.Mutex
	finished bool
}

func startDynamicRedirectBudget(req *http.Request, timeout time.Duration) (*http.Request, *dynamicRedirectBudget) {
	ctx, cancel := context.WithCancelCause(req.Context())
	budget := &dynamicRedirectBudget{parent: req.Context(), ctx: ctx, cancel: cancel}
	budget.timer = time.AfterFunc(timeout, func() {
		budget.mu.Lock()
		defer budget.mu.Unlock()
		if !budget.finished {
			cancel(errDynamicRedirectTimeout)
		}
	})
	return req.Clone(ctx), budget
}

// Keep transport metadata (leases, learned paths, etc.) while returning body
// parsing and streaming to the caller's original cancellation/deadline scope.
type completedRedirectContext struct {
	context.Context
	values context.Context
}

func (c completedRedirectContext) Value(key any) any {
	if value := c.values.Value(key); value != nil {
		return value
	}
	return c.Context.Value(key)
}

func (b *dynamicRedirectBudget) finish(resp *http.Response, err error) (*http.Response, error) {
	b.mu.Lock()
	b.finished = true
	b.timer.Stop()
	timedOut := errors.Is(context.Cause(b.ctx), errDynamicRedirectTimeout)
	b.mu.Unlock()
	if timedOut {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		b.cancel(errDynamicRedirectTimeout)
		return nil, errDynamicRedirectTimeout
	}
	if err != nil || resp == nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		b.cancel(nil)
		return nil, err
	}
	if resp.Request != nil {
		resp.Request = resp.Request.WithContext(completedRedirectContext{Context: b.parent, values: context.WithoutCancel(resp.Request.Context())})
	}
	if resp.Body == nil || resp.Body == http.NoBody {
		b.cancel(nil)
	} else {
		resp.Body = &dynamicResponseBody{ReadCloser: resp.Body, release: func() { b.cancel(nil) }}
	}
	return resp, nil
}
