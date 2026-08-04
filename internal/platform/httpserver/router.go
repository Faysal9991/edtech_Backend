package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	assignmentmodule "github.com/Faysal9991/edtech_Backend/internal/modules/assignment"
	certificatemodule "github.com/Faysal9991/edtech_Backend/internal/modules/certificate"
	coursemodule "github.com/Faysal9991/edtech_Backend/internal/modules/course"
	enrollmentmodule "github.com/Faysal9991/edtech_Backend/internal/modules/enrollment"
	liveclassmodule "github.com/Faysal9991/edtech_Backend/internal/modules/liveclass"
	mediamodule "github.com/Faysal9991/edtech_Backend/internal/modules/media"
	notificationmodule "github.com/Faysal9991/edtech_Backend/internal/modules/notification"
	organizationmodule "github.com/Faysal9991/edtech_Backend/internal/modules/organization"
	paymentmodule "github.com/Faysal9991/edtech_Backend/internal/modules/payment"
	quizmodule "github.com/Faysal9991/edtech_Backend/internal/modules/quiz"
	reportingmodule "github.com/Faysal9991/edtech_Backend/internal/modules/reporting"
	"github.com/Faysal9991/edtech_Backend/internal/platform/auth"
	"github.com/Faysal9991/edtech_Backend/internal/platform/config"
	"github.com/Faysal9991/edtech_Backend/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Handlers struct {
	Organization *organizationmodule.Handler
	Course       *coursemodule.Handler
	Media        *mediamodule.Handler
	Enrollment   *enrollmentmodule.Handler
	Quiz         *quizmodule.Handler
	Assignment   *assignmentmodule.Handler
	LiveClass    *liveclassmodule.Handler
	Certificate  *certificatemodule.Handler
	Payment      *paymentmodule.Handler
	Notification *notificationmodule.Handler
	Reporting    *reportingmodule.Handler
}
type Dependencies struct {
	Config   config.Config
	Logger   *slog.Logger
	Auth     *auth.Middleware
	DB       *pgxpool.Pool
	Redis    *redis.Client
	Handlers Handlers
}

