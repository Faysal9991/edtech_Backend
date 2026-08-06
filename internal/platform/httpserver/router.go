package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	apispec "github.com/neoscoder/lms-service/api"
	"github.com/neoscoder/lms-service/internal/data"
	assignmentmodule "github.com/neoscoder/lms-service/internal/modules/assignment"
	certificatemodule "github.com/neoscoder/lms-service/internal/modules/certificate"
	coursemodule "github.com/neoscoder/lms-service/internal/modules/course"
	enrollmentmodule "github.com/neoscoder/lms-service/internal/modules/enrollment"
	identitymodule "github.com/neoscoder/lms-service/internal/modules/identity"
	liveclassmodule "github.com/neoscoder/lms-service/internal/modules/liveclass"
	mediamodule "github.com/neoscoder/lms-service/internal/modules/media"
	notificationmodule "github.com/neoscoder/lms-service/internal/modules/notification"
	paymentmodule "github.com/neoscoder/lms-service/internal/modules/payment"
	quizmodule "github.com/neoscoder/lms-service/internal/modules/quiz"
	reportingmodule "github.com/neoscoder/lms-service/internal/modules/reporting"
	usersmodule "github.com/neoscoder/lms-service/internal/modules/users"
	"github.com/neoscoder/lms-service/internal/platform/auth"
	"github.com/neoscoder/lms-service/internal/platform/cache"
	"github.com/neoscoder/lms-service/internal/platform/config"
	"github.com/neoscoder/lms-service/internal/platform/httpx"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type Handlers struct {
	Identity     *identitymodule.Handler
	Users        *usersmodule.Handler
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
	JWT      *auth.JWTManager
	Queries  *data.Queries
	Limiter  cache.Limiter
	DB       *pgxpool.Pool
	Redis    *redis.Client
	Handlers Handlers
}

