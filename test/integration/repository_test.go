//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Faysal9991/edtech_Backend/internal/data"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func databaseForTest(t *testing.T) (context.Context, *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	dsn := os.Getenv("TEST_DATABASE_URL")
	gooseTable := "goose_db_version"
	if dsn == "" {
		testcontainers.SkipIfProviderIsNotHealthy(t)
		container, err := postgrescontainer.Run(ctx, "postgres:16-alpine", postgrescontainer.WithDatabase("lms"), postgrescontainer.WithUsername("lms"), postgrescontainer.WithPassword("lms"), testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
		dsn, err = container.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
	} else {
		admin, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		schema := "lms_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		gooseTable = schema + ".goose_db_version"
		if _, err := admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
			admin.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
			admin.Close()
		})
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := parsed.Query()
		query.Set("search_path", schema+",public")
		parsed.RawQuery = query.Encode()
		dsn = parsed.String()
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := filepath.Abs("../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetTableName(gooseTable)
	if err := goose.Up(db, migrations); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, pool
}

func numeric(value string) pgtype.Numeric {
	var result pgtype.Numeric
	_ = result.Scan(value)
	return result
}

func TestSchemaIsolationAndIdempotency(t *testing.T) {
	ctx, pool := databaseForTest(t)
	q := data.New(pool)
	ids := platformid.Secure{}
	orgA, orgB, userID := ids.New(), ids.New(), ids.New()
	_, err := pool.Exec(ctx, "INSERT INTO organizations(id,name,slug) VALUES($1,'A Org','a-org'),($2,'B Org','b-org')", orgA, orgB)
	if err == nil {
		_, err = pool.Exec(ctx, "INSERT INTO users(id,firebase_uid,email,display_name) VALUES($1,'firebase-student','student@example.test','Student')", userID)
	}
	if err != nil {
		t.Fatal(err)
	}
	membershipID := ids.New()
	if _, err := pool.Exec(ctx, "INSERT INTO organization_memberships(id,organization_id,user_id,status,joined_at) VALUES($1,$2,$3,'active',now())", membershipID, orgA, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetActiveMembership(ctx, data.GetActiveMembershipParams{UserID: userID, OrganizationID: orgB}); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cross-organization membership lookup must fail, got %v", err)
	}
	courseID := ids.New()
	if _, err := pool.Exec(ctx, "INSERT INTO courses(id,organization_id,title,slug,description,status,is_free,price_minor,currency,created_by) VALUES($1,$2,'Course','course','Description','published',true,0,'BDT',$3)", courseID, orgA, userID); err != nil {
		t.Fatal(err)
	}
	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := q.CreateEnrollment(ctx, data.CreateEnrollmentParams{ID: ids.New(), OrganizationID: orgA, CourseID: courseID, StudentID: userID, Status: "active", Source: "free", PriceMinorSnapshot: 0, CurrencySnapshot: "BDT"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM enrollments WHERE course_id=$1 AND student_id=$2", courseID, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one enrollment, got %d", count)
	}
	eventID := ids.New()
	params := data.CreatePaymentWebhookEventParams{ID: eventID, ProviderEventID: "evt_replayed", EventType: "payment_intent.succeeded", Payload: []byte(`{}`)}
	first, err := q.CreatePaymentWebhookEvent(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	params.ID = ids.New()
	second, err := q.CreatePaymentWebhookEvent(ctx, params)
	if err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 0 {
		t.Fatalf("webhook replay was not idempotent: %d %d", first, second)
	}
	if err := q.InsertAuditLog(ctx, data.InsertAuditLogParams{ID: ids.New(), OrganizationID: uuid.NullUUID{UUID: orgA, Valid: true}, ActorUserID: uuid.NullUUID{UUID: userID, Valid: true}, Action: "test.action", ResourceType: "course", ResourceID: uuid.NullUUID{UUID: courseID, Valid: true}, BeforeData: []byte(`{"status":"draft"}`), AfterData: []byte(`{"status":"published"}`)}); err != nil {
		t.Fatal(err)
	}
	auditRows, err := q.ListOrganizationAuditLogs(ctx, data.ListOrganizationAuditLogsParams{OrganizationID: uuid.NullUUID{UUID: orgA, Valid: true}, PageSize: 10})
	if err != nil || len(auditRows) != 1 || auditRows[0].ActorEmail.String != "student@example.test" {
		t.Fatalf("audit history query failed: rows=%d err=%v", len(auditRows), err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO orders(id,organization_id,user_id,status,amount_minor,currency,idempotency_key) VALUES($1,$2,$3,'paid',12500,'BDT','report-order')", ids.New(), orgA, userID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	trend, err := q.RevenueTrend(ctx, data.RevenueTrendParams{OrganizationID: orgA, FromTime: pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}, ToTime: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}})
	if err != nil || len(trend) != 1 || trend[0].GrossMinor != 12500 || trend[0].Currency != "BDT" {
		t.Fatalf("revenue trend query failed: rows=%v err=%v", trend, err)
	}
	var indexes int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname IN ('courses_org_listing_idx','enrollments_student_listing_idx','outbox_pending_idx','quizzes_course_cursor_idx','assignments_course_cursor_idx','certificates_student_cursor_idx','live_sessions_org_cursor_idx')").Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 7 {
		t.Fatalf("expected critical indexes, found %d", indexes)
	}
}

func TestConcurrencyDedupeAndAccessGuards(t *testing.T) {
	ctx, pool := databaseForTest(t)
	q := data.New(pool)
	ids := platformid.Secure{}
	orgID, ownerID, otherID := ids.New(), ids.New(), ids.New()
	if _, err := pool.Exec(ctx, "INSERT INTO organizations(id,name,slug) VALUES($1,'Guard Org','guard-org')", orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO users(id,firebase_uid,email,display_name) VALUES($1,'owner-firebase','owner@example.test','Owner'),($2,'other-firebase','other@example.test','Other')", ownerID, otherID); err != nil {
		t.Fatal(err)
	}
	courseID := ids.New()
	if _, err := pool.Exec(ctx, "INSERT INTO courses(id,organization_id,title,slug,description,status,is_free,price_minor,currency,created_by) VALUES($1,$2,'Guard Course','guard-course','Description','published',true,0,'BDT',$3)", courseID, orgID, ownerID); err != nil {
		t.Fatal(err)
	}
	enrollment, err := q.CreateEnrollment(ctx, data.CreateEnrollmentParams{ID: ids.New(), OrganizationID: orgID, CourseID: courseID, StudentID: ownerID, Status: "active", Source: "free", PriceMinorSnapshot: 0, CurrencySnapshot: "BDT"})
	if err != nil {
		t.Fatal(err)
	}
	quizID := ids.New()
	if _, err := pool.Exec(ctx, "INSERT INTO quizzes(id,organization_id,course_id,title,status) VALUES($1,$2,$3,'Concurrency Quiz','published')", quizID, orgID, courseID); err != nil {
		t.Fatal(err)
	}
	const workers = 10
	var wg sync.WaitGroup
	successes := make(chan bool, workers)
	for i := range workers {
		wg.Add(1)
		go func(attemptNumber int) {
			defer wg.Done()
			_, createErr := q.CreateQuizAttempt(ctx, data.CreateQuizAttemptParams{ID: ids.New(), QuizID: quizID, EnrollmentID: enrollment.ID, StudentID: ownerID, AttemptNumber: int32(attemptNumber + 1), QuestionSnapshot: []byte(`{"questions":[]}`), QuestionOrder: []uuid.UUID{}, StartedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}, MaxPoints: numeric("0")})
			successes <- createErr == nil
		}(i)
	}
	wg.Wait()
	close(successes)
	created := 0
	for success := range successes {
		if success {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("partial unique index allowed %d simultaneous active quiz attempts", created)
	}

	assetID := ids.New()
	if _, err := pool.Exec(ctx, "INSERT INTO media_assets(id,organization_id,owner_user_id,kind,status,storage_key,original_filename,content_type,size_bytes) VALUES($1,$2,$3,'pdf','ready','guards/file.pdf','file.pdf','application/pdf',10)", assetID, orgID, ownerID); err != nil {
		t.Fatal(err)
	}
	ownerAllowed, err := q.CanUserAccessMedia(ctx, data.CanUserAccessMediaParams{MediaAssetID: assetID, UserID: ownerID})
	if err != nil || !ownerAllowed {
		t.Fatalf("media owner access failed: %v", err)
	}
	otherAllowed, err := q.CanUserAccessMedia(ctx, data.CanUserAccessMediaParams{MediaAssetID: assetID, UserID: otherID})
	if err != nil || otherAllowed {
		t.Fatalf("cross-user private media access was allowed: %v", err)
	}

	outbox := data.InsertOutboxEventParams{ID: ids.New(), AggregateType: "course", AggregateID: courseID, EventType: "course.test", Payload: []byte(`{"user_id":"` + ownerID.String() + `"}`), DeduplicationKey: "guard-dedupe"}
	if err := q.InsertOutboxEvent(ctx, outbox); err != nil {
		t.Fatal(err)
	}
	outbox.ID = ids.New()
	if err := q.InsertOutboxEvent(ctx, outbox); err != nil {
		t.Fatal(err)
	}
	var outboxCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE deduplication_key='guard-dedupe'").Scan(&outboxCount); err != nil || outboxCount != 1 {
		t.Fatalf("outbox deduplication failed: count=%d err=%v", outboxCount, err)
	}

	liveParams := data.CreateLiveWebhookEventParams{ID: ids.New(), ProviderEventID: "live-replay", EventType: "participant_joined", Payload: []byte(`{}`)}
	first, err := q.CreateLiveWebhookEvent(ctx, liveParams)
	if err != nil {
		t.Fatal(err)
	}
	liveParams.ID = ids.New()
	second, err := q.CreateLiveWebhookEvent(ctx, liveParams)
	if err != nil || first != 1 || second != 0 {
		t.Fatalf("live webhook replay was not idempotent: first=%d second=%d err=%v", first, second, err)
	}

	orderKey := "concurrent-order-key"
	orderSuccesses := 0
	for i := 0; i < 2; i++ {
		_, createErr := q.CreateOrder(ctx, data.CreateOrderParams{ID: ids.New(), OrganizationID: orgID, UserID: ownerID, AmountMinor: 1000, Currency: "BDT", IdempotencyKey: orderKey})
		if createErr == nil {
			orderSuccesses++
		} else if !errors.Is(createErr, pgx.ErrNoRows) {
			t.Fatal(createErr)
		}
	}
	if orderSuccesses != 1 {
		t.Fatalf("expected exactly one idempotent order insert, got %d", orderSuccesses)
	}

	var assignmentAssetsTable string
	if err := pool.QueryRow(ctx, "SELECT to_regclass('assignment_assets')::text").Scan(&assignmentAssetsTable); err != nil || assignmentAssetsTable == "" {
		t.Fatalf("assignment attachment migration missing: %v", err)
	}
	var openAttendanceIndex string
	if err := pool.QueryRow(ctx, "SELECT to_regclass('live_attendance_one_open_uidx')::text").Scan(&openAttendanceIndex); err != nil || openAttendanceIndex == "" {
		t.Fatalf("live attendance uniqueness migration missing: %v", err)
	}
}
