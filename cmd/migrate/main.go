package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	migrate "github.com/golang-migrate/migrate/v4"
	postgresmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/stub"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/neoscoder/lms-service/internal/platform/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	command := "up"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "create" {
		if len(os.Args) != 3 {
			return errors.New("usage: migrate create <snake_case_name>")
		}
		return createMigration(os.Args[2])
	}
	databaseConfig, err := config.LoadDatabase()
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", databaseConfig.URL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err = db.PingContext(context.Background()); err != nil {
		return fmt.Errorf("ping migration database: %w", err)
	}
	databaseDriver, err := postgresmigrate.WithInstance(db, &postgresmigrate.Config{})
	if err != nil {
		return fmt.Errorf("initialize PostgreSQL migration driver: %w", err)
	}
	sourceDriver, err := loadSource("migrations")
	if err != nil {
		return err
	}
	migrator, err := migrate.NewWithInstance("lms-goose-files", sourceDriver, "postgres", databaseDriver)
	if err != nil {
		return fmt.Errorf("initialize migrator: %w", err)
	}
	defer func() { _, _ = migrator.Close() }()
	switch command {
	case "up":
		err = migrator.Up()
	case "down", "down-one":
		err = migrator.Steps(-1)
	case "down-all":
		err = migrator.Down()
	case "status":
		version, dirty, versionErr := migrator.Version()
		if errors.Is(versionErr, migrate.ErrNilVersion) {
			fmt.Println("version=0 dirty=false")
			return nil
		}
		if versionErr != nil {
			return versionErr
		}
		fmt.Printf("version=%d dirty=%t\n", version, dirty)
		return nil
	default:
		return fmt.Errorf("unknown migration command %q (use up, down, down-all, status, or create)", command)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return err
}

// loadSource keeps the existing single-file, Goose-compatible migrations as
// the canonical schema while executing them through golang-migrate. This
// avoids two divergent migration histories: each file is split at its explicit
// Down marker and supplied to golang-migrate's source contract in memory.
func loadSource(directory string) (source.Driver, error) {
	files, err := filepath.Glob(filepath.Join(directory, "[0-9][0-9][0-9][0-9][0-9]_*.sql"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	driver, err := stub.WithInstance(nil, &stub.Config{})
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open migration root: %w", err)
	}
	defer func() { _ = root.Close() }()
	instance := driver.(*stub.Stub)
	for _, path := range files {
		name := filepath.Base(path)
		file, readErr := root.Open(name)
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", path, readErr)
		}
		body, readErr := io.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", path, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close %s: %w", path, closeErr)
		}
		version64, parseErr := strconv.ParseUint(name[:5], 10, 32)
		if parseErr != nil {
			return nil, fmt.Errorf("parse migration version %s: %w", name, parseErr)
		}
		parts := strings.SplitN(string(body), "-- +goose Down", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("migration %s has no explicit Down section", name)
		}
		up := strings.TrimSpace(strings.TrimPrefix(parts[0], "-- +goose Up"))
		down := strings.TrimSpace(parts[1])
		version := uint(version64)
		if !instance.Migrations.Append(&source.Migration{Version: version, Direction: source.Up, Identifier: up, Raw: name}) || !instance.Migrations.Append(&source.Migration{Version: version, Direction: source.Down, Identifier: down, Raw: name}) {
			return nil, fmt.Errorf("duplicate migration version %d", version)
		}
	}
	if len(files) == 0 {
		return nil, errors.New("no migrations found")
	}
	return driver, nil
}

func createMigration(name string) error {
	if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789_") != "" {
		return errors.New("migration name must use lowercase letters, digits, and underscores")
	}
	files, err := filepath.Glob(filepath.Join("migrations", "[0-9][0-9][0-9][0-9][0-9]_*.sql"))
	if err != nil {
		return err
	}
	maxVersion := 0
	for _, path := range files {
		if version, parseErr := strconv.Atoi(filepath.Base(path)[:5]); parseErr == nil && version > maxVersion {
			maxVersion = version
		}
	}
	filename := fmt.Sprintf("%05d_%s.sql", maxVersion+1, name)
	root, err := os.OpenRoot("migrations")
	if err != nil {
		return fmt.Errorf("open migrations directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	file, err := root.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.WriteString("-- +goose Up\n\n-- +goose Down\n"); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	fmt.Println(filepath.Join("migrations", filename))
	return nil
}