func NewRouter(d Dependencies) http.Handler {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	engine.Use(ginLogger(d.Logger), globalLimit(d), gin.CustomRecovery(func(c *gin.Context, recovered any) {
		d.Logger.ErrorContext(c.Request.Context(), "panic recovered", "request_id", httpx.RequestID(c.Request.Context()), "error", recovered)
		httpx.Problem(c.Writer, c.Request, 500, "Internal Server Error", "the request could not be completed")
		c.Abort()
	}))

	engine.GET("/health/live", func(c *gin.Context) { c.JSON(200, gin.H{"status": "alive"}) })
	engine.GET("/health/ready", readiness(d))
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))
	engine.GET("/openapi.yaml", func(c *gin.Context) { c.Data(200, "application/yaml; charset=utf-8", apispec.OpenAPI) })
	engine.GET("/docs", swaggerUI)

	v1 := engine.Group("/api/v1")
	v1.POST("/auth/register", strictLimit(d, "register", 10, time.Hour), adapt(d.Handlers.Identity.Register))
	v1.POST("/auth/login", strictLimit(d, "login", 10, 15*time.Minute), adapt(d.Handlers.Identity.Login))
	v1.POST("/auth/refresh", strictLimit(d, "refresh", 30, time.Minute), adapt(d.Handlers.Identity.Refresh))
	v1.POST("/auth/forgot-password", strictLimit(d, "forgot", 5, time.Hour), adapt(d.Handlers.Identity.ForgotPassword))
	v1.POST("/auth/reset-password", strictLimit(d, "reset", 10, time.Hour), adapt(d.Handlers.Identity.ResetPassword))
	v1.POST("/auth/verify-email", strictLimit(d, "verify", 20, time.Hour), adapt(d.Handlers.Identity.VerifyEmail))

	v1.GET("/courses", adapt(d.Handlers.Course.List))
	v1.GET("/courses/:slug", adapt(d.Handlers.Course.PublicDetailBySlug))
	v1.GET("/certificates/verify/:certificateNumber", adaptNamed(d.Handlers.Certificate.Verify, map[string]string{"certificateNumber": "code"}))
	v1.GET("/public/certificates/verify/:code", adapt(d.Handlers.Certificate.Verify))
	v1.POST("/payments/webhooks/stripe", adapt(d.Handlers.Payment.Webhook))
	v1.POST("/webhooks/stripe", adapt(d.Handlers.Payment.Webhook))
	v1.POST("/webhooks/livekit", adapt(d.Handlers.LiveClass.Webhook))

	authenticated := v1.Group("")
	authenticated.Use(authenticate(d))
	authenticated.POST("/auth/logout", adapt(d.Handlers.Identity.Logout))
	authenticated.POST("/auth/logout-all", adapt(d.Handlers.Identity.LogoutAll))
	authenticated.GET("/auth/me", adapt(d.Handlers.Identity.Me))
	authenticated.POST("/auth/change-password", adapt(d.Handlers.Identity.ChangePassword))
	authenticated.PATCH("/users/me", adapt(d.Handlers.Users.UpdateMe))

	admin := authenticated.Group("/admin")
	admin.Use(requireRoles("admin"))
	admin.GET("/users", adapt(d.Handlers.Users.List))
	admin.GET("/users/:id", adapt(d.Handlers.Users.Get))
	admin.PATCH("/users/:id/status", adapt(d.Handlers.Users.SetStatus))
	admin.PUT("/users/:id/roles", adapt(d.Handlers.Users.ReplaceRoles))
	admin.POST("/courses/:id/publish", adapt(d.Handlers.Course.SetStatus("published")))
	admin.GET("/enrollments", adapt(d.Handlers.Enrollment.AdminList))
	admin.GET("/payments", adapt(d.Handlers.Payment.ListAdmin))
	admin.GET("/reports/overview", adapt(d.Handlers.Reporting.Overview))
	admin.GET("/reports/revenue", adapt(d.Handlers.Reporting.Revenue))

	teacher := authenticated.Group("/teacher")
	teacher.Use(requireRoles("teacher", "admin"))
	teacher.POST("/courses", adapt(d.Handlers.Course.Create))
	teacher.GET("/courses", adapt(d.Handlers.Course.ListManaged))
	teacher.GET("/courses/:id", adapt(d.Handlers.Course.Detail))
	teacher.PATCH("/courses/:id", adapt(d.Handlers.Course.Update))
	teacher.POST("/courses/:id/submit-review", adapt(d.Handlers.Course.SetStatus("review")))
	teacher.POST("/courses/:id/modules", adapt(d.Handlers.Course.CreateModule))
	teacher.PATCH("/modules/:id", adapt(d.Handlers.Course.UpdateModule))
	teacher.DELETE("/modules/:id", adapt(d.Handlers.Course.DeleteModule))
	teacher.POST("/modules/:id/lessons", adapt(d.Handlers.Course.CreateLesson))
	teacher.PATCH("/lessons/:id", adapt(d.Handlers.Course.UpdateLesson))
	teacher.DELETE("/lessons/:id", adapt(d.Handlers.Course.DeleteLesson))
	teacher.GET("/quizzes", adapt(d.Handlers.Quiz.List))
	teacher.POST("/quizzes", adapt(d.Handlers.Quiz.Create))
	teacher.GET("/quizzes/:id", adapt(d.Handlers.Quiz.Get))
	teacher.PATCH("/quizzes/:id", adapt(d.Handlers.Quiz.Update))
	teacher.DELETE("/quizzes/:id", adapt(d.Handlers.Quiz.Delete))
	teacher.POST("/quizzes/:id/questions", adapt(d.Handlers.Quiz.AddQuestion))
	teacher.PATCH("/quiz-questions/:id", adapt(d.Handlers.Quiz.UpdateQuestion))
	teacher.DELETE("/quiz-questions/:id", adapt(d.Handlers.Quiz.DeleteQuestion))
	teacher.GET("/assignments", adapt(d.Handlers.Assignment.List))
	teacher.POST("/assignments", adapt(d.Handlers.Assignment.Create))
	teacher.GET("/assignments/:id", adapt(d.Handlers.Assignment.Get))
	teacher.PATCH("/assignments/:id", adapt(d.Handlers.Assignment.Update))
	teacher.DELETE("/assignments/:id", adapt(d.Handlers.Assignment.Delete))
	teacher.PATCH("/submissions/:id/grade", adapt(d.Handlers.Assignment.Grade))
	teacher.GET("/live-classes", adapt(d.Handlers.LiveClass.List))
	teacher.POST("/live-classes", adapt(d.Handlers.LiveClass.Create))
	teacher.GET("/live-classes/:id", adapt(d.Handlers.LiveClass.Get))
	teacher.PATCH("/live-classes/:id", adapt(d.Handlers.LiveClass.Update))
	teacher.DELETE("/live-classes/:id", adapt(d.Handlers.LiveClass.SetStatus("cancelled")))
	teacher.POST("/live-classes/:id/start", adapt(d.Handlers.LiveClass.SetStatus("live")))
	teacher.POST("/live-classes/:id/end", adapt(d.Handlers.LiveClass.SetStatus("ended")))
	teacher.GET("/reports/overview", adapt(d.Handlers.Reporting.TeacherOverview))

	authenticated.POST("/media/upload-intents", adapt(d.Handlers.Media.CreateIntent))
	authenticated.POST("/media/:id/complete", adapt(d.Handlers.Media.Complete))
	authenticated.GET("/media/:id/access-url", adapt(d.Handlers.Media.AccessURL))
	authenticated.POST("/courses/:id/enroll", requireRoles("student"), adapt(d.Handlers.Enrollment.Enroll))
	student := authenticated.Group("/student")
	student.Use(requireRoles("student"))
	student.GET("/enrollments", adapt(d.Handlers.Enrollment.List))
	student.GET("/enrollments/:id", adapt(d.Handlers.Enrollment.Get))
	student.PUT("/enrollments/:id/lessons/:lessonId/progress", adapt(d.Handlers.Enrollment.UpdateProgress))
	student.POST("/quizzes/:id/attempts", adapt(d.Handlers.Quiz.Start))
	student.PUT("/quiz-attempts/:id/answers", adapt(d.Handlers.Quiz.SaveAnswer))
	student.POST("/quiz-attempts/:id/submit", adapt(d.Handlers.Quiz.Submit))
	student.GET("/quizzes/:id/attempts", adapt(d.Handlers.Quiz.Attempts))
	student.POST("/assignments/:id/submissions", adapt(d.Handlers.Assignment.CreateSubmission))
	student.POST("/submissions/:id/submit", adapt(d.Handlers.Assignment.Submit))
	authenticated.POST("/live-classes/:id/token", requireRoles("student", "teacher", "admin"), adapt(d.Handlers.LiveClass.JoinToken))
	authenticated.POST("/payments/orders", requireRoles("student"), adapt(d.Handlers.Payment.CreateOrder))
	authenticated.GET("/payments/orders/:id", requireRoles("student"), adapt(d.Handlers.Payment.GetOrder))
	authenticated.POST("/payments/orders/:id/payment-intent", requireRoles("student"), adapt(d.Handlers.Payment.CreateIntent))
	student.GET("/payments", adapt(d.Handlers.Payment.ListPayments))
	student.GET("/results", adapt(d.Handlers.Certificate.Results))
	student.GET("/certificates", adapt(d.Handlers.Certificate.List))
	student.GET("/certificates/:id/download", adapt(d.Handlers.Certificate.Download))
	authenticated.GET("/notifications", adapt(d.Handlers.Notification.List))
	authenticated.GET("/notifications/unread-count", adapt(d.Handlers.Notification.Unread))
	authenticated.PATCH("/notifications/:id/read", adapt(d.Handlers.Notification.Read))
	authenticated.POST("/devices", adapt(d.Handlers.Notification.Register))
	authenticated.DELETE("/devices/:token", adaptNamed(d.Handlers.Notification.Remove, map[string]string{"token": "token"}))

	// Compatibility aliases retain the original Phase-1 route surface while
	// clients migrate to the explicit student/teacher/admin namespaces.
	registerCompatibility(authenticated, d)

	engine.NoRoute(func(c *gin.Context) { httpx.Problem(c.Writer, c.Request, 404, "Not Found", "route not found") })
	engine.NoMethod(func(c *gin.Context) {
		httpx.Problem(c.Writer, c.Request, 405, "Method Not Allowed", "method is not allowed for this route")
	})

	var handler http.Handler = engine
	handler = httpx.Logging(d.Logger)(handler)
	handler = httpx.CORS(d.Config.HTTP.AllowedOrigins)(handler)
	handler = httpx.SecureHeaders(handler)
	handler = httpx.RequestIDMiddleware(handler)
	return otelhttp.NewHandler(handler, "lms.http")
}

