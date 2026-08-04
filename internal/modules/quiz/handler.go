package quiz

import (
	"errors"
	"net/http"
	"strings"

	api "github.com/Faysal9991/edtech_Backend/internal/api"
	"github.com/Faysal9991/edtech_Backend/internal/data"
	"github.com/Faysal9991/edtech_Backend/internal/platform/auth"
	"github.com/Faysal9991/edtech_Backend/internal/platform/httpx"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Handler struct {
	s *Service
	q *data.Queries
}

func NewHandler(s *Service, q *data.Queries) *Handler { return &Handler{s: s, q: q} }
func fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.Problem(w, r, 404, "Not Found", err.Error())
	case errors.Is(err, ErrForbidden):
		httpx.Problem(w, r, 403, "Forbidden", err.Error())
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrAttemptLimit):
		httpx.Problem(w, r, 409, "Quiz Unavailable", err.Error())
	default:
		httpx.Problem(w, r, 422, "Quiz Request Failed", err.Error())
	}
}
func quizDTO(q data.Quiz) map[string]any {
	return map[string]any{"id": q.ID, "organization_id": q.OrganizationID, "course_id": q.CourseID, "lesson_id": nullable(q.LessonID), "title": q.Title, "instructions": q.Instructions, "status": q.Status, "time_limit_seconds": intValue(q.TimeLimitSeconds), "attempt_limit": q.AttemptLimit, "pass_percentage": decimal(q.PassPercentage), "randomize_questions": q.RandomizeQuestions, "randomize_options": q.RandomizeOptions, "available_from": timeValue(q.AvailableFrom), "available_until": timeValue(q.AvailableUntil), "results_visibility": q.ResultsVisibility, "is_required": q.IsRequired}
}
func decimal(v pgtype.Numeric) float64 { return floatNumeric(v) }
func nullable(v uuid.NullUUID) any {
	if v.Valid {
		return v.UUID
	}
	return nil
}
func intValue(v pgtype.Int4) any {
	if v.Valid {
		return v.Int32
	}
	return nil
}
func timeValue(v pgtype.Timestamptz) any {
	if v.Valid {
		return v.Time
	}
	return nil
}
func (h *Handler) manager(r *http.Request, courseID uuid.UUID) bool {
	p, _ := auth.PrincipalFrom(r.Context())
	course, err := h.q.GetCourse(r.Context(), courseID)
	if err != nil {
		return false
	}
	m, ok := auth.MembershipFrom(r.Context())
	if !ok {
		row, e := h.q.GetActiveMembership(r.Context(), data.GetActiveMembershipParams{UserID: p.UserID, OrganizationID: course.OrganizationID})
		if e != nil {
			return false
		}
		m = auth.Membership{ID: row.ID, OrganizationID: row.OrganizationID, Roles: row.Roles}
	}
	if course.OrganizationID != m.OrganizationID {
		return false
	}
	if auth.HasRole(m.Roles, "organization_admin", "super_admin") {
		return true
	}
	if auth.HasRole(m.Roles, "instructor") {
		allowed, _ := h.q.IsCourseInstructor(r.Context(), data.IsCourseInstructorParams{CourseID: courseID, InstructorID: p.UserID})
		return allowed
	}
	return false
}
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	var in api.QuizWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	if !h.manager(r, in.CourseId) {
		httpx.Problem(w, r, 403, "Forbidden", "assigned course access is required")
		return
	}
	course, err := h.q.GetCourse(r.Context(), in.CourseId)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	row, err := h.s.Create(r.Context(), course.OrganizationID, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 201, quizDTO(row))
}
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	courseID, err := httpx.UUIDParam(r.URL.Query().Get("course_id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Filter", "course_id is required")
		return
	}
	p, _ := auth.PrincipalFrom(r.Context())
	manager := h.manager(r, courseID)
	allowed := manager
	if !allowed {
		enrollment, e := h.q.GetCourseEnrollment(r.Context(), data.GetCourseEnrollmentParams{CourseID: courseID, StudentID: p.UserID})
		allowed = e == nil && (enrollment.Status == "active" || enrollment.Status == "completed")
	}
	if !allowed {
		httpx.Problem(w, r, 403, "Forbidden", "course access is required")
		return
	}
	size, err := httpx.PageSize(r)
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	cursor, err := httpx.ParseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	params := data.ListCourseQuizzesParams{CourseID: courseID, IncludeUnpublished: manager, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListCourseQuizzes(r.Context(), params)
	if err != nil {
		httpx.Problem(w, r, 500, "Internal Server Error", "unable to list quizzes")
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, quizDTO(row))
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	quiz, err := h.q.GetQuiz(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, r, ErrNotFound)
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	manager := h.manager(r, quiz.CourseID)
	p, _ := auth.PrincipalFrom(r.Context())
	if !manager {
		enrollment, e := h.q.GetCourseEnrollment(r.Context(), data.GetCourseEnrollmentParams{CourseID: quiz.CourseID, StudentID: p.UserID})
		if e != nil || (enrollment.Status != "active" && enrollment.Status != "completed") || quiz.Status != "published" {
			fail(w, r, ErrForbidden)
			return
		}
	}
	rows, err := h.q.ListQuizQuestionBank(r.Context(), id)
	if err != nil {
		fail(w, r, err)
		return
	}
	questions := []any{}
	indexes := map[uuid.UUID]int{}
	for _, row := range rows {
		idx, ok := indexes[row.ID]
		if !ok {
			idx = len(questions)
			indexes[row.ID] = idx
			questions = append(questions, map[string]any{"id": row.ID, "type": row.QuestionType, "prompt": row.Prompt, "points": decimal(row.Points), "options": []any{}})
		}
		if row.OptionID.Valid {
			qmap := questions[idx].(map[string]any)
			option := map[string]any{"id": row.OptionID.UUID, "text": row.OptionText.String}
			if manager {
				option["is_correct"] = row.IsCorrect.Bool
			}
			qmap["options"] = append(qmap["options"].([]any), option)
		}
	}
	httpx.JSON(w, 200, map[string]any{"quiz": quizDTO(quiz), "questions": questions})
}
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	existing, err := h.q.GetQuiz(r.Context(), id)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	if !h.manager(r, existing.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	var in api.QuizWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	if err := validateQuizSettings(in); err != nil {
		fail(w, r, err)
		return
	}
	row, err := h.q.UpdateQuiz(r.Context(), data.UpdateQuizParams{Title: strings.TrimSpace(in.Title), Instructions: value(in.Instructions), TimeLimitSeconds: nullableInt(in.TimeLimitSeconds), AttemptLimit: int32(in.AttemptLimit), PassPercentage: numeric(float64(in.PassPercentage)), RandomizeQuestions: valueBool(in.RandomizeQuestions), RandomizeOptions: valueBool(in.RandomizeOptions), AvailableFrom: nullableTime(in.AvailableFrom), AvailableUntil: nullableTime(in.AvailableUntil), ResultsVisibility: visibilityValue(in.ResultsVisibility), IsRequired: valueBool(in.IsRequired), ID: id})
	if err != nil {
		fail(w, r, err)
		return
	}
	if status := r.URL.Query().Get("status"); status != "" {
		if status != "draft" && status != "published" && status != "archived" {
			httpx.Problem(w, r, 422, "Invalid Status", "status must be draft, published, or archived")
			return
		}
		row, err = h.q.SetQuizStatus(r.Context(), data.SetQuizStatusParams{ID: id, Status: status})
		if err != nil {
			fail(w, r, err)
			return
		}
	}
	httpx.JSON(w, 200, quizDTO(row))
}
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	q, err := h.q.GetQuiz(r.Context(), id)
	if err != nil || !h.manager(r, q.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	n, err := h.q.DeleteQuiz(r.Context(), id)
	if err != nil || n == 0 {
		httpx.Problem(w, r, 409, "Delete Failed", "only a draft quiz may be deleted")
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) AddQuestion(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	quiz, err := h.q.GetQuiz(r.Context(), id)
	if err != nil || !h.manager(r, quiz.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	var in api.QuestionWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.AddQuestion(r.Context(), id, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 201, map[string]any{"id": row.ID, "quiz_id": row.QuizID, "type": row.QuestionType, "prompt": row.Prompt, "points": decimal(row.Points), "position": row.Position})
}

func (h *Handler) UpdateQuestion(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	question, err := h.q.GetQuizQuestion(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, r, ErrNotFound)
		return
	}
	quiz, err := h.q.GetQuiz(r.Context(), question.QuizID)
	if err != nil || !h.manager(r, quiz.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	var in api.QuestionWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.UpdateQuestion(r.Context(), id, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"id": row.ID, "quiz_id": row.QuizID, "type": row.QuestionType, "prompt": row.Prompt, "points": decimal(row.Points), "position": row.Position})
}

func (h *Handler) DeleteQuestion(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	question, err := h.q.GetQuizQuestion(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, r, ErrNotFound)
		return
	}
	quiz, err := h.q.GetQuiz(r.Context(), question.QuizID)
	if err != nil || !h.manager(r, quiz.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	if err := h.s.DeleteQuestion(r.Context(), id); err != nil {
		fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	result, err := h.s.Start(r.Context(), id, p.UserID)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 201, result)
}
func (h *Handler) Attempts(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	quiz, err := h.q.GetQuiz(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, r, ErrNotFound)
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	size, err := httpx.PageSize(r)
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	cursor, err := httpx.ParseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid Pagination", err.Error())
		return
	}
	params := data.ListQuizAttemptsParams{QuizID: id, StudentID: p.UserID, PageSize: size}
	if cursor != nil {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.Time, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: cursor.ID, Valid: true}
	}
	rows, err := h.q.ListQuizAttempts(r.Context(), params)
	if err != nil {
		fail(w, r, err)
		return
	}
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		snapshot, err := decodeSnapshot(row.QuestionSnapshot)
		if err != nil {
			fail(w, r, err)
			return
		}
		reveal := row.Status != "in_progress" && canReveal(quiz, h.s.clock.Now())
		items = append(items, view(row, snapshot, reveal))
	}
	response := map[string]any{"items": items}
	if len(rows) == int(size) {
		last := rows[len(rows)-1]
		response["next_cursor"] = httpx.EncodeCursor(last.CreatedAt.Time, last.ID)
	}
	httpx.JSON(w, 200, response)
}

