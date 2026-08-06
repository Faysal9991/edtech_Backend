package quiz

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	mathrand "math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	api "github.com/neoscoder/lms-service/internal/api"
	"github.com/neoscoder/lms-service/internal/data"
	"github.com/neoscoder/lms-service/internal/platform/clock"
	"github.com/neoscoder/lms-service/internal/platform/database"
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
	"github.com/neoscoder/lms-service/internal/platform/queue"
)

var (
	ErrNotFound     = errors.New("quiz not found")
	ErrForbidden    = errors.New("quiz access denied")
	ErrUnavailable  = errors.New("quiz is not currently available")
	ErrAttemptLimit = errors.New("quiz attempt limit reached")
)

type Service struct {
	db    database.Beginner
	q     *data.Queries
	ids   platformid.Generator
	clock clock.Clock
	jobs  queue.Client
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator, c clock.Clock, jobs queue.Client) *Service {
	return &Service{db: db, q: q, ids: ids, clock: c, jobs: jobs}
}

type Snapshot struct {
	Questions []SnapshotQuestion `json:"questions"`
}
type SnapshotQuestion struct {
	ID      uuid.UUID        `json:"id"`
	Type    string           `json:"type"`
	Prompt  string           `json:"prompt"`
	Points  float64          `json:"points"`
	Options []SnapshotOption `json:"options"`
}
type SnapshotOption struct {
	ID      uuid.UUID `json:"id"`
	Text    string    `json:"text"`
	Correct bool      `json:"correct"`
}
type PublicQuestion struct {
	ID      uuid.UUID      `json:"id"`
	Type    string         `json:"type"`
	Prompt  string         `json:"prompt"`
	Points  float64        `json:"points"`
	Options []PublicOption `json:"options"`
}
type PublicOption struct {
	ID      uuid.UUID `json:"id"`
	Text    string    `json:"text"`
	Correct *bool     `json:"correct,omitempty"`
}
type AttemptView struct {
	ID            uuid.UUID        `json:"id"`
	QuizID        uuid.UUID        `json:"quiz_id"`
	AttemptNumber int32            `json:"attempt_number"`
	Status        string           `json:"status"`
	StartedAt     time.Time        `json:"started_at"`
	ExpiresAt     *time.Time       `json:"expires_at,omitempty"`
	Questions     []PublicQuestion `json:"questions"`
	Percentage    *float64         `json:"percentage,omitempty"`
	Passed        *bool            `json:"passed,omitempty"`
}

func numeric(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%.2f", v))
	return n
}
func floatNumeric(v pgtype.Numeric) float64 {
	f, _ := v.Float64Value()
	if !f.Valid {
		return 0
	}
	return f.Float64
}
func nullableUUID(v *uuid.UUID) uuid.NullUUID {
	if v == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *v, Valid: *v != uuid.Nil}
}
func nullableInt(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true} // #nosec G115 -- callers validate the quiz time limit
}
func nullableTime(v *time.Time) pgtype.Timestamptz {
	if v == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: v.UTC(), Valid: true}
}
func text(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}

func (s *Service) Create(ctx context.Context, orgID uuid.UUID, in api.QuizWrite) (data.Quiz, error) {
	if err := validateQuizSettings(in); err != nil {
		return data.Quiz{}, err
	}
	return s.q.CreateQuiz(ctx, data.CreateQuizParams{ID: s.ids.New(), OrganizationID: orgID, CourseID: in.CourseId, LessonID: nullableUUID(in.LessonId), Title: strings.TrimSpace(in.Title), Instructions: value(in.Instructions), TimeLimitSeconds: nullableInt(in.TimeLimitSeconds), AttemptLimit: int32(in.AttemptLimit), PassPercentage: numeric(float64(in.PassPercentage)), RandomizeQuestions: valueBool(in.RandomizeQuestions), RandomizeOptions: valueBool(in.RandomizeOptions), AvailableFrom: nullableTime(in.AvailableFrom), AvailableUntil: nullableTime(in.AvailableUntil), ResultsVisibility: visibilityValue(in.ResultsVisibility), IsRequired: valueBool(in.IsRequired)}) // #nosec G115 -- attempt limit validated to 1..100
}