func registerCompatibility(r *gin.RouterGroup, d Dependencies) {
	r.GET("/categories", adapt(d.Handlers.Course.ListCategories))
	// Gin requires a consistent wildcard name for the same method/path tree.
	// This legacy ID route shares the public :slug segment, then aliases it back
	// to the handler's historical parameter name.
	r.GET("/courses/:slug/modules", adaptNamed(d.Handlers.Course.ListContent, map[string]string{"slug": "id"}))
	r.GET("/lessons/:id", adapt(d.Handlers.Course.GetLesson))
	r.GET("/enrollments", adapt(d.Handlers.Enrollment.List))
	r.GET("/enrollments/:id", adapt(d.Handlers.Enrollment.Get))
	r.GET("/enrollments/:id/progress", adapt(d.Handlers.Enrollment.Progress))
	r.GET("/me/resume-learning", adapt(d.Handlers.Enrollment.Resume))
	r.GET("/quiz-attempts/:id", adapt(d.Handlers.Quiz.GetAttempt))
	r.GET("/assignment-submissions/:id", adapt(d.Handlers.Assignment.GetSubmission))
	r.PATCH("/assignment-submissions/:id", adapt(d.Handlers.Assignment.UpdateSubmission))
	r.GET("/orders", adapt(d.Handlers.Payment.ListOrders))
	r.GET("/orders/:id", adapt(d.Handlers.Payment.GetOrder))
	r.DELETE("/orders/:id", adapt(d.Handlers.Payment.CancelOrder))
	r.POST("/orders/:id/payment-intent", adapt(d.Handlers.Payment.CreateIntent))
	r.POST("/notifications/read-all", adapt(d.Handlers.Notification.ReadAll))
}

