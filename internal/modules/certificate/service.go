package certificate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/neoscoder/lms-service/internal/data"
	"github.com/neoscoder/lms-service/internal/platform/clock"
	"github.com/neoscoder/lms-service/internal/platform/config"
	"github.com/neoscoder/lms-service/internal/platform/database"
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
	"github.com/neoscoder/lms-service/internal/platform/queue"
	"github.com/neoscoder/lms-service/internal/platform/storage"
	"github.com/phpdave11/gofpdf"
	"github.com/skip2/go-qrcode"
)

var (
	ErrNotFound  = errors.New("certificate not found")
	ErrForbidden = errors.New("certificate access denied")
)

type Service struct {
	db    database.Beginner
	q     *data.Queries
	ids   platformid.Generator
	clock clock.Clock
	jobs  queue.Client
	store storage.Store
	cfg   config.Config
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator, c clock.Clock, jobs queue.Client, store storage.Store, cfg config.Config) *Service {
	return &Service{db: db, q: q, ids: ids, clock: c, jobs: jobs, store: store, cfg: cfg}
}
func numeric(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%.2f", v))
	return n
}

func (s *Service) Evaluate(ctx context.Context, enrollmentID uuid.UUID) (*data.Certificate, error) {
	var cert data.Certificate
	created := false
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		enrollment, err := q.GetEnrollmentForUpdate(ctx, enrollmentID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if enrollment.Status == "completed" {
			var id uuid.UUID
			if e := tx.QueryRow(ctx, "SELECT id FROM certificates WHERE enrollment_id=$1", enrollmentID).Scan(&id); errors.Is(e, pgx.ErrNoRows) {
				return nil
			} else if e != nil {
				return e
			}
			existing, e := q.GetCertificate(ctx, id)
			if e != nil {
				return e
			}
			cert = existing
			created = false
			return nil
		}
		if enrollment.Status != "active" {
			return nil
		}
		facts, err := q.CompletionRequirements(ctx, data.CompletionRequirementsParams{CourseID: enrollment.CourseID, EnrollmentID: enrollment.ID})
		if err != nil {
			return err
		}
		complete := facts.RequiredLessons == facts.CompletedLessons && facts.RequiredQuizzes == facts.PassedQuizzes && facts.RequiredAssignments == facts.PassedAssignments
		total := facts.RequiredLessons + facts.RequiredQuizzes + facts.RequiredAssignments
		done := facts.CompletedLessons + facts.PassedQuizzes + facts.PassedAssignments
		percentage := 100.0
		if total > 0 {
			percentage = float64(done) / float64(total) * 100
		}
		counts := []int64{facts.RequiredLessons, facts.CompletedLessons, facts.RequiredQuizzes, facts.PassedQuizzes, facts.RequiredAssignments, facts.PassedAssignments}
		for _, count := range counts {
			if count < 0 || count > math.MaxInt32 {
				return errors.New("completion requirement count exceeds supported range")
			}
		}
		// All database counts were range checked above before narrowing to int32.
		if _, err = q.CreateCompletionSnapshot(ctx, data.CreateCompletionSnapshotParams{ID: s.ids.New(), EnrollmentID: enrollment.ID, RequiredLessons: int32(facts.RequiredLessons), CompletedLessons: int32(facts.CompletedLessons), RequiredQuizzes: int32(facts.RequiredQuizzes), PassedQuizzes: int32(facts.PassedQuizzes), RequiredAssignments: int32(facts.RequiredAssignments), PassedAssignments: int32(facts.PassedAssignments), Percentage: numeric(percentage), IsComplete: complete}); err != nil { // #nosec G115 -- checked against math.MaxInt32
			return err
		}
		quizAverage, assignmentAverage, passingPercentage := 100.0, 100.0, 60.0
		if err = tx.QueryRow(ctx, `SELECT
			CASE WHEN $2::integer=0 THEN 100 ELSE COALESCE((SELECT avg(best.percentage) FROM (SELECT quiz_id,max(percentage) AS percentage FROM quiz_attempts WHERE enrollment_id=$1 AND status IN ('submitted','graded') GROUP BY quiz_id) best),0) END,
			CASE WHEN $3::integer=0 THEN 100 ELSE COALESCE((SELECT avg(g.percentage) FROM grades g JOIN assignment_submissions sub ON sub.id=g.assignment_submission_id WHERE sub.enrollment_id=$1),0) END,
			c.passing_percentage FROM enrollments e JOIN courses c ON c.id=e.course_id WHERE e.id=$1`, enrollment.ID, facts.RequiredQuizzes, facts.RequiredAssignments).Scan(&quizAverage, &assignmentAverage, &passingPercentage); err != nil {
			return err
		}
		finalPercentage := (percentage + quizAverage + assignmentAverage) / 3
		passed := complete && finalPercentage >= passingPercentage
		components := []byte(fmt.Sprintf(`{"completion":%.2f,"quiz":%.2f,"assignment":%.2f}`, percentage, quizAverage, assignmentAverage))
		if _, err = tx.Exec(ctx, `INSERT INTO course_results(id,enrollment_id,student_id,course_id,quiz_percentage,assignment_percentage,completion_percentage,final_percentage,passing_percentage,passed,components)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT(enrollment_id) DO UPDATE SET quiz_percentage=EXCLUDED.quiz_percentage,assignment_percentage=EXCLUDED.assignment_percentage,completion_percentage=EXCLUDED.completion_percentage,final_percentage=EXCLUDED.final_percentage,passing_percentage=EXCLUDED.passing_percentage,passed=EXCLUDED.passed,components=EXCLUDED.components,calculated_at=now(),updated_at=now()`, s.ids.New(), enrollment.ID, enrollment.StudentID, enrollment.CourseID, quizAverage, assignmentAverage, percentage, finalPercentage, passingPercentage, passed, components); err != nil {
			return err
		}
		if !passed {
			return nil
		}
		if _, err = q.SetEnrollmentStatus(ctx, data.SetEnrollmentStatusParams{ID: enrollment.ID, Status: "completed"}); err != nil {
			return err
		}
		verification, err := s.ids.Token(24)
		if err != nil {
			return err
		}
		numberToken, err := s.ids.Token(9)
		if err != nil {
			return err
		}
		number := "LMS-" + strings.ToUpper(strings.ReplaceAll(numberToken, "-", ""))
		if len(number) > 24 {
			number = number[:24]
		}
		cert, err = q.CreateCertificate(ctx, data.CreateCertificateParams{ID: s.ids.New(), OrganizationID: enrollment.OrganizationID, EnrollmentID: enrollment.ID, StudentID: enrollment.StudentID, CourseID: enrollment.CourseID, CertificateNumber: number, VerificationCode: verification, IssuedAt: pgtype.Timestamptz{Time: s.clock.Now(), Valid: true}})
		if err != nil {
			return err
		}
		created = true
		payload := []byte(fmt.Sprintf(`{"user_id":%q,"organization_id":%q,"certificate_id":%q}`, enrollment.StudentID.String(), enrollment.OrganizationID.String(), cert.ID.String()))
		return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "enrollment", AggregateID: enrollment.ID, EventType: "course.completed", Payload: payload, DeduplicationKey: "course.completed:" + enrollment.ID.String()})
	})
	if err != nil {
		return nil, err
	}
	if !created {
		return nil, nil
	}
	if err := s.jobs.Enqueue(queue.TypeCertificateGenerate, map[string]string{"certificate_id": cert.ID.String()}); err != nil {
		return &cert, err
	}
	return &cert, nil
}