func validateQuizSettings(in api.QuizWrite) error {
	if strings.TrimSpace(in.Title) == "" || in.AttemptLimit < 1 || in.AttemptLimit > 100 || in.PassPercentage < 0 || in.PassPercentage > 100 {
		return errors.New("invalid quiz settings")
	}
	if in.TimeLimitSeconds != nil && (*in.TimeLimitSeconds < 1 || *in.TimeLimitSeconds > 24*60*60) {
		return errors.New("quiz time limit must be between 1 second and 24 hours")
	}
	if in.AvailableFrom != nil && in.AvailableUntil != nil && !in.AvailableUntil.After(*in.AvailableFrom) {
		return errors.New("available_until must be after available_from")
	}
	visibility := visibilityValue(in.ResultsVisibility)
	if visibility != "immediate" && visibility != "after_close" && visibility != "manual" {
		return errors.New("invalid result visibility")
	}
	if visibility == "after_close" && in.AvailableUntil == nil {
		return errors.New("after_close result visibility requires available_until")
	}
	return nil
}

func visibilityValue(value *api.QuizWriteResultsVisibility) string {
	if value == nil {
		return "immediate"
	}
	return string(*value)
}
func value(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
func valueBool(v *bool) bool { return v != nil && *v }

func (s *Service) AddQuestion(ctx context.Context, quizID uuid.UUID, in api.QuestionWrite) (data.QuizQuestion, error) {
	kind, err := validateQuestion(in)
	if err != nil {
		return data.QuizQuestion{}, err
	}
	var question data.QuizQuestion
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		quiz, err := q.GetQuiz(ctx, quizID)
		if err != nil {
			return err
		}
		if quiz.Status != "draft" {
			return errors.New("questions can only be changed while the quiz is draft")
		}
		question, err = q.CreateQuizQuestion(ctx, data.CreateQuizQuestionParams{ID: s.ids.New(), QuizID: quizID, QuestionType: kind, Prompt: strings.TrimSpace(in.Prompt), Points: numeric(float64(in.Points)), Position: int32(in.Position)}) // #nosec G115 -- question position is range checked
		if err != nil {
			return err
		}
		for _, option := range in.Options {
			if strings.TrimSpace(option.Text) == "" || option.Position < 1 || option.Position > math.MaxInt32 {
				return errors.New("invalid option")
			}
			if _, err = q.CreateQuizOption(ctx, data.CreateQuizOptionParams{ID: s.ids.New(), QuestionID: question.ID, Text: strings.TrimSpace(option.Text), IsCorrect: option.IsCorrect, Position: int32(option.Position)}); err != nil { // #nosec G115 -- option position is range checked
				return err
			}
		}
		return nil
	})
	return question, err
}

func validateQuestion(in api.QuestionWrite) (string, error) {
	if strings.TrimSpace(in.Prompt) == "" || in.Points <= 0 || in.Position < 1 || in.Position > math.MaxInt32 {
		return "", errors.New("invalid question")
	}
	kind := string(in.QuestionType)
	if kind != "single" && kind != "multiple" && kind != "true_false" && kind != "short_answer" {
		return "", errors.New("unsupported question type")
	}
	if kind == "short_answer" {
		if len(in.Options) != 0 {
			return "", errors.New("short-answer questions cannot have options")
		}
		return kind, nil
	}
	if len(in.Options) < 2 {
		return "", errors.New("objective questions need at least two options")
	}
	correct := 0
	for _, option := range in.Options {
		if strings.TrimSpace(option.Text) == "" || option.Position < 1 || option.Position > math.MaxInt32 {
			return "", errors.New("invalid option")
		}
		if option.IsCorrect {
			correct++
		}
	}
	if (kind == "single" || kind == "true_false") && correct != 1 {
		return "", errors.New("single-answer questions require exactly one correct option")
	}
	if kind == "multiple" && correct < 1 {
		return "", errors.New("multiple-answer questions require a correct option")
	}
	return kind, nil
}

