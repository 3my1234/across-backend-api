package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOverloadRequiresRetryDirectiveAndJSON(t *testing.T) {
	for _, tc := range []struct {
		status      int
		retry, body string
		want        bool
	}{
		{503, "2", `{"error":"busy"}`, true}, {429, "10", `{"error":"rate limited"}`, true},
		{503, "", `{"error":"busy"}`, false}, {503, "0", `{"error":"busy"}`, false},
		{503, "2", `<html>proxy unavailable</html>`, false}, {500, "2", `{"error":"failure"}`, false},
		{503, "2", `{}`, false}, {503, "-1", `{"error":"busy"}`, false},
	} {
		if got := controlledOverload(tc.status, tc.retry, []byte(tc.body)); got != tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
}

func TestIndependentArrivalsDoNotQueueBehindResponses(t *testing.T) {
	var active, peak atomic.Int32
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	result := make(chan arrivalReport, 1)
	go func() {
		result <- runArrivalStage(context.Background(), arrivalStage{"bounded-test", 10000, 100 * time.Millisecond}, [][]operation{{{Name: "buyer_send", Write: true}}}, "test:", func(op operation, _ int) sample {
			current := active.Add(1)
			select {
			case entered <- struct{}{}:
			default:
			}
			defer active.Add(-1)
			for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
			}
			<-gate
			return sample{Name: op.Name, Valid: true, Write: true, Status: 201, Backend: "api1"}
		})
	}()
	// Hold responses beyond the entire offering window. Arrivals must still
	// finish scheduling/drop at the cap, rather than wait to issue the next one.
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("generator did not start")
	}
	time.Sleep(200 * time.Millisecond)
	close(gate)
	r := <-result
	if peak.Load() > burstConcurrency || r.Counts.Launched > burstConcurrency {
		t.Fatal("unbounded in-flight requests")
	}
	if r.Counts.GeneratorFull == 0 {
		t.Fatal("full generator was not reported")
	}
	if r.Counts.Planned != r.Counts.Launched+r.Counts.GeneratorFull+r.Counts.GeneratorLate {
		t.Fatal("lost arrival accounting")
	}
	if r.Confirmed != r.Counts.Launched {
		t.Fatal("confirmed-write accounting mismatch")
	}
}

func TestBurstWritesHaveUniqueIDsAndPrefixes(t *testing.T) {
	templates := [][]operation{{{Name: "buyer_send", Body: "stale", Write: true}}}
	seen := map[string]bool{}
	var duplicates atomic.Bool
	// Collect bodies after completion in a buffered channel to avoid a test lock
	// becoming the generator's response-speed bottleneck.
	bodies := make(chan string, 20)
	r := runArrivalStage(context.Background(), arrivalStage{"write-test", 20, 200 * time.Millisecond}, templates, "unique:", func(op operation, _ int) sample {
		bodies <- op.Body
		return sample{Name: op.Name, Status: 201, Valid: true, Write: true}
	})
	close(bodies)
	for body := range bodies {
		var data map[string]string
		if json.Unmarshal([]byte(body), &data) != nil || !strings.HasPrefix(data["message"], "unique:") {
			t.Fatal("missing write prefix")
		}
		if seen[data["client_message_id"]] {
			duplicates.Store(true)
		}
		seen[data["client_message_id"]] = true
	}
	if duplicates.Load() || len(seen) != r.Confirmed || r.Confirmed != 4 {
		t.Fatal("write identities reused/lost")
	}
}

func TestRecoveryCannotPassWithErrorsDropsOrLatencyFailure(t *testing.T) {
	base := arrivalReport{Backends: map[string]int{"api1": 1, "api2": 1}, LatencyPassed: true}
	if !healthyArrivalStage(base) {
		t.Fatal("healthy recovery rejected")
	}
	for _, modify := range []func(*arrivalReport){
		func(r *arrivalReport) { r.Errors = 1 }, func(r *arrivalReport) { r.Busy = 1 },
		func(r *arrivalReport) { r.Counts.GeneratorFull = 1 }, func(r *arrivalReport) { r.Counts.GeneratorLate = 1 },
		func(r *arrivalReport) { r.LatencyPassed = false }, func(r *arrivalReport) { r.Backends = map[string]int{"api1": 1} },
	} {
		r := base
		modify(&r)
		if healthyArrivalStage(r) {
			t.Fatal("unhealthy recovery accepted")
		}
	}
}

func TestBurstDoesNotCreateUnboundedRequestsOrChangeReadIdentity(t *testing.T) {
	for _, hot := range []string{"", "room"} {
		ops := burstOperations(actor{HotRoom: hot})
		writes := 0
		for _, op := range ops {
			if op.Write {
				writes++
			}
			if (op.Name == "catalogue" || op.Name == "services") && op.Token != "" {
				t.Fatal("public cache bypassed")
			}
		}
		if writes != 3 || len(ops) > 10 {
			t.Fatalf("unexpected mix size %d writes %d", len(ops), writes)
		}
	}
	stages := burstStages()
	if len(stages) != 3 || stages[0].Rate != stages[2].Rate || stages[1].Rate <= stages[0].Rate {
		t.Fatal("burst/recovery shape changed")
	}
}

func TestBurstCountsProxyFailuresSeparatelyFromControlledOverload(t *testing.T) {
	var sequence atomic.Int32
	r := runArrivalStage(context.Background(), arrivalStage{"classification", 30, 100 * time.Millisecond}, [][]operation{{{Name: "history"}}}, "test:", func(op operation, _ int) sample {
		n := sequence.Add(1)
		return sample{Name: op.Name, Status: 503, Controlled: n == 1, AdmissionRejected: n == 1}
	})
	if r.Busy != 1 || r.AdmissionRejected != 1 || r.Errors != 2 || r.Operations["history"].Busy != 1 || r.Operations["history"].Errors != 2 {
		t.Fatalf("incorrect overload accounting: %+v", r)
	}
}

func TestCancelledScheduleAccountsForUnlaunchedArrivals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := scheduleArrivals(ctx, arrivalStage{"cancelled", 100, time.Second}, func(int) bool { t.Fatal("launched after cancellation"); return true })
	if r.Launched != 0 || r.GeneratorLate != r.Planned {
		t.Fatal("cancelled arrivals disappeared")
	}
}