func (s *Service) Generate(ctx context.Context, certificateID uuid.UUID) error {
	cert, err := s.q.GetCertificate(ctx, certificateID)
	if err != nil {
		return err
	}
	if cert.Status == "ready" {
		return nil
	}
	verification, err := s.q.VerifyCertificate(ctx, cert.VerificationCode)
	if err != nil {
		return err
	}
	verifyURL := strings.TrimSuffix(s.cfg.URLs.CertificateVerify, "/") + "/" + cert.VerificationCode
	pdfBytes, err := renderPDF(verification.StudentName, verification.CourseTitle, verification.OrganizationName, cert.CertificateNumber, cert.IssuedAt.Time, verifyURL)
	if err != nil {
		return err
	}
	assetID := s.ids.New()
	key := fmt.Sprintf("organizations/%s/certificates/%s.pdf", cert.OrganizationID, cert.ID)
	_, err = s.store.Put(ctx, key, "application/pdf", bytes.NewReader(pdfBytes), int64(len(pdfBytes)))
	if err != nil {
		return err
	}
	return database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		_, err := q.CreateMediaAsset(ctx, data.CreateMediaAssetParams{ID: assetID, OrganizationID: cert.OrganizationID, OwnerUserID: cert.StudentID, Kind: "certificate", StorageKey: key, OriginalFilename: cert.CertificateNumber + ".pdf", ContentType: "application/pdf", SizeBytes: int64(len(pdfBytes)), ChecksumSha256: pgtype.Text{}})
		if err != nil {
			return err
		}
		if _, err = q.SetMediaAssetStatus(ctx, data.SetMediaAssetStatusParams{ID: assetID, Status: "ready", FailureReason: pgtype.Text{}}); err != nil {
			return err
		}
		if _, err = q.SetCertificateReady(ctx, data.SetCertificateReadyParams{ID: cert.ID, MediaAssetID: uuid.NullUUID{UUID: assetID, Valid: true}}); err != nil {
			return err
		}
		payload := []byte(fmt.Sprintf(`{"user_id":%q,"organization_id":%q,"certificate_id":%q}`, cert.StudentID.String(), cert.OrganizationID.String(), cert.ID.String()))
		return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "certificate", AggregateID: cert.ID, EventType: "certificate.ready", Payload: payload, DeduplicationKey: "certificate.ready:" + cert.ID.String()})
	})
}