func (s *Service) UpdateQuestion(ctx context.Context, questionID uuid.UUID, in api.QuestionWrite) (data.QuizQuestion, error) {
	kind, err := validateQuestion(in)
	if err != nil {
		return data.QuizQuestion{}, err
	}
	var updated data.QuizQuestion
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		existing, err := q.GetQuizQuestion(ctx, questionID)
		if err != nil {
			return err
		}
		quiz, err := q.GetQuiz(ctx, existing.QuizID)
		if err != nil {
			return err
		}
		if quiz.Status != "draft" {
			return errors.New("questions can only be changed while the quiz is draft")
		}
		updated, err = q.UpdateQuizQuestion(ctx, data.UpdateQuizQuestionParams{ID: questionID, QuestionType: kind, Prompt: strings.TrimSpace(in.Prompt), Points: numeric(float64(in.Points)), Position: int32(in.Position)}) // #nosec G115 -- question position is range checked
		if err != nil {
			return err
		}
		if err = q.DeleteQuizQuestionOptions(ctx, questionID); err != nil {
			return err
		}
		for _, option := range in.Options {
			if _, err = q.CreateQuizOption(ctx, data.CreateQuizOptionParams{ID: s.ids.New(), QuestionID: questionID, Text: strings.TrimSpace(option.Text), IsCorrect: option.IsCorrect, Position: int32(option.Position)}); err != nil { // #nosec G115 -- option position is range checked
				return err
			}
		}
		return nil
	})
	return updated, err
}

func (s *Service) DeleteQuestion(ctx context.Context, questionID uuid.UUID) error {
	deleted, err := s.q.DeleteQuizQuestion(ctx, questionID)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return errors.New("questions can only be deleted while the quiz is draft")
	}
	return nil
}