func NewRouter(d Dependencies) http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.RequestIDMiddleware, httpx.Recover(d.Logger), httpx.SecureHeaders, httpx.CORS(d.Config.HTTP.AllowedOrigins), httpx.Logging(d.Logger), httpx.Metrics)
	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) { httpx.JSON(w, 200, map[string]string{"status": "alive"}) })
	r.Get("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.Ping(ctx); err != nil {
			httpx.Problem(w, r, 503, "Not Ready", "PostgreSQL is unavailable")
			return
		}
		if err := d.Redis.Ping(ctx).Err(); err != nil {
			httpx.Problem(w, r, 503, "Not Ready", "Redis is unavailable")
			return
		}
		httpx.JSON(w, 200, map[string]string{"status": "ready"})
	})
	r.Handle("/metrics", promhttp.Handler())
	r.Post("/api/v1/webhooks/stripe", d.Handlers.Payment.Webhook)
	r.Post("/api/v1/webhooks/livekit", d.Handlers.LiveClass.Webhook)
	r.Get("/api/v1/public/certificates/verify/{code}", d.Handlers.Certificate.Verify)
	r.Group(func(p chi.Router) {
		p.Use(d.Auth.Authenticate)
		p.Post("/api/v1/auth/bootstrap", d.Handlers.Organization.Me)
		p.Get("/api/v1/auth/me", d.Handlers.Organization.Me)
		p.Post("/api/v1/auth/revoke-sessions", d.Handlers.Organization.Revoke)
		p.Post("/api/v1/invitations/accept", d.Handlers.Organization.Accept)
		p.With(d.Auth.RequireSuperAdmin).Get("/api/v1/organizations", d.Handlers.Organization.List)
		p.With(d.Auth.RequireSuperAdmin).Post("/api/v1/organizations", d.Handlers.Organization.Create)
		p.With(d.Auth.RequireSuperAdmin).Get("/api/v1/audit-logs", d.Handlers.Organization.AuditLogs)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor", "student")).Get("/api/v1/organizations/{id}", d.Handlers.Organization.Get)
		p.With(d.Auth.RequireRoles("organization_admin")).Patch("/api/v1/organizations/{id}", d.Handlers.Organization.Update)
		p.With(d.Auth.RequireRoles("organization_admin")).Get("/api/v1/organizations/{id}/members", d.Handlers.Organization.Members)
		p.With(d.Auth.RequireRoles("organization_admin")).Patch("/api/v1/organizations/{id}/members/{membershipId}", d.Handlers.Organization.UpdateMembership)
		p.With(d.Auth.RequireRoles("organization_admin")).Post("/api/v1/organizations/{id}/invitations", d.Handlers.Organization.Invite)
		p.With(d.Auth.RequireRoles("organization_admin")).Get("/api/v1/users", d.Handlers.Organization.ListUsers)
		p.With(d.Auth.RequireRoles("organization_admin")).Get("/api/v1/users/{id}", d.Handlers.Organization.GetUser)
		p.With(d.Auth.RequireAnyMembership).Get("/api/v1/categories", d.Handlers.Course.ListCategories)
		p.With(d.Auth.RequireRoles("organization_admin")).Post("/api/v1/categories", d.Handlers.Course.CreateCategory)
		p.With(d.Auth.RequireRoles("organization_admin")).Patch("/api/v1/categories/{id}", d.Handlers.Course.UpdateCategory)
		p.With(d.Auth.RequireRoles("organization_admin")).Delete("/api/v1/categories/{id}", d.Handlers.Course.DeleteCategory)
		p.Get("/api/v1/courses", d.Handlers.Course.List)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Get("/api/v1/courses/managed", d.Handlers.Course.ListManaged)
		p.With(d.Auth.RequireRoles("organization_admin")).Post("/api/v1/courses", d.Handlers.Course.Create)
		p.Get("/api/v1/courses/{id}", d.Handlers.Course.Detail)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Patch("/api/v1/courses/{id}", d.Handlers.Course.Update)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/courses/{id}/review", d.Handlers.Course.SetStatus("review"))
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/courses/{id}/publish", d.Handlers.Course.SetStatus("published"))
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Delete("/api/v1/courses/{id}/publish", d.Handlers.Course.SetStatus("draft"))
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/courses/{id}/archive", d.Handlers.Course.SetStatus("archived"))
		p.With(d.Auth.RequireRoles("organization_admin")).Post("/api/v1/courses/{id}/instructors", d.Handlers.Course.AssignInstructor)
		p.With(d.Auth.RequireRoles("organization_admin")).Delete("/api/v1/courses/{id}/instructors", d.Handlers.Course.RemoveInstructor)
		p.Get("/api/v1/courses/{id}/modules", d.Handlers.Course.ListContent)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/courses/{id}/modules", d.Handlers.Course.CreateModule)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Patch("/api/v1/modules/{id}", d.Handlers.Course.UpdateModule)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Delete("/api/v1/modules/{id}", d.Handlers.Course.DeleteModule)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/modules/{id}/lessons", d.Handlers.Course.CreateLesson)
		p.Get("/api/v1/lessons/{id}", d.Handlers.Course.GetLesson)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Patch("/api/v1/lessons/{id}", d.Handlers.Course.UpdateLesson)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Delete("/api/v1/lessons/{id}", d.Handlers.Course.DeleteLesson)
		p.With(d.Auth.RequireAnyMembership).Post("/api/v1/media/upload-intents", d.Handlers.Media.CreateIntent)
		p.Post("/api/v1/media/upload-intents/{id}/complete", d.Handlers.Media.Complete)
		p.Post("/api/v1/media/{id}/access-url", d.Handlers.Media.AccessURL)
		p.Post("/api/v1/courses/{id}/enroll", d.Handlers.Enrollment.Enroll)
		p.With(d.Auth.RequireRoles("organization_admin")).Post("/api/v1/courses/{id}/admin-enroll", d.Handlers.Enrollment.AdminEnroll)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Get("/api/v1/courses/{id}/students", d.Handlers.Enrollment.CourseStudents)
		p.Get("/api/v1/enrollments", d.Handlers.Enrollment.List)
		p.Get("/api/v1/enrollments/{id}", d.Handlers.Enrollment.Get)
		p.Delete("/api/v1/enrollments/{id}", d.Handlers.Enrollment.Cancel)
		p.Get("/api/v1/enrollments/{id}/progress", d.Handlers.Enrollment.Progress)
		p.Put("/api/v1/enrollments/{id}/lessons/{lessonId}/progress", d.Handlers.Enrollment.UpdateProgress)
		p.Get("/api/v1/me/resume-learning", d.Handlers.Enrollment.Resume)
		p.Get("/api/v1/quizzes", d.Handlers.Quiz.List)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/quizzes", d.Handlers.Quiz.Create)
		p.Get("/api/v1/quizzes/{id}", d.Handlers.Quiz.Get)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Patch("/api/v1/quizzes/{id}", d.Handlers.Quiz.Update)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Delete("/api/v1/quizzes/{id}", d.Handlers.Quiz.Delete)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/quizzes/{id}/questions", d.Handlers.Quiz.AddQuestion)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Patch("/api/v1/quiz-questions/{id}", d.Handlers.Quiz.UpdateQuestion)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Delete("/api/v1/quiz-questions/{id}", d.Handlers.Quiz.DeleteQuestion)
		p.Get("/api/v1/quizzes/{id}/attempts", d.Handlers.Quiz.Attempts)
		p.Post("/api/v1/quizzes/{id}/attempts", d.Handlers.Quiz.Start)
		p.Get("/api/v1/quiz-attempts/{id}", d.Handlers.Quiz.GetAttempt)
		p.Put("/api/v1/quiz-attempts/{id}/answers", d.Handlers.Quiz.SaveAnswer)
		p.Post("/api/v1/quiz-attempts/{id}/submit", d.Handlers.Quiz.Submit)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/quiz-attempts/{id}/grade", d.Handlers.Quiz.Grade)
		p.Get("/api/v1/assignments", d.Handlers.Assignment.List)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/assignments", d.Handlers.Assignment.Create)
		p.Get("/api/v1/assignments/{id}", d.Handlers.Assignment.Get)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Patch("/api/v1/assignments/{id}", d.Handlers.Assignment.Update)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Delete("/api/v1/assignments/{id}", d.Handlers.Assignment.Delete)
		p.Get("/api/v1/assignments/{id}/submissions", d.Handlers.Assignment.ListSubmissions)
		p.Post("/api/v1/assignments/{id}/submissions", d.Handlers.Assignment.CreateSubmission)
		p.Get("/api/v1/assignment-submissions/{id}", d.Handlers.Assignment.GetSubmission)
		p.Patch("/api/v1/assignment-submissions/{id}", d.Handlers.Assignment.UpdateSubmission)
		p.Post("/api/v1/assignment-submissions/{id}/submit", d.Handlers.Assignment.Submit)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/assignment-submissions/{id}/grade", d.Handlers.Assignment.Grade)
		p.With(d.Auth.RequireAnyMembership).Get("/api/v1/live-sessions", d.Handlers.LiveClass.List)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/live-sessions", d.Handlers.LiveClass.Create)
		p.Get("/api/v1/live-sessions/{id}", d.Handlers.LiveClass.Get)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Patch("/api/v1/live-sessions/{id}", d.Handlers.LiveClass.Update)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Delete("/api/v1/live-sessions/{id}", d.Handlers.LiveClass.SetStatus("cancelled"))
		p.Post("/api/v1/live-sessions/{id}/join-token", d.Handlers.LiveClass.JoinToken)
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/live-sessions/{id}/start", d.Handlers.LiveClass.SetStatus("live"))
		p.With(d.Auth.RequireRoles("organization_admin", "instructor")).Post("/api/v1/live-sessions/{id}/end", d.Handlers.LiveClass.SetStatus("ended"))
		p.Get("/api/v1/results/me", d.Handlers.Certificate.Results)
		p.Get("/api/v1/certificates", d.Handlers.Certificate.List)
		p.Get("/api/v1/certificates/{id}/download", d.Handlers.Certificate.Download)
		p.Get("/api/v1/orders", d.Handlers.Payment.ListOrders)
		p.Post("/api/v1/orders", d.Handlers.Payment.CreateOrder)
		p.Get("/api/v1/orders/{id}", d.Handlers.Payment.GetOrder)
		p.Delete("/api/v1/orders/{id}", d.Handlers.Payment.CancelOrder)
		p.Post("/api/v1/orders/{id}/payment-intent", d.Handlers.Payment.CreateIntent)
		p.Get("/api/v1/payments", d.Handlers.Payment.ListPayments)
		p.Post("/api/v1/device-tokens", d.Handlers.Notification.Register)
		p.Delete("/api/v1/device-tokens", d.Handlers.Notification.Remove)
		p.Get("/api/v1/notifications", d.Handlers.Notification.List)
		p.Get("/api/v1/notifications/unread-count", d.Handlers.Notification.Unread)
		p.Post("/api/v1/notifications/{id}/read", d.Handlers.Notification.Read)
		p.Post("/api/v1/notifications/read-all", d.Handlers.Notification.ReadAll)
		p.With(d.Auth.RequireRoles("organization_admin")).Get("/api/v1/reports/overview", d.Handlers.Reporting.Overview)
		p.With(d.Auth.RequireRoles("organization_admin")).Get("/api/v1/reports/enrollments", d.Handlers.Reporting.Enrollments)
		p.With(d.Auth.RequireRoles("organization_admin")).Get("/api/v1/reports/completions", d.Handlers.Reporting.Completions)
		p.With(d.Auth.RequireRoles("organization_admin")).Get("/api/v1/reports/assessments", d.Handlers.Reporting.Assessments)
		p.With(d.Auth.RequireRoles("organization_admin")).Get("/api/v1/reports/live-attendance", d.Handlers.Reporting.LiveAttendance)
		p.With(d.Auth.RequireRoles("organization_admin")).Get("/api/v1/reports/revenue", d.Handlers.Reporting.Revenue)
	})
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { httpx.Problem(w, r, 404, "Not Found", "route not found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		httpx.Problem(w, r, 405, "Method Not Allowed", "method is not allowed for this route")
	})
	return otelhttp.NewHandler(r, "lms.http")
}