func authenticate(d Dependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			httpx.Problem(c.Writer, c.Request, 401, "Unauthorized", "a bearer access token is required")
			c.Abort()
			return
		}
		claims, err := d.JWT.ParseAccess(raw)
		if err != nil {
			httpx.Problem(c.Writer, c.Request, 401, "Unauthorized", "the access token is invalid or expired")
			c.Abort()
			return
		}
		userID, _ := uuid.Parse(claims.Subject)
		user, err := d.Queries.GetUser(c.Request.Context(), userID)
		if err != nil || user.Status != "active" {
			httpx.Problem(c.Writer, c.Request, 403, "Account Unavailable", "the account is not active")
			c.Abort()
			return
		}
		roles, err := d.Queries.ListGlobalRoleCodes(c.Request.Context(), userID)
		if err != nil {
			httpx.Problem(c.Writer, c.Request, 500, "Internal Server Error", "authorization lookup failed")
			c.Abort()
			return
		}
		principal := auth.Principal{UserID: user.ID, Email: user.Email, DisplayName: user.DisplayName, Roles: roles}
		ctx := auth.WithPrincipal(c.Request.Context(), principal)
		membership, found, err := resolveMembership(ctx, d.Queries, userID, c.GetHeader("X-Organization-ID"))
		if err != nil {
			httpx.Problem(c.Writer, c.Request, 400, "Invalid Organization", err.Error())
			c.Abort()
			return
		}
		if found {
			ctx = auth.WithMembership(ctx, membership)
		}
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func resolveMembership(ctx context.Context, q *data.Queries, userID uuid.UUID, rawOrganization string) (auth.Membership, bool, error) {
	if strings.TrimSpace(rawOrganization) != "" {
		organizationID, err := uuid.Parse(rawOrganization)
		if err != nil || organizationID == uuid.Nil {
			return auth.Membership{}, false, fmt.Errorf("X-Organization-ID must be a UUID")
		}
		row, err := q.GetActiveMembership(ctx, data.GetActiveMembershipParams{UserID: userID, OrganizationID: organizationID})
		if err != nil {
			return auth.Membership{}, false, errors.New("active organization membership is required")
		}
		return auth.Membership{ID: row.ID, OrganizationID: row.OrganizationID, Roles: row.Roles}, true, nil
	}
	rows, err := q.ListUserMemberships(ctx, userID)
	if err != nil {
		return auth.Membership{}, false, err
	}
	if len(rows) == 0 {
		return auth.Membership{}, false, nil
	}
	return auth.Membership{ID: rows[0].ID, OrganizationID: rows[0].OrganizationID, Roles: rows[0].Roles}, true, nil
}