func (s *Service) Start(ctx context.Context, quizID, studentID uuid.UUID) (AttemptView, error) {
	quiz, err := s.q.GetQuiz(ctx, quizID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AttemptView{}, ErrNotFound
	}
	if err != nil {
		return AttemptView{}, err
	}
	now := s.clock.Now()
	if quiz.Status != "published" || (quiz.AvailableFrom.Valid && now.Before(quiz.AvailableFrom.Time)) || (quiz.AvailableUntil.Valid && !now.Before(quiz.AvailableUntil.Time)) {
		return AttemptView{}, ErrUnavailable
	}
	enrollment, err := s.q.GetCourseEnrollment(ctx, data.GetCourseEnrollmentParams{CourseID: quiz.CourseID, StudentID: studentID})
	if err != nil || enrollment.Status != "active" {
		return AttemptView{}, ErrForbidden
	}
	rows, err := s.q.ListQuizQuestionBank(ctx, quizID)
	if err != nil {
		return AttemptView{}, err
	}
	snapshot := Snapshot{Questions: []SnapshotQuestion{}}
	indexes := map[uuid.UUID]int{}
	for _, row := range rows {
		idx, ok := indexes[row.ID]
		if !ok {
			idx = len(snapshot.Questions)
			indexes[row.ID] = idx
			snapshot.Questions = append(snapshot.Questions, SnapshotQuestion{ID: row.ID, Type: row.QuestionType, Prompt: row.Prompt, Points: floatNumeric(row.Points), Options: []SnapshotOption{}})
		}
		if row.OptionID.Valid {
			snapshot.Questions[idx].Options = append(snapshot.Questions[idx].Options, SnapshotOption{ID: row.OptionID.UUID, Text: row.OptionText.String, Correct: row.IsCorrect.Bool})
		}
	}
	if len(snapshot.Questions) == 0 {
		return AttemptView{}, errors.New("quiz has no questions")
	}
	shuffleSnapshot(&snapshot, quiz.RandomizeQuestions, quiz.RandomizeOptions)
	payload, _ := json.Marshal(snapshot)
	var attempt data.QuizAttempt
	err = database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if _, err := q.ExpireStudentQuizAttempts(ctx, data.ExpireStudentQuizAttemptsParams{QuizID: quizID, StudentID: studentID, ExpiredAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
			return err
		}
		number, err := q.NextQuizAttemptNumber(ctx, data.NextQuizAttemptNumberParams{QuizID: quizID, StudentID: studentID})
		if err != nil {
			return err
		}
		if number > quiz.AttemptLimit {
			return ErrAttemptLimit
		}
		var expires pgtype.Timestamptz
		if quiz.TimeLimitSeconds.Valid {
			expires = pgtype.Timestamptz{Time: now.Add(time.Duration(quiz.TimeLimitSeconds.Int32) * time.Second), Valid: true}
		}
		max := 0.0
		order := make([]uuid.UUID, 0, len(snapshot.Questions))
		for _, q := range snapshot.Questions {
			max += q.Points
			order = append(order, q.ID)
		}
		attempt, err = q.CreateQuizAttempt(ctx, data.CreateQuizAttemptParams{ID: s.ids.New(), QuizID: quizID, EnrollmentID: enrollment.ID, StudentID: studentID, AttemptNumber: number, QuestionSnapshot: payload, QuestionOrder: order, StartedAt: pgtype.Timestamptz{Time: now, Valid: true}, ExpiresAt: expires, MaxPoints: numeric(max)})
		return err
	})
	if err != nil {
		return AttemptView{}, err
	}
	return view(attempt, snapshot, false, false), nil
}

func shuffleSnapshot(snapshot *Snapshot, questions, options bool) {
	var seed [16]byte
	_, _ = rand.Read(seed[:])
	rng := mathrand.New(mathrand.NewPCG(binary.LittleEndian.Uint64(seed[:8]), binary.LittleEndian.Uint64(seed[8:]))) // #nosec G404 -- crypto/rand-seeded PRNG is used only for question ordering
	if questions {
		rng.Shuffle(len(snapshot.Questions), func(i, j int) {
			snapshot.Questions[i], snapshot.Questions[j] = snapshot.Questions[j], snapshot.Questions[i]
		})
	}
	if options {
		for i := range snapshot.Questions {
			rng.Shuffle(len(snapshot.Questions[i].Options), func(a, b int) {
				snapshot.Questions[i].Options[a], snapshot.Questions[i].Options[b] = snapshot.Questions[i].Options[b], snapshot.Questions[i].Options[a]
			})
		}
	}
}

func (s *Service) SaveAnswer(ctx context.Context, attemptID, studentID uuid.UUID, in api.AnswerWrite) (data.QuizAnswer, error) {
	attempt, err := s.q.GetQuizAttempt(ctx, attemptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return data.QuizAnswer{}, ErrNotFound
	}
	if err != nil {
		return data.QuizAnswer{}, err
	}
	if attempt.StudentID != studentID {
		return data.QuizAnswer{}, ErrForbidden
	}
	if attempt.Status != "in_progress" || (attempt.ExpiresAt.Valid && !s.clock.Now().Before(attempt.ExpiresAt.Time)) {
		return data.QuizAnswer{}, errors.New("quiz attempt is no longer editable")
	}
	snapshot, err := decodeSnapshot(attempt.QuestionSnapshot)
	if err != nil {
		return data.QuizAnswer{}, err
	}
	var question *SnapshotQuestion
	for i := range snapshot.Questions {
		if snapshot.Questions[i].ID == in.QuestionId {
			question = &snapshot.Questions[i]
			break
		}
	}
	if question == nil {
		return data.QuizAnswer{}, errors.New("question is not part of this attempt")
	}
	selected := []uuid.UUID{}
	if in.SelectedOptionIds != nil {
		selected = append(selected, *in.SelectedOptionIds...)
	}
	allowed := map[uuid.UUID]bool{}
	for _, o := range question.Options {
		allowed[o.ID] = true
	}
	for _, id := range selected {
		if !allowed[id] {
			return data.QuizAnswer{}, errors.New("selected option is not part of question")
		}
	}
	if question.Type == "short_answer" && in.TextAnswer == nil {
		return data.QuizAnswer{}, errors.New("text answer is required")
	}
	return s.q.UpsertQuizAnswer(ctx, data.UpsertQuizAnswerParams{ID: s.ids.New(), AttemptID: attemptID, QuestionID: in.QuestionId, SelectedOptionIds: selected, TextAnswer: text(in.TextAnswer)})
}

