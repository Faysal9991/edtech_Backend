package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	databaseConfig, err := config.LoadDatabase()
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", databaseConfig.URL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		return err
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "up":
		return goose.Up(db, "migrations")
	case "down-one":
		return goose.Down(db, "migrations")
	case "status":
		return goose.Status(db, "migrations")
	default:
		return fmt.Errorf("unknown migration command %q (use up, down-one, or status)", command)
	}
}