func renderPDF(student, course, organization, number string, issued time.Time, verifyURL string) ([]byte, error) {
	qr, err := qrcode.Encode(verifyURL, qrcode.Medium, 256)
	if err != nil {
		return nil, err
	}
	pdf := gofpdf.New("L", "mm", "A4", "")
	pdf.SetTitle("Course Completion Certificate", false)
	pdf.AddPage()
	pdf.SetDrawColor(40, 80, 130)
	pdf.SetLineWidth(2)
	pdf.Rect(10, 10, 277, 190, "D")
	pdf.SetFont("Helvetica", "B", 28)
	pdf.SetY(30)
	pdf.CellFormat(0, 15, "Certificate of Completion", "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 14)
	pdf.CellFormat(0, 10, "This certifies that", "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "B", 24)
	pdf.CellFormat(0, 18, student, "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 14)
	pdf.CellFormat(0, 10, "has successfully completed", "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(0, 16, course, "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 12)
	pdf.CellFormat(0, 9, organization, "", 1, "C", false, 0, "")
	pdf.CellFormat(0, 9, "Completed: "+issued.UTC().Format("2 January 2006"), "", 1, "C", false, 0, "")
	pdf.CellFormat(0, 9, "Certificate: "+number, "", 1, "C", false, 0, "")
	pdf.RegisterImageOptionsReader("qr", gofpdf.ImageOptions{ImageType: "PNG"}, bytes.NewReader(qr))
	pdf.ImageOptions("qr", 247, 150, 28, 28, false, gofpdf.ImageOptions{ImageType: "PNG"}, 0, "")
	var out bytes.Buffer
	if err := pdf.Output(&out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type Verification struct {
	Valid             bool      `json:"valid"`
	StudentName       string    `json:"student_name"`
	CourseTitle       string    `json:"course_title"`
	OrganizationName  string    `json:"organization_name"`
	IssuedAt          time.Time `json:"issued_at"`
	CertificateNumber string    `json:"certificate_number"`
}

func (s *Service) Verify(ctx context.Context, code string) (Verification, error) {
	row, err := s.q.VerifyCertificate(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return Verification{}, ErrNotFound
	}
	if err != nil {
		return Verification{}, err
	}
	return Verification{Valid: row.Status == "ready", StudentName: row.StudentName, CourseTitle: row.CourseTitle, OrganizationName: row.OrganizationName, IssuedAt: row.IssuedAt.Time, CertificateNumber: row.CertificateNumber}, nil
}
func (s *Service) DownloadURL(ctx context.Context, id, userID uuid.UUID) (string, error) {
	cert, err := s.q.GetCertificate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if cert.StudentID != userID {
		return "", ErrForbidden
	}
	if cert.Status != "ready" || !cert.MediaAssetID.Valid {
		return "", errors.New("certificate is not ready")
	}
	asset, err := s.q.GetMediaAsset(ctx, cert.MediaAssetID.UUID)
	if err != nil {
		return "", err
	}
	return s.store.PresignDownload(ctx, asset.StorageKey, s.cfg.Storage.SignedURLTTL)
}