func (s *Service) Submit(ctx context.Context, attemptID, studentID uuid.UUID) (AttemptView, error) {
	var attempt data.QuizAttempt
	var snapshot Snapshot
	var quiz data.Quiz
	var completedEnrollment uuid.UUID
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := q.GetQuizAttemptForUpdate(ctx, attemptID)
		if err != nil {
			return err
		}
		if locked.StudentID != studentID {
			return ErrForbidden
		}
		snapshot, err = decodeSnapshot(locked.QuestionSnapshot)
		if err != nil {
			return err
		}
		if locked.Status == "in_progress" && locked.ExpiresAt.Valid && !s.clock.Now().Before(locked.ExpiresAt.Time) {
			attempt, err = q.ExpireQuizAttempt(ctx, locked.ID)
			if err != nil {
				return err
			}
			quiz, err = q.GetQuiz(ctx, locked.QuizID)
			return err
		}
		if locked.Status != "in_progress" {
			attempt = locked
			quiz, err = q.GetQuiz(ctx, locked.QuizID)
			return err
		}
		answers, err := q.ListQuizAnswers(ctx, attemptID)
		if err != nil {
			return err
		}
		byQuestion := map[uuid.UUID]data.QuizAnswer{}
		for _, a := range answers {
			byQuestion[a.QuestionID] = a
		}
		score := 0.0
		manual := false
		for _, question := range snapshot.Questions {
			answer, ok := byQuestion[question.ID]
			if !ok {
				if question.Type == "short_answer" {
					manual = true
				}
				continue
			}
			if question.Type == "short_answer" {
				manual = true
				continue
			}
			correct := correctSelection(question, answer.SelectedOptionIds)
			points := 0.0
			if correct {
				points = question.Points
				score += points
			}
			_, err = q.GradeQuizAnswer(ctx, data.GradeQuizAnswerParams{AwardedPoints: numeric(points), IsCorrect: pgtype.Bool{Bool: correct, Valid: true}, GraderFeedback: pgtype.Text{}, ID: answer.ID})
			if err != nil {
				return err
			}
		}
		max := floatNumeric(locked.MaxPoints)
		percentage := 0.0
		if max > 0 {
			percentage = math.Round(score/max*10000) / 100
		}
		quiz, err = q.GetQuiz(ctx, locked.QuizID)
		if err != nil {
			return err
		}
		passed := percentage >= floatNumeric(quiz.PassPercentage)
		status := "graded"
		if manual {
			status = "submitted"
		}
		attempt, err = q.SubmitQuizAttempt(ctx, data.SubmitQuizAttemptParams{Status: status, ScorePoints: numeric(score), Percentage: numeric(percentage), Passed: pgtype.Bool{Bool: passed, Valid: !manual}, ID: attemptID})
		if err != nil {
			return err
		}
		if !manual {
			completedEnrollment = locked.EnrollmentID
			payload, _ := json.Marshal(map[string]string{"user_id": locked.StudentID.String(), "organization_id": quiz.OrganizationID.String(), "quiz_id": quiz.ID.String(), "attempt_id": locked.ID.String()})
			return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "quiz_attempt", AggregateID: locked.ID, EventType: "quiz.result", Payload: payload, DeduplicationKey: "quiz.result:" + locked.ID.String()})
		}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AttemptView{}, ErrNotFound
	}
	if err != nil {
		return AttemptView{}, err
	}
	if completedEnrollment != uuid.Nil {
		_ = s.jobs.Enqueue(queue.TypeCompletionEvaluate, map[string]string{"enrollment_id": completedEnrollment.String()})
	}
	return view(attempt, snapshot, false, canReveal(quiz, s.clock.Now())), nil
}