func requireRoles(wanted ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, ok := auth.PrincipalFrom(c.Request.Context())
		if !ok || !auth.HasRole(principal.Roles, wanted...) {
			httpx.Problem(c.Writer, c.Request, 403, "Forbidden", "the required role is missing")
			c.Abort()
			return
		}
		c.Next()
	}
}

func adapt(handler http.HandlerFunc) gin.HandlerFunc { return adaptNamed(handler, nil) }
func adaptNamed(handler http.HandlerFunc, aliases map[string]string) gin.HandlerFunc {
	return func(c *gin.Context) {
		routeContext := chi.NewRouteContext()
		for _, param := range c.Params {
			name := param.Key
			if alias, ok := aliases[name]; ok {
				name = alias
			}
			routeContext.URLParams.Add(name, param.Value)
		}
		request := c.Request.WithContext(context.WithValue(c.Request.Context(), chi.RouteCtxKey, routeContext))
		handler(c.Writer, request)
	}
}

func readiness(d Dependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := d.DB.Ping(ctx); err != nil {
			c.JSON(503, gin.H{"status": "not_ready", "dependency": "postgres"})
			return
		}
		if err := d.Redis.Ping(ctx).Err(); err != nil {
			c.JSON(503, gin.H{"status": "not_ready", "dependency": "redis"})
			return
		}
		c.JSON(200, gin.H{"status": "ready"})
	}
}

func globalLimit(d Dependencies) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, err := d.Limiter.Allow(c.Request.Context(), "global:"+remoteIP(c.Request), d.Config.RateLimit.Requests, d.Config.RateLimit.Window)
		if err != nil {
			d.Logger.WarnContext(c.Request.Context(), "global rate limiter unavailable", "error", err)
			c.Next()
			return
		}
		if !allowed {
			c.Header("Retry-After", strconv.Itoa(max(1, int(d.Config.RateLimit.Window.Seconds()))))
			httpx.Problem(c.Writer, c.Request, 429, "Too Many Requests", "request limit exceeded")
			c.Abort()
			return
		}
		c.Next()
	}
}

func strictLimit(d Dependencies, scope string, limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, err := d.Limiter.Allow(c.Request.Context(), scope+":"+remoteIP(c.Request), limit, window)
		if err != nil {
			httpx.Problem(c.Writer, c.Request, 503, "Service Unavailable", "security rate limiter is unavailable")
			c.Abort()
			return
		}
		if !allowed {
			c.Header("Retry-After", strconv.Itoa(max(1, int(window.Seconds()))))
			httpx.Problem(c.Writer, c.Request, 429, "Too Many Requests", "request limit exceeded")
			c.Abort()
			return
		}
		c.Next()
	}
}

func ginLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		actor := ""
		if principal, ok := auth.PrincipalFrom(c.Request.Context()); ok {
			actor = principal.UserID.String()
		}
		logger.InfoContext(c.Request.Context(), "http route", "request_id", httpx.RequestID(c.Request.Context()), "actor_id", actor, "method", c.Request.Method, "route", c.FullPath(), "status", c.Writer.Status(), "duration_ms", time.Since(start).Milliseconds(), "error_code", firstError(c))
		httpx.ObserveMetrics(c.Request.Method, c.FullPath(), c.Writer.Status(), time.Since(start))
	}
}

func swaggerUI(c *gin.Context) {
	const page = `<!doctype html><html><head><meta charset="utf-8"><title>LMS Service API</title><link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"></head><body><div id="swagger-ui"></div><script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script><script>SwaggerUIBundle({url:'/openapi.yaml',dom_id:'#swagger-ui',deepLinking:true,persistAuthorization:true})</script></body></html>`
	c.Data(200, "text/html; charset=utf-8", []byte(page))
}

func bearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
		return "", false
	}
	return fields[1], fields[1] != ""
}
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func firstError(c *gin.Context) string {
	if len(c.Errors) == 0 {
		return ""
	}
	return c.Errors[0].Error()
}
