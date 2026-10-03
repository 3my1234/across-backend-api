// settlements reports verified charges and seller payouts without exposing
// credentials or bank details. Writes require the explicit -reconcile flag.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"across/backend/internal/config"
	"across/backend/internal/controllers"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	from := flag.String("from", time.Now().UTC().AddDate(0, 0, -7).Format("2006-01-02"), "first payment date (UTC, YYYY-MM-DD)")
	to := flag.String("to", time.Now().UTC().Format("2006-01-02"), "last payment date (UTC, YYYY-MM-DD)")
	reconcile := flag.Bool("reconcile", false, "import due seller settlement jobs before reporting (writes payment/ledger state)")
	schema := flag.Bool("schema", false, "report current schema table names only")
	flag.Parse()
	start, err := time.Parse("2006-01-02", *from)
	if err != nil {
		fail("invalid -from date")
	}
	end, err := time.Parse("2006-01-02", *to)
	if err != nil || end.Before(start) {
		fail("invalid -to date")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cfg := config.Load()
	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fail("could not configure database connection")
	}
	defer db.Close()
	if err = db.Ping(ctx); err != nil {
		fail("database is unavailable; check the configured connection")
	}
	if *schema {
		rows, err := db.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`)
		if err != nil {
			fail("schema report unavailable")
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if rows.Scan(&name) != nil {
				fail("schema report failed")
			}
			fmt.Println(name)
		}
		return
	}
	var latest string
	err = db.QueryRow(ctx, `SELECT name FROM schema_migrations WHERE name<>'schema.sql' ORDER BY name DESC LIMIT 1`).Scan(&latest)
	if err != nil {
		fail("schema migration history is unavailable")
	}
	fmt.Printf("Latest recorded migration: %s\n", latest)
	if *reconcile {
		count, err := controllers.ReconcileFlutterwaveSettlements(ctx, db, cfg, &http.Client{Timeout: 20 * time.Second})
		fmt.Printf("Imported settlement transaction records: %d\n", count)
		if err != nil {
			fail("settlement reconciliation did not finish; inspect seller_settlement_jobs.last_error")
		}
	}
	rows, err := db.Query(ctx, `SELECT p.order_id::text,p.provider_reference,p.provider_transaction_id,
  p.amount,p.currency_code,p.payment_status,p.paid_at,p.settlement_status,
  p.provider_settlement_id,p.provider_settlement_amount,
	  ml.expected_net_amount,ml.settlement_amount,ml.gateway_deductions,ml.settlement_status AS ledger_settlement_status,
  ml.settlement_destination,ml.settlement_checked_at
  FROM payments p LEFT JOIN merchant_ledger ml ON ml.order_id=p.order_id AND ml.event_key='order-paid:'||p.order_id::text
  WHERE p.provider='flutterwave' AND p.purpose='order' AND p.paid_at>=$1 AND p.paid_at<$2
  ORDER BY p.paid_at,p.id LIMIT 101`, start, end.AddDate(0, 0, 1))
	if err != nil {
		fail("settlement report unavailable; apply migrations through 049 first")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			fail("could not read settlement report")
		}
		if count == 100 {
			fmt.Println("Report capped at 100 payments; narrow the date range.")
			break
		}
		row := map[string]any{}
		for i, field := range rows.FieldDescriptions() {
			row[string(field.Name)] = values[i]
		}
		// Duplicate field names from payment/ledger are intentionally avoided by
		// naming the last ledger state separately below in the SQL query.
		encoded, err := json.Marshal(row)
		if err != nil {
			fail("could not encode settlement report")
		}
		fmt.Println(string(encoded))
		count++
	}
	if rows.Err() != nil {
		fail("settlement report interrupted")
	}
	fmt.Printf("Reported payments: %d\n", count)
}
func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