func (s *Service) GetAttempt(ctx context.Context, id, userID uuid.UUID, privileged bool) (AttemptView, error) {
	attempt, err := s.q.GetQuizAttempt(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return AttemptView{}, ErrNotFound
	}
	if err != nil {
		return AttemptView{}, err
	}
	if !privileged && attempt.StudentID != userID {
		return AttemptView{}, ErrForbidden
	}
	snapshot, err := decodeSnapshot(attempt.QuestionSnapshot)
	if err != nil {
		return AttemptView{}, err
	}
	revealResults := privileged
	if !privileged && attempt.Status != "in_progress" {
		quiz, err := s.q.GetQuiz(ctx, attempt.QuizID)
		if err != nil {
			return AttemptView{}, err
		}
		revealResults = canReveal(quiz, s.clock.Now())
	}
	return view(attempt, snapshot, privileged, revealResults), nil
}

func canReveal(quiz data.Quiz, now time.Time) bool {
	switch quiz.ResultsVisibility {
	case "immediate":
		return true
	case "after_close":
		return quiz.AvailableUntil.Valid && !now.Before(quiz.AvailableUntil.Time)
	default:
		return false
	}
}

func (s *Service) ManualGrade(ctx context.Context, attemptID, questionID, graderID uuid.UUID, points float64, feedback string) (AttemptView, error) {
	var attempt data.QuizAttempt
	var snapshot Snapshot
	var completedEnrollment uuid.UUID
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		locked, err := q.GetQuizAttemptForUpdate(ctx, attemptID)
		if err != nil {
			return err
		}
		if locked.Status != "submitted" && locked.Status != "graded" {
			return errors.New("attempt is not ready for manual grading")
		}
		snapshot, err = decodeSnapshot(locked.QuestionSnapshot)
		if err != nil {
			return err
		}
		maxQuestion := -1.0
		for _, question := range snapshot.Questions {
			if question.ID == questionID && question.Type == "short_answer" {
				maxQuestion = question.Points
			}
		}
		if maxQuestion < 0 || points < 0 || points > maxQuestion {
			return errors.New("manual points exceed question maximum")
		}
		answer, err := q.GetQuizAnswerByQuestion(ctx, data.GetQuizAnswerByQuestionParams{AttemptID: attemptID, QuestionID: questionID})
		if err != nil {
			return err
		}
		if _, err = q.GradeQuizAnswer(ctx, data.GradeQuizAnswerParams{AwardedPoints: numeric(points), IsCorrect: pgtype.Bool{Bool: points >= maxQuestion, Valid: true}, GraderFeedback: pgtype.Text{String: feedback, Valid: feedback != ""}, ID: answer.ID}); err != nil {
			return err
		}
		answers, err := q.ListQuizAnswers(ctx, attemptID)
		if err != nil {
			return err
		}
		score := 0.0
		allManual := true
		manualIDs := map[uuid.UUID]bool{}
		for _, question := range snapshot.Questions {
			if question.Type == "short_answer" {
				manualIDs[question.ID] = true
			}
		}
		for _, a := range answers {
			if a.AwardedPoints.Valid {
				score += floatNumeric(a.AwardedPoints)
			}
			if manualIDs[a.QuestionID] && !a.AwardedPoints.Valid {
				allManual = false
			}
		}
		for id := range manualIDs {
			found := false
			for _, a := range answers {
				if a.QuestionID == id {
					found = true
				}
			}
			if !found {
				allManual = false
			}
		}
		max := floatNumeric(locked.MaxPoints)
		percentage := 0.0
		if max > 0 {
			percentage = math.Round(score/max*10000) / 100
		}
		quiz, err := q.GetQuiz(ctx, locked.QuizID)
		if err != nil {
			return err
		}
		passed := percentage >= floatNumeric(quiz.PassPercentage)
		status := "submitted"
		if allManual {
			status = "graded"
		}
		attempt, err = q.SubmitQuizAttempt(ctx, data.SubmitQuizAttemptParams{Status: status, ScorePoints: numeric(score), Percentage: numeric(percentage), Passed: pgtype.Bool{Bool: passed, Valid: allManual}, ID: attemptID})
		_ = graderID
		if err != nil {
			return err
		}
		if allManual {
			completedEnrollment = locked.EnrollmentID
			payload, _ := json.Marshal(map[string]string{"user_id": locked.StudentID.String(), "organization_id": quiz.OrganizationID.String(), "quiz_id": quiz.ID.String(), "attempt_id": locked.ID.String()})
			return q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "quiz_attempt", AggregateID: locked.ID, EventType: "quiz.result", Payload: payload, DeduplicationKey: "quiz.result:" + locked.ID.String()})
		}
		return nil
	})
	if err != nil {
		return AttemptView{}, err
	}
	if completedEnrollment != uuid.Nil {
		_ = s.jobs.Enqueue(queue.TypeCompletionEvaluate, map[string]string{"enrollment_id": completedEnrollment.String()})
	}
	return view(attempt, snapshot, true, true), nil
}

