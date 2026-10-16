package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Fixed, short, independently paced stages. No user-supplied rate, origin,
// concurrency, credentials or production target is accepted.
type arrivalStage struct {
	Name     string
	Rate     int
	Duration time.Duration
}

func burstStages() []arrivalStage {
	return []arrivalStage{{"warmup", 60, 20 * time.Second}, {"spike", 1500, 20 * time.Second}, {"recovery", 60, 30 * time.Second}}
}

const burstConcurrency = 384 // Above the two APIs' 128 admission slots; below proxy maxconn 512.

type arrivalCounts struct {
	Planned       int     `json:"planned_arrivals"`
	Launched      int     `json:"launched_requests"`
	GeneratorFull int     `json:"generator_concurrency_drops"`
	GeneratorLate int     `json:"generator_scheduler_drops"`
	MaxLagMS      float64 `json:"maximum_scheduler_lag_ms"`
}

// Scheduling does not wait for responses or queue requests behind busy workers.
// Late arrivals are explicitly counted rather than silently lowering demand or
// sending an unbounded catch-up burst. launch must be nonblocking.
func scheduleArrivals(ctx context.Context, stage arrivalStage, launch func(int) bool) arrivalCounts {
	counts := arrivalCounts{Planned: int(stage.Duration * time.Duration(stage.Rate) / time.Second)}
	start := time.Now()
	for n := 0; n < counts.Planned; n++ {
		due := start.Add(time.Duration(n) * time.Second / time.Duration(stage.Rate))
		if wait := time.Until(due); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				counts.GeneratorLate += counts.Planned - n
				return counts
			case <-timer.C:
			}
		}
		if ctx.Err() != nil {
			counts.GeneratorLate += counts.Planned - n
			return counts
		}
		lag := time.Since(due)
		if ms := float64(lag.Microseconds()) / 1000; ms > counts.MaxLagMS {
			counts.MaxLagMS = ms
		}
		if lag > 50*time.Millisecond {
			counts.GeneratorLate++
			continue
		}
		if launch(n) {
			counts.Launched++
		} else {
			counts.GeneratorFull++
		}
	}
	return counts
}

func controlledOverload(status int, retry string, raw []byte) bool {
	if status != 429 && status != 503 {
		return false
	}
	seconds, err := strconv.Atoi(retry)
	if err != nil || seconds <= 0 || seconds > 3600 {
		return false
	}
	var body struct {
		Error string `json:"error"`
	}
	return json.Unmarshal(raw, &body) == nil && body.Error != ""
}

// Tokens and read templates are prepared before timing; each attempted write
// receives a fresh id/body. No automatic retry of rejected or uncertain writes.
func burstOperations(a actor) []operation {
	ops := []operation{}
	for n := 0; n < 3; n++ {
		for _, op := range operations(a, n, "unused:") {
			if n == 0 && !op.Write {
				ops = append(ops, op)
				continue
			}
			if op.Name == "buyer_send" || op.Name == "provider_send" ||
				(a.HotRoom != "" && n == 0 && op.Name == "busy_room_send") ||
				(a.HotRoom == "" && op.Name == "bargaining_send") {
				ops = append(ops, op)
			}
		}
	}
	return ops
}

type arrivalReport struct {
	Stage                                      arrivalStage
	Counts                                     arrivalCounts
	Operations                                 map[string]*metric
	Backends                                   map[string]int
	Errors, Busy, Confirmed, AdmissionRejected int
	LatencyPassed                              bool
	Elapsed                                    float64
}