func (h *Handler) GetAttempt(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	attempt, err := h.q.GetQuizAttempt(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		fail(w, r, ErrNotFound)
		return
	}
	privileged := false
	if attempt.StudentID != p.UserID {
		quiz, loadErr := h.q.GetQuiz(r.Context(), attempt.QuizID)
		privileged = loadErr == nil && h.manager(r, quiz.CourseID)
		if !privileged {
			fail(w, r, ErrForbidden)
			return
		}
	}
	result, err := h.s.GetAttempt(r.Context(), id, p.UserID, privileged)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) SaveAnswer(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	var in api.AnswerWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	row, err := h.s.SaveAnswer(r.Context(), id, p.UserID, in)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, map[string]any{"id": row.ID, "question_id": row.QuestionID, "saved": true})
}
func (h *Handler) Submit(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.PrincipalFrom(r.Context())
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	result, err := h.s.Submit(r.Context(), id, p.UserID)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, result)
}
func (h *Handler) Grade(w http.ResponseWriter, r *http.Request) {
	id, err := httpx.UUIDParam(chi.URLParam(r, "id"))
	if err != nil {
		httpx.Problem(w, r, 400, "Invalid ID", err.Error())
		return
	}
	attempt, err := h.q.GetQuizAttempt(r.Context(), id)
	if err != nil {
		fail(w, r, ErrNotFound)
		return
	}
	quiz, err := h.q.GetQuiz(r.Context(), attempt.QuizID)
	if err != nil || !h.manager(r, quiz.CourseID) {
		fail(w, r, ErrForbidden)
		return
	}
	var in api.GradeWrite
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		httpx.Problem(w, r, 400, "Invalid Request", err.Error())
		return
	}
	if in.QuestionId == nil {
		httpx.Problem(w, r, 422, "Question Required", "question_id is required for manual quiz grading")
		return
	}
	feedback := ""
	if in.Feedback != nil {
		feedback = *in.Feedback
	}
	p, _ := auth.PrincipalFrom(r.Context())
	result, err := h.s.ManualGrade(r.Context(), id, *in.QuestionId, p.UserID, float64(in.Points), feedback)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpx.JSON(w, 200, result)
}