func correctSelection(q SnapshotQuestion, selected []uuid.UUID) bool {
	expected := []string{}
	actual := []string{}
	for _, o := range q.Options {
		if o.Correct {
			expected = append(expected, o.ID.String())
		}
	}
	for _, id := range selected {
		actual = append(actual, id.String())
	}
	sort.Strings(expected)
	sort.Strings(actual)
	if len(expected) != len(actual) {
		return false
	}
	for i := range expected {
		if expected[i] != actual[i] {
			return false
		}
	}
	return true
}
func decodeSnapshot(raw []byte) (Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return Snapshot{}, fmt.Errorf("decode attempt snapshot: %w", err)
	}
	return s, nil
}
func view(a data.QuizAttempt, s Snapshot, revealAnswers, revealResults bool) AttemptView {
	out := AttemptView{ID: a.ID, QuizID: a.QuizID, AttemptNumber: a.AttemptNumber, Status: a.Status, Questions: []PublicQuestion{}}
	if a.StartedAt.Valid {
		out.StartedAt = a.StartedAt.Time
	}
	if a.ExpiresAt.Valid {
		v := a.ExpiresAt.Time
		out.ExpiresAt = &v
	}
	for _, q := range s.Questions {
		pq := PublicQuestion{ID: q.ID, Type: q.Type, Prompt: q.Prompt, Points: q.Points, Options: []PublicOption{}}
		for _, o := range q.Options {
			po := PublicOption{ID: o.ID, Text: o.Text}
			if revealAnswers {
				v := o.Correct
				po.Correct = &v
			}
			pq.Options = append(pq.Options, po)
		}
		out.Questions = append(out.Questions, pq)
	}
	if revealResults && a.Percentage.Valid {
		v := floatNumeric(a.Percentage)
		out.Percentage = &v
	}
	if revealResults && a.Passed.Valid {
		v := a.Passed.Bool
		out.Passed = &v
	}
	return out
}