func runArrivalStage(ctx context.Context, stage arrivalStage, templates [][]operation, prefix string, send func(operation, int) sample) arrivalReport {
	start := time.Now()
	samples := make(chan sample, burstConcurrency)
	finished := make(chan arrivalCounts, 1)
	slots := make(chan struct{}, burstConcurrency)
	go func() {
		var wg sync.WaitGroup
		counts := scheduleArrivals(ctx, stage, func(sequence int) bool {
			select {
			case slots <- struct{}{}:
			default:
				return false
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				actorIndex := (sequence / len(templates[0])) % len(templates)
				op := templates[actorIndex][sequence%len(templates[actorIndex])]
				if op.Write {
					body, _ := json.Marshal(map[string]string{"message": prefix + uuid.NewString(), "client_message_id": uuid.NewString()})
					op.Body = string(body)
				}
				samples <- send(op, actorIndex)
			}()
			return true
		})
		wg.Wait()
		finished <- counts
		close(samples)
	}()
	r := arrivalReport{Stage: stage, Operations: map[string]*metric{}, Backends: map[string]int{}, LatencyPassed: true}
	for s := range samples {
		m := r.Operations[s.Name]
		if m == nil {
			m = &metric{}
			r.Operations[s.Name] = m
		}
		m.add(s)
		if s.Backend != "" {
			r.Backends[s.Backend]++
		}
		if s.Valid {
			if s.Write {
				r.Confirmed++
			}
			continue
		}
		if s.Controlled {
			r.Busy++
			if s.AdmissionRejected {
				r.AdmissionRejected++
			}
		} else {
			r.Errors++
			if s.Status == 429 || s.Status == 503 {
				m.Busy--
				m.Errors++
			}
		}
	}
	r.Counts = <-finished
	r.Elapsed = time.Since(start).Seconds()
	for _, m := range r.Operations {
		m.finish()
		if m.Success == 0 || m.P95MS > 500 {
			r.LatencyPassed = false
		}
	}
	return r
}

func healthyArrivalStage(r arrivalReport) bool {
	return r.Errors == 0 && r.Busy == 0 && r.Counts.GeneratorFull == 0 && r.Counts.GeneratorLate == 0 && r.LatencyPassed && len(r.Backends) == 2
}

func runBurst(ctx context.Context, db *pgxpool.Pool, actors []actor, prefix, scenario, cpus string, c *http.Client) error {
	templates := make([][]operation, len(actors))
	for i, a := range actors {
		templates[i] = burstOperations(a)
	}
	confirmed := 0
	var failure error
	saturated := false
	for _, stage := range burstStages() {
		if stage.Name == "recovery" {
			// Let in-flight requests drain before measuring normal service again.
			timer := time.NewTimer(10 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		r := runArrivalStage(ctx, stage, templates, prefix, func(op operation, i int) sample { return request(c, op, i) })
		confirmed += r.Confirmed
		must(json.NewEncoder(os.Stdout).Encode(map[string]any{
			"profile": "burst", "scenario": scenario, "database_cpus": cpus, "stage": stage.Name,
			"target_requests_per_second": stage.Rate, "offering_seconds": stage.Duration.Seconds(), "elapsed_seconds_including_drain": r.Elapsed,
			"arrival_counts": r.Counts, "maximum_in_flight": burstConcurrency, "operations": r.Operations, "backends": r.Backends,
			"unexpected_errors": r.Errors, "controlled_overload": r.Busy, "latency_target_ms": 500, "latency_target_passed": r.LatencyPassed,
			"admission_rejections":  r.AdmissionRejected,
			"normal_service_passed": healthyArrivalStage(r), "scope": "isolated Docker host; not production user capacity or autoscaling proof",
		}))
		if stage.Name == "spike" {
			saturated = r.AdmissionRejected > 0
			if r.Errors > 0 {
				failure = fmt.Errorf("spike had %d unexpected failures", r.Errors)
			}
			if !saturated {
				failure = fmt.Errorf("admission overload not observed; saturation protection is unproven")
			}
		} else if !healthyArrivalStage(r) {
			failure = fmt.Errorf("%s failed normal-service criteria", stage.Name)
		}
		if stage.Name == "warmup" && failure != nil {
			break
		}
		if ctx.Err() != nil {
			failure = ctx.Err()
			break
		}
	}
	if err := reconcileWrites(ctx, db, prefix, confirmed); err != nil {
		return err
	}
	return failure
}
