package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Evaluate the actual developer-owned source, never a copied SQL approximation.
// Only the two fixed read-query entrypoints below can be selected.
func diagnosticSQL(name string) (string, error) {
	return diagnosticSQLFromSource("/source/internal/controllers", name)
}

func diagnosticSQLFromSource(directory, name string) (string, error) {
	constants := map[string]ast.Expr{}
	functions := map[string]*ast.FuncDecl{}
	for _, file := range []string{"driver_availability.go", "provider_threads.go"} {
		f, e := parser.ParseFile(token.NewFileSet(), filepath.Join(directory, file), nil, 0)
		if e != nil {
			return "", e
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok == token.CONST {
					for _, spec := range d.Specs {
						v := spec.(*ast.ValueSpec)
						for i, id := range v.Names {
							if i < len(v.Values) {
								constants[id.Name] = v.Values[i]
							}
						}
					}
				}
			case *ast.FuncDecl:
				functions[d.Name.Name] = d
			}
		}
	}
	var eval func(ast.Expr, map[string]string) (string, error)
	eval = func(expr ast.Expr, vars map[string]string) (string, error) {
		switch n := expr.(type) {
		case *ast.BasicLit:
			return strconv.Unquote(n.Value)
		case *ast.Ident:
			if v, ok := vars[n.Name]; ok {
				return v, nil
			}
			if v, ok := constants[n.Name]; ok {
				return eval(v, vars)
			}
		case *ast.BinaryExpr:
			if n.Op == token.ADD {
				a, e := eval(n.X, vars)
				if e != nil {
					return "", e
				}
				b, e := eval(n.Y, vars)
				return a + b, e
			}
		case *ast.CallExpr:
			id, ok := n.Fun.(*ast.Ident)
			if ok && id.Name == "businessThreadMembers" && len(n.Args) == 1 {
				arg, e := eval(n.Args[0], vars)
				if e != nil {
					return "", e
				}
				for _, s := range functions[id.Name].Body.List {
					if r, ok := s.(*ast.ReturnStmt); ok {
						return eval(r.Results[0], map[string]string{"root": arg})
					}
				}
			}
		}
		return "", fmt.Errorf("unsupported static expression %T", expr)
	}
	if name != "NearbyDrivers" && name != "businessHistoryPageSQL" {
		return "", fmt.Errorf("query not allowlisted")
	}
	fn := functions[name]
	if fn == nil {
		return "", fmt.Errorf("source entrypoint missing")
	}
	var expr ast.Expr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if name == "businessHistoryPageSQL" {
			if r, ok := node.(*ast.ReturnStmt); ok && expr == nil {
				expr = r.Results[0]
			}
		}
		if c, ok := node.(*ast.CallExpr); ok && len(c.Args) > 1 {
			if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Query" && expr == nil {
				// Fiber c.Query reads a request parameter; only m.db.Query
				// supplies the application SQL under investigation.
				if receiver, ok := s.X.(*ast.SelectorExpr); ok && receiver.Sel.Name == "db" {
					expr = c.Args[1]
				}
			}
		}
		return true
	})
	return eval(expr, nil)
}

