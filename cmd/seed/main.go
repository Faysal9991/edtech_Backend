package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	"github.com/Faysal9991/edtech_Backend/internal/platform/database"
	platformid "github.com/Faysal9991/edtech_Backend/internal/platform/id"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type seeder struct {
	tx  pgx.Tx
	ids platformid.Secure
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	environment := strings.TrimSpace(os.Getenv("APP_ENV"))
	if environment == "" {
		environment = "development"
	}
	if environment != "development" && environment != "test" {
		return errors.New("development seed is allowed only when APP_ENV is development or test")
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
	return database.WithinTx(ctx, pool, func(tx pgx.Tx) error {
		s := &seeder{tx: tx}
		orgID, err := s.organization(ctx)
		if err != nil {
			return err
		}
		users := map[string]uuid.UUID{}
		specs := []struct{ email, name, role string }{{"super@lms.local", "Platform Super Admin", "super_admin"}, {"admin@acme.test", "Acme Organization Admin", "organization_admin"}, {"instructor1@acme.test", "Amina Instructor", "instructor"}, {"instructor2@acme.test", "Rahim Instructor", "instructor"}, {"student1@acme.test", "Nadia Student", "student"}, {"student2@acme.test", "Sakib Student", "student"}, {"student3@acme.test", "Maya Student", "student"}, {"student4@acme.test", "Tanvir Student", "student"}}
		for _, spec := range specs {
			id, err := s.user(ctx, orgID, spec.email, spec.name, spec.role)
			if err != nil {
				return err
			}
			users[spec.email] = id
		}
		categoryID, err := s.category(ctx, orgID, "Backend Engineering", "backend-engineering")
		if err != nil {
			return err
		}
		freeID, freeLesson, err := s.course(ctx, orgID, categoryID, users["admin@acme.test"], users["instructor1@acme.test"], "Production Go Foundations", "production-go-foundations", true, 0)
		if err != nil {
			return err
		}
		paidID, _, err := s.course(ctx, orgID, categoryID, users["admin@acme.test"], users["instructor2@acme.test"], "Advanced Go Systems", "advanced-go-systems", false, 100000)
		if err != nil {
			return err
		}
		if err := s.assessments(ctx, orgID, freeID, freeLesson); err != nil {
			return err
		}
		if err := s.live(ctx, orgID, freeID, users["instructor1@acme.test"]); err != nil {
			return err
		}
		for i, email := range []string{"student1@acme.test", "student2@acme.test", "student3@acme.test"} {
			if err := s.enrollment(ctx, orgID, freeID, freeLesson, users[email], i > 0); err != nil {
				return err
			}
		}
		if err := s.enrollment(ctx, orgID, paidID, uuid.Nil, users["student4@acme.test"], false); err != nil {
			return err
		}
		fmt.Println("seed complete; development tokens use Authorization: Bearer dev:<email>, for example dev:student1@acme.test")
		return nil
	})
}
func (s *seeder) organization(ctx context.Context) (uuid.UUID, error) {
	id := s.ids.New()
	err := s.tx.QueryRow(ctx, "INSERT INTO organizations(id,name,slug) VALUES($1,'Acme Learning','acme-learning') ON CONFLICT(slug) DO UPDATE SET name=EXCLUDED.name,updated_at=now() RETURNING id", id).Scan(&id)
	return id, err
}
func devUID(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(email)))
	return "dev-" + hex.EncodeToString(sum[:16])
}
func (s *seeder) user(ctx context.Context, orgID uuid.UUID, email, name, role string) (uuid.UUID, error) {
	userID := s.ids.New()
	if err := s.tx.QueryRow(ctx, "INSERT INTO users(id,firebase_uid,email,display_name,status) VALUES($1,$2,$3,$4,'active') ON CONFLICT(firebase_uid) DO UPDATE SET email=EXCLUDED.email,display_name=EXCLUDED.display_name,status='active',updated_at=now() RETURNING id", userID, devUID(email), email, name).Scan(&userID); err != nil {
		return uuid.Nil, err
	}
	membershipID := s.ids.New()
	if err := s.tx.QueryRow(ctx, "INSERT INTO organization_memberships(id,organization_id,user_id,status,joined_at) VALUES($1,$2,$3,'active',now()) ON CONFLICT(organization_id,user_id) DO UPDATE SET status='active',updated_at=now() RETURNING id", membershipID, orgID, userID).Scan(&membershipID); err != nil {
		return uuid.Nil, err
	}
	_, err := s.tx.Exec(ctx, "INSERT INTO membership_roles(membership_id,role_id) SELECT $1,id FROM roles WHERE code=$2 ON CONFLICT DO NOTHING", membershipID, role)
	return userID, err
}
func (s *seeder) category(ctx context.Context, orgID uuid.UUID, name, slug string) (uuid.UUID, error) {
	id := s.ids.New()
	err := s.tx.QueryRow(ctx, "INSERT INTO course_categories(id,organization_id,name,slug,description) VALUES($1,$2,$3,$4,'Server-side engineering') ON CONFLICT(organization_id,slug) DO UPDATE SET name=EXCLUDED.name,updated_at=now() RETURNING id", id, orgID, name, slug).Scan(&id)
	return id, err
}
func (s *seeder) course(ctx context.Context, orgID, categoryID, creator, instructor uuid.UUID, title, slug string, free bool, price int64) (uuid.UUID, uuid.UUID, error) {
	courseID := s.ids.New()
	if err := s.tx.QueryRow(ctx, "INSERT INTO courses(id,organization_id,category_id,title,slug,description,language,level,status,is_free,price_minor,currency,published_at,created_by) VALUES($1,$2,$3,$4,$5,'A practical, production-focused course.','en','beginner','published',$6,$7,'BDT',now(),$8) ON CONFLICT(organization_id,slug) DO UPDATE SET title=EXCLUDED.title,description=EXCLUDED.description,status='published',updated_at=now() RETURNING id", courseID, orgID, categoryID, title, slug, free, price, creator).Scan(&courseID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	_, err := s.tx.Exec(ctx, "INSERT INTO course_instructors(course_id,instructor_id,assigned_by) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", courseID, instructor, creator)
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	moduleID := s.ids.New()
	if err := s.tx.QueryRow(ctx, "INSERT INTO course_modules(id,course_id,title,description,position) VALUES($1,$2,'Getting Started','Core concepts',1) ON CONFLICT(course_id,position) DO UPDATE SET title=EXCLUDED.title RETURNING id", moduleID, courseID).Scan(&moduleID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	lessonID := s.ids.New()
	if err := s.tx.QueryRow(ctx, "INSERT INTO lessons(id,module_id,title,description,lesson_type,body,position,is_preview,is_required,is_published) VALUES($1,$2,'Welcome and Setup','Prepare the development environment','text','Install Go, PostgreSQL, Redis, and Docker.',1,true,true,true) ON CONFLICT(module_id,position) DO UPDATE SET title=EXCLUDED.title,is_published=true RETURNING id", lessonID, moduleID).Scan(&lessonID); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	second := s.ids.New()
	_, err = s.tx.Exec(ctx, "INSERT INTO lessons(id,module_id,title,description,lesson_type,body,position,is_preview,is_required,is_published) VALUES($1,$2,'Building the Service','Implement a modular monolith','text','Build the service through explicit boundaries.',2,false,true,true) ON CONFLICT(module_id,position) DO UPDATE SET title=EXCLUDED.title,is_published=true", second, moduleID)
	return courseID, lessonID, err
}
func (s *seeder) assessments(ctx context.Context, orgID, courseID, lessonID uuid.UUID) error {
	quizID := uuid.Nil
	if err := s.tx.QueryRow(ctx, "SELECT id FROM quizzes WHERE course_id=$1 AND title='Go Foundations Check' LIMIT 1", courseID).Scan(&quizID); errors.Is(err, pgx.ErrNoRows) {
		quizID = s.ids.New()
		if err := s.tx.QueryRow(ctx, "INSERT INTO quizzes(id,organization_id,course_id,title,instructions,status,attempt_limit,pass_percentage,is_required) VALUES($1,$2,$3,'Go Foundations Check','Choose the correct answer.','published',3,60,true) RETURNING id", quizID, orgID, courseID).Scan(&quizID); err != nil {
			return err
		}
	}
	var count int
	if err := s.tx.QueryRow(ctx, "SELECT count(*) FROM quiz_questions WHERE quiz_id=$1", quizID).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		questionID := s.ids.New()
		if _, err := s.tx.Exec(ctx, "INSERT INTO quiz_questions(id,quiz_id,question_type,prompt,points,position) VALUES($1,$2,'single','Which package provides the Go HTTP server?',10,1)", questionID, quizID); err != nil {
			return err
		}
		if _, err := s.tx.Exec(ctx, "INSERT INTO quiz_question_options(id,question_id,text,is_correct,position) VALUES($1,$2,'net/http',true,1),($3,$2,'fmt',false,2),($4,$2,'bytes',false,3)", s.ids.New(), questionID, s.ids.New(), s.ids.New()); err != nil {
			return err
		}
	}
	assignmentID := uuid.Nil
	if err := s.tx.QueryRow(ctx, "SELECT id FROM assignments WHERE course_id=$1 AND title='Build a Health Endpoint' LIMIT 1", courseID).Scan(&assignmentID); errors.Is(err, pgx.ErrNoRows) {
		assignmentID = s.ids.New()
		_, err := s.tx.Exec(ctx, "INSERT INTO assignments(id,organization_id,course_id,title,instructions,due_at,maximum_score,passing_score,allowed_file_types,maximum_submissions,is_required,status) VALUES($1,$2,$3,'Build a Health Endpoint','Submit a short design note.',now()+interval '14 days',100,60,ARRAY['application/pdf','text/plain'],2,true,'published')", assignmentID, orgID, courseID)
		return err
	}
	_ = lessonID
	return nil
}
func (s *seeder) live(ctx context.Context, orgID, courseID, creator uuid.UUID) error {
	var exists bool
	if err := s.tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM live_sessions WHERE course_id=$1 AND title='Weekly Q&A')", courseID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	id := s.ids.New()
	_, err := s.tx.Exec(ctx, "INSERT INTO live_sessions(id,organization_id,course_id,title,description,room_name,status,scheduled_start_at,scheduled_end_at,created_by) VALUES($1,$2,$3,'Weekly Q&A','Questions with the instructor',$4,'scheduled',now()+interval '2 days',now()+interval '2 days 1 hour',$5)", id, orgID, courseID, "seed_"+id.String(), creator)
	return err
}
func (s *seeder) enrollment(ctx context.Context, orgID, courseID, lessonID, studentID uuid.UUID, withProgress bool) error {
	coursePrice := int64(0)
	currency := "BDT"
	if err := s.tx.QueryRow(ctx, "SELECT price_minor,currency FROM courses WHERE id=$1", courseID).Scan(&coursePrice, &currency); err != nil {
		return err
	}
	source := "free"
	status := "active"
	if coursePrice > 0 {
		source = "payment"
		status = "pending_payment"
	}
	id := s.ids.New()
	if err := s.tx.QueryRow(ctx, "INSERT INTO enrollments(id,organization_id,course_id,student_id,status,source,price_minor_snapshot,currency_snapshot,enrolled_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,CASE WHEN $5='active' THEN now() END) ON CONFLICT(course_id,student_id) DO UPDATE SET updated_at=now() RETURNING id", id, orgID, courseID, studentID, status, source, coursePrice, currency).Scan(&id); err != nil {
		return err
	}
	if withProgress && lessonID != uuid.Nil {
		_, err := s.tx.Exec(ctx, "INSERT INTO lesson_progress(id,enrollment_id,lesson_id,state,last_position_seconds,total_watched_seconds,completed_at) VALUES($1,$2,$3,'completed',0,0,now()) ON CONFLICT(enrollment_id,lesson_id) DO UPDATE SET state='completed',completed_at=COALESCE(lesson_progress.completed_at,now()),updated_at=now()", s.ids.New(), id, lessonID)
		if err != nil {
			return err
		}
	}
	_, err := s.tx.Exec(ctx, "INSERT INTO notifications(id,user_id,organization_id,type,title,body,deduplication_key) VALUES($1,$2,$3,'enrollment.activated','Welcome to your course','Your seed enrollment is ready.',$4) ON CONFLICT(user_id,deduplication_key) WHERE deduplication_key IS NOT NULL DO NOTHING", s.ids.New(), studentID, orgID, "seed-enrollment-"+courseID.String())
	return err
}
