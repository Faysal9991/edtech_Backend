package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/neoscoder/lms-service/internal/platform/auth"
	"github.com/neoscoder/lms-service/internal/platform/config"
	"github.com/neoscoder/lms-service/internal/platform/database"
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if os.Getenv("BOOTSTRAP_ADMIN_CONFIRM") != "CREATE_FIRST_SUPER_ADMIN" {
		return errors.New("set BOOTSTRAP_ADMIN_CONFIRM=CREATE_FIRST_SUPER_ADMIN to acknowledge the one-shot operation")
	}
	email := strings.ToLower(strings.TrimSpace(os.Getenv("BOOTSTRAP_EMAIL")))
	password := os.Getenv("BOOTSTRAP_PASSWORD")
	name := strings.TrimSpace(os.Getenv("BOOTSTRAP_DISPLAY_NAME"))
	orgName := strings.TrimSpace(os.Getenv("BOOTSTRAP_ORGANIZATION_NAME"))
	orgSlug := strings.ToLower(strings.TrimSpace(os.Getenv("BOOTSTRAP_ORGANIZATION_SLUG")))
	if !strings.Contains(email, "@") || len(password) < 12 || name == "" || orgName == "" || orgSlug == "" {
		return errors.New("BOOTSTRAP_EMAIL, BOOTSTRAP_PASSWORD (12+ characters), BOOTSTRAP_DISPLAY_NAME, BOOTSTRAP_ORGANIZATION_NAME, and BOOTSTRAP_ORGANIZATION_SLUG are required")
	}
	passwordHash, err := auth.NewPasswordHasher(config.Password{MemoryKiB: 64 * 1024, Iterations: 3, Parallelism: 2, SaltBytes: 16, KeyBytes: 32}).Hash(password)
	if err != nil {
		return err
	}
	databaseConfig, err := config.LoadDatabase()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseConfig)
	if err != nil {
		return err
	}
	defer pool.Close()
	ids := platformid.Secure{}
	return database.WithinTx(ctx, pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM organization_memberships m JOIN membership_roles mr ON mr.membership_id=m.id JOIN roles r ON r.id=mr.role_id WHERE r.code='super_admin' AND m.status='active')").Scan(&exists); err != nil {
			return err
		}
		if exists {
			return errors.New("an active super administrator already exists; bootstrap refused")
		}
		orgID := ids.New()
		if err := tx.QueryRow(ctx, "INSERT INTO organizations(id,name,slug) VALUES($1,$2,$3) ON CONFLICT(slug) DO UPDATE SET name=EXCLUDED.name RETURNING id", orgID, orgName, orgSlug).Scan(&orgID); err != nil {
			return err
		}
		userID := ids.New()
		if err := tx.QueryRow(ctx, "INSERT INTO users(id,firebase_uid,email,display_name,status,password_hash,email_verified_at) VALUES($1,'local:'||($1::uuid)::text,$2,$3,'active',$4,now()) RETURNING id", userID, email, name, passwordHash).Scan(&userID); err != nil {
			return err
		}
		membershipID := ids.New()
		if err := tx.QueryRow(ctx, "INSERT INTO organization_memberships(id,organization_id,user_id,status,joined_at) VALUES($1,$2,$3,'active',now()) ON CONFLICT(organization_id,user_id) DO UPDATE SET status='active' RETURNING id", membershipID, orgID, userID).Scan(&membershipID); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, "INSERT INTO membership_roles(membership_id,role_id) SELECT $1,id FROM roles WHERE code='super_admin' ON CONFLICT DO NOTHING", membershipID)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return errors.New("super_admin reference role is missing; apply migrations first")
		}
		if _, err := tx.Exec(ctx, "INSERT INTO user_roles(user_id,role_id,assigned_by) SELECT $1,id,$1 FROM roles WHERE code='admin'", userID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO user_profiles(user_id,first_name) VALUES($1,$2)", userID, name); err != nil {
			return err
		}
		fmt.Printf("created first super administrator %s in organization %s\n", userID, orgID)
		return nil
	})
}