func queryDiagnostic(ctx context.Context, db *pgxpool.Pool, out io.Writer) error {
	if e := requireSessionLab(ctx, db); e != nil {
		return e
	}
	// Set up fixed, synthetic live presences. No actual GPS or external endpoint.
	_, e := db.Exec(ctx, `INSERT INTO provider_driver_presence(provider_id,listing_id,latitude,longitude,accuracy_m,updated_at,expires_at)
 SELECT provider,listing,9.081+(slot%7)*0.001,7.451+(slot%5)*0.001,15,clock_timestamp(),clock_timestamp()+interval '30 minutes'
 FROM capacity_lab_actors WHERE slot%10 IN(1,2) ON CONFLICT(provider_id) DO NOTHING`)
	if e != nil {
		return e
	}
	if _, e = db.Exec(ctx, "ANALYZE"); e != nil {
		return e
	}
	var conversation string
	var cursor time.Time
	var cursorID string
	e = db.QueryRow(ctx, `SELECT conversation::text FROM capacity_lab_actors ORDER BY slot LIMIT 1`).Scan(&conversation)
	if e != nil {
		return e
	}
	e = db.QueryRow(ctx, `SELECT created_at,id::text FROM provider_conversation_messages WHERE conversation_id=$1::uuid ORDER BY created_at,id OFFSET 30 LIMIT 1`, conversation).Scan(&cursor, &cursorID)
	if e != nil {
		return e
	}
	near, e := diagnosticSQL("NearbyDrivers")
	if e != nil {
		return e
	}
	history, e := diagnosticSQL("businessHistoryPageSQL")
	if e != nil {
		return e
	}
	type scenario struct {
		name, sql string
		args      []any
	}
	cases := []scenario{}
	for _, kind := range []string{"all", "passenger", "courier"} {
		cases = append(cases, scenario{"nearby-" + kind, near, []any{9.081, 7.451, 20.0 / 111.32, 20.0 / (111.32 * math.Cos(9.081*math.Pi/180)), 20.0, kind}})
	}
	cases = append(cases, scenario{"history-newest", history, []any{conversation, nil, nil, 31}}, scenario{"history-old", history, []any{conversation, cursor, cursorID, 31}})
	// The first identity-fenced lookup comparison showed no gain. Test lazy
	// eligibility after an ordered, cheap distance/classification pass instead.
	// There is NO pre-limit: rejected candidates are skipped until 25 eligible
	// rows are found or every in-radius candidate has been checked.
	marker := "\n SELECT * FROM candidates WHERE"
	end := strings.Index(near, marker)
	if end < 0 {
		return fmt.Errorf("actual nearby query shape changed")
	}
	base := strings.TrimSuffix(strings.TrimPrefix(near[:end], "WITH candidates AS ("), ")")
	base = strings.Replace(base, ",i.paid_active,", ",l.plan_slot,", 1)
	base = strings.Replace(base, " JOIN provider_public_identity i ON i.provider_id=d.provider_id", "", 1)
	base = strings.Replace(base, " AND l.plan_slot<=i.listing_limit AND i.verified AND i.paid_active AND driver_documents_approved(l.provider_id)", "", 1)
	fenced := `SELECT c.id,c.provider_id,c.title,c.description,c.category,c.media_urls,c.city,c.country_code,c.price,c.currency_code,c.pricing_unit,i.paid_active,c.updated_at,c.expires_at,c.transport_kind,c.distance
 FROM (SELECT * FROM (` + base + `) spatial WHERE distance<=$5 AND ($6='all' OR transport_kind=$6) ORDER BY distance,id OFFSET 0) c
 JOIN LATERAL (SELECT i.paid_active FROM provider_public_identity i WHERE i.provider_id=c.provider_id::uuid AND c.plan_slot<=i.listing_limit AND i.verified AND i.paid_active AND driver_documents_approved(i.provider_id) OFFSET 0) i ON true
 ORDER BY c.distance,c.id LIMIT 25`
	matched := []scenario{}
	for _, original := range cases {
		if !strings.HasPrefix(original.name, "nearby-") {
			matched = append(matched, original)
			continue
		}
		read := func(query string) ([][]any, error) {
			rows, err := db.Query(ctx, query, original.args...)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			result := [][]any{}
			for rows.Next() {
				v, err := rows.Values()
				if err != nil {
					return nil, err
				}
				result = append(result, v)
			}
			return result, rows.Err()
		}
		before, err := read(near)
		if err != nil {
			return err
		}
		after, err := read(fenced)
		if err != nil {
			return err
		}
		if len(before) == 0 || !reflect.DeepEqual(before, after) {
			return fmt.Errorf("nearby differential failed for %s", original.name)
		}
		for index, query := range []string{near, fenced, fenced, near} {
			variant := "original"
			if index == 1 || index == 2 {
				variant = "ordered-eligibility"
			}
			matched = append(matched, scenario{fmt.Sprintf("%s-arm%d-%s", original.name, index+1, variant), query, original.args})
		}
	}
	cases = matched
	encoder := json.NewEncoder(out)
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		cfg := db.Config().Copy()
		cfg.MaxConns = 4
		cfg.ConnConfig.RuntimeParams["plan_cache_mode"] = mode
		cfg.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
		pool, e := pgxpool.NewWithConfig(ctx, cfg)
		if e != nil {
			return e
		}
		for _, s := range cases {
			var plan json.RawMessage
			if e = pool.QueryRow(ctx, "EXPLAIN(ANALYZE,BUFFERS,SETTINGS,FORMAT JSON) "+s.sql, s.args...).Scan(&plan); e != nil {
				pool.Close()
				return e
			}
			if e = encoder.Encode(map[string]any{"case": s.name, "mode": mode, "sql_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(s.sql))), "plan": plan}); e != nil {
				pool.Close()
				return e
			}
			for _, parallel := range []int{1, 4} {
				// Bound offered work independently of HTTP login or the K6 generator.
				began := time.Now()
				end := began.Add(3 * time.Second)
				var wg sync.WaitGroup
				var mu sync.Mutex
				latency := []float64{}
				errors := 0
				rowsTotal := 0
				for i := 0; i < parallel; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for time.Now().Before(end) && ctx.Err() == nil {
							start := time.Now()
							rows, err := pool.Query(ctx, s.sql, s.args...)
							count := 0
							if err == nil {
								for rows.Next() {
									count++
								}
								err = rows.Err()
								rows.Close()
							}
							elapsed := time.Since(start).Seconds() * 1000
							mu.Lock()
							if err != nil {
								errors++
							} else {
								latency = append(latency, elapsed)
								rowsTotal += count
							}
							mu.Unlock()
						}
					}()
				}
				wg.Wait()
				seconds := time.Since(began).Seconds()
				sort.Float64s(latency)
				percentile := func(p float64) float64 {
					if len(latency) == 0 {
						return 0
					}
					return latency[int(float64(len(latency)-1)*p)]
				}
				if e = encoder.Encode(map[string]any{"case": s.name, "mode": mode, "concurrency": parallel, "started_at": began.UTC(), "finished_at": time.Now().UTC(), "completed": len(latency), "errors": errors, "rows": rowsTotal, "seconds": seconds, "queries_per_second": float64(len(latency)) / seconds, "p50_ms": percentile(.5), "p95_ms": percentile(.95), "p99_ms": percentile(.99)}); e != nil {
					pool.Close()
					return e
				}
				if errors > 0 {
					pool.Close()
					return fmt.Errorf("bounded query phase had errors")
				}
			}
		}
		pool.Close()
	}
	return nil
}
