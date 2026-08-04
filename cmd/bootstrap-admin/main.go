package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	"github.com/Faysal9991/edtech_Backend/internal/platform/database"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	"github.com/jackc/pgx/v5"
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
	uid := strings.TrimSpace(os.Getenv("BOOTSTRAP_FIREBASE_UID"))
	email := strings.ToLower(strings.TrimSpace(os.Getenv("BOOTSTRAP_EMAIL")))
	name := strings.TrimSpace(os.Getenv("BOOTSTRAP_DISPLAY_NAME"))
	orgName := strings.TrimSpace(os.Getenv("BOOTSTRAP_ORGANIZATION_NAME"))
	orgSlug := strings.ToLower(strings.TrimSpace(os.Getenv("BOOTSTRAP_ORGANIZATION_SLUG")))
	if uid == "" || !strings.Contains(email, "@") || name == "" || orgName == "" || orgSlug == "" {
		return errors.New("BOOTSTRAP_FIREBASE_UID, BOOTSTRAP_EMAIL, BOOTSTRAP_DISPLAY_NAME, BOOTSTRAP_ORGANIZATION_NAME, and BOOTSTRAP_ORGANIZATION_SLUG are required")
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
		if err := tx.QueryRow(ctx, "INSERT INTO users(id,firebase_uid,email,display_name,status) VALUES($1,$2,$3,$4,'active') ON CONFLICT(firebase_uid) DO UPDATE SET email=EXCLUDED.email,display_name=EXCLUDED.display_name,status='active' RETURNING id", userID, uid, email, name).Scan(&userID); err != nil {
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
		fmt.Printf("created first super administrator %s in organization %s\n", userID, orgID)
		return nil
	})
}
