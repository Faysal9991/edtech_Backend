package quiz

import (
	"testing"
	"time"

	"github.com/Faysal9991/edtech_Backend/internal/data"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestDelayedResultVisibility(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	quiz := data.Quiz{ResultsVisibility: "after_close", AvailableUntil: pgtype.Timestamptz{Time: now.Add(time.Minute), Valid: true}}
	if canReveal(quiz, now) {
		t.Fatal("result was revealed before quiz availability closed")
	}
	if !canReveal(quiz, now.Add(time.Minute)) {
		t.Fatal("result remained hidden after quiz availability closed")
	}
	quiz.ResultsVisibility = "manual"
	if canReveal(quiz, now.Add(time.Hour)) {
		t.Fatal("manual results must remain hidden from students")
	}
}

func TestCorrectSelection(t *testing.T) {
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	q := SnapshotQuestion{Options: []SnapshotOption{{ID: a, Correct: true}, {ID: b, Correct: true}, {ID: c}}}
	if !correctSelection(q, []uuid.UUID{b, a}) {
		t.Fatal("order-independent exact answer should pass")
	}
	if correctSelection(q, []uuid.UUID{a}) {
		t.Fatal("partial answer must fail")
	}
	if correctSelection(q, []uuid.UUID{a, b, c}) {
		t.Fatal("extra option must fail")
	}
}
func TestViewDoesNotLeakAnswers(t *testing.T) {
	id := uuid.New()
	snapshot := Snapshot{Questions: []SnapshotQuestion{{ID: id, Options: []SnapshotOption{{ID: uuid.New(), Correct: true}}}}}
	attempt := data.QuizAttempt{ID: id, QuizID: id, StartedAt: pgtype.Timestamptz{Valid: true}, Percentage: numeric(100), Passed: pgtype.Bool{Bool: true, Valid: true}}
	v := view(attempt, snapshot, false)
	if v.Questions[0].Options[0].Correct != nil {
		t.Fatal("correct answer leaked before submission")
	}
	if v.Percentage != nil || v.Passed != nil {
		t.Fatal("score leaked before the result visibility policy allowed release")
	}
}
