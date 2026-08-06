package course

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	api "github.com/neoscoder/lms-service/internal/api"
	"github.com/neoscoder/lms-service/internal/data"
	"github.com/neoscoder/lms-service/internal/platform/database"
	"github.com/neoscoder/lms-service/internal/platform/httpx"
	platformid "github.com/neoscoder/lms-service/internal/platform/id"
)

var (
	ErrNotFound  = errors.New("course resource not found")
	ErrForbidden = errors.New("course access denied")
	ErrConflict  = errors.New("course was modified by another request")
)

type Service struct {
	db  database.Beginner
	q   *data.Queries
	ids platformid.Generator
}

func NewService(db database.Beginner, q *data.Queries, ids platformid.Generator) *Service {
	return &Service{db: db, q: q, ids: ids}
}

func validateWrite(in api.CourseWrite) error {
	if len(strings.TrimSpace(in.Title)) < 2 {
		return errors.New("title must contain at least two characters")
	}
	if strings.TrimSpace(in.Description) == "" {
		return errors.New("description is required")
	}
	switch in.Level {
	case "beginner", "intermediate", "advanced", "all":
	default:
		return errors.New("invalid course level")
	}
	if len(in.Currency) != 3 || in.Currency != strings.ToUpper(in.Currency) {
		return errors.New("currency must be a three-letter uppercase code")
	}
	if in.IsFree && in.PriceMinor != 0 {
		return errors.New("free courses must have a zero price")
	}
	if !in.IsFree && in.PriceMinor <= 0 {
		return errors.New("paid courses require a positive price")
	}
	return nil
}
func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: *id != uuid.Nil}
}
func asAPI(c data.Course) api.Course {
	result := api.Course{Id: c.ID, OrganizationId: c.OrganizationID, Title: c.Title, Slug: c.Slug, Description: c.Description, Language: c.Language, Level: api.CourseLevel(c.Level), Status: api.CourseStatus(c.Status), IsFree: c.IsFree, PriceMinor: c.PriceMinor, Currency: c.Currency, Version: c.Version}
	if c.ThumbnailAssetID.Valid {
		result.ThumbnailAssetId = &c.ThumbnailAssetID.UUID
	}
	return result
}

func (s *Service) validateThumbnail(ctx context.Context, orgID uuid.UUID, assetID *uuid.UUID) error {
	if assetID == nil {
		return nil
	}
	asset, err := s.q.GetMediaAsset(ctx, *assetID)
	if err != nil {
		return errors.New("thumbnail asset was not found")
	}
	if asset.OrganizationID != orgID || asset.Kind != "image" {
		return errors.New("thumbnail must be an image asset in this organization")
	}
	return nil
}

func (s *Service) Create(ctx context.Context, orgID, actorID uuid.UUID, in api.CourseWrite) (api.Course, error) {
	if err := validateWrite(in); err != nil {
		return api.Course{}, err
	}
	if err := s.validateThumbnail(ctx, orgID, in.ThumbnailAssetId); err != nil {
		return api.Course{}, err
	}
	var row data.Course
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var createErr error
		row, createErr = q.CreateCourse(ctx, data.CreateCourseParams{ID: s.ids.New(), OrganizationID: orgID, CategoryID: nullUUID(in.CategoryId), ThumbnailAssetID: nullUUID(in.ThumbnailAssetId), Title: strings.TrimSpace(in.Title), Slug: httpx.NormalizeSlug(in.Slug), Description: strings.TrimSpace(in.Description), Language: in.Language, Level: string(in.Level), IsFree: in.IsFree, PriceMinor: in.PriceMinor, Currency: in.Currency, CreatedBy: actorID})
		if createErr != nil {
			return createErr
		}
		isInstructor, createErr := q.HasOrganizationRole(ctx, data.HasOrganizationRoleParams{OrganizationID: orgID, UserID: actorID, RoleCode: "instructor"})
		if createErr != nil {
			return createErr
		}
		if isInstructor {
			return q.AssignCourseInstructor(ctx, data.AssignCourseInstructorParams{CourseID: row.ID, InstructorID: actorID, AssignedBy: actorID})
		}
		return nil
	})
	if err != nil {
		return api.Course{}, err
	}
	return asAPI(row), nil
}
func (s *Service) Get(ctx context.Context, id uuid.UUID) (data.Course, error) {
	row, err := s.q.GetCourse(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return data.Course{}, ErrNotFound
	}
	return row, err
}
func (s *Service) Update(ctx context.Context, id, orgID, actorID uuid.UUID, in api.CourseWrite) (api.Course, error) {
	if err := validateWrite(in); err != nil {
		return api.Course{}, err
	}
	if err := s.validateThumbnail(ctx, orgID, in.ThumbnailAssetId); err != nil {
		return api.Course{}, err
	}
	var updated data.Course
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.GetCourseForUpdate(ctx, id)
		if err != nil {
			return err
		}
		updated, err = q.UpdateCourse(ctx, data.UpdateCourseParams{CategoryID: nullUUID(in.CategoryId), ThumbnailAssetID: nullUUID(in.ThumbnailAssetId), Title: strings.TrimSpace(in.Title), Slug: httpx.NormalizeSlug(in.Slug), Description: strings.TrimSpace(in.Description), Language: in.Language, Level: string(in.Level), IsFree: in.IsFree, PriceMinor: in.PriceMinor, Currency: in.Currency, ID: id, OrganizationID: orgID, ExpectedVersion: in.Version})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(updated)
		return q.InsertAuditLog(ctx, data.InsertAuditLogParams{ID: s.ids.New(), OrganizationID: uuid.NullUUID{UUID: orgID, Valid: true}, ActorUserID: uuid.NullUUID{UUID: actorID, Valid: true}, Action: "course.updated", ResourceType: "course", ResourceID: uuid.NullUUID{UUID: id, Valid: true}, BeforeData: beforeJSON, AfterData: afterJSON})
	})
	if err != nil {
		return api.Course{}, err
	}
	return asAPI(updated), nil
}

func (s *Service) CanManage(ctx context.Context, courseID, userID, orgID uuid.UUID, roles []string) error {
	c, err := s.Get(ctx, courseID)
	if err != nil {
		return err
	}
	if c.OrganizationID != orgID {
		return ErrForbidden
	}
	for _, role := range roles {
		if role == "organization_admin" || role == "super_admin" {
			return nil
		}
	}
	assigned, err := s.q.IsCourseInstructor(ctx, data.IsCourseInstructorParams{CourseID: courseID, InstructorID: userID})
	if err != nil {
		return err
	}
	if !assigned {
		return ErrForbidden
	}
	return nil
}

func (s *Service) AssignInstructor(ctx context.Context, courseID, instructorID, actorID, orgID uuid.UUID) error {
	course, err := s.Get(ctx, courseID)
	if err != nil {
		return err
	}
	if course.OrganizationID != orgID {
		return ErrForbidden
	}
	allowed, err := s.q.HasOrganizationRole(ctx, data.HasOrganizationRoleParams{OrganizationID: orgID, UserID: instructorID, RoleCode: "instructor"})
	if err != nil {
		return err
	}
	if !allowed {
		return errors.New("user is not an active instructor in this organization")
	}
	return s.q.AssignCourseInstructor(ctx, data.AssignCourseInstructorParams{CourseID: courseID, InstructorID: instructorID, AssignedBy: actorID})
}

func (s *Service) SetStatus(ctx context.Context, courseID, actorID uuid.UUID, status string) (api.Course, error) {
	switch status {
	case "review", "published", "draft", "archived":
	default:
		return api.Course{}, errors.New("invalid course status")
	}
	var row data.Course
	err := database.WithinTx(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		before, err := q.GetCourseForUpdate(ctx, courseID)
		if err != nil {
			return err
		}
		if status == "published" {
			facts, factsErr := q.CoursePublishFacts(ctx, courseID)
			if factsErr != nil {
				return factsErr
			}
			if validationErr := validatePublishFacts(facts); validationErr != nil {
				return validationErr
			}
		}
		row, err = q.SetCourseStatus(ctx, data.SetCourseStatusParams{ID: courseID, Status: status})
		if err != nil {
			return err
		}
		if status == "published" {
			students, err := q.ListOrganizationStudentIDs(ctx, row.OrganizationID)
			if err != nil {
				return err
			}
			for _, userID := range students {
				payload, _ := json.Marshal(map[string]string{"user_id": userID.String(), "organization_id": row.OrganizationID.String(), "course_id": row.ID.String(), "title": row.Title})
				if err := q.InsertOutboxEvent(ctx, data.InsertOutboxEventParams{ID: s.ids.New(), AggregateType: "course", AggregateID: row.ID, EventType: "course.published", Payload: payload, DeduplicationKey: "course.published:" + row.ID.String() + ":" + userID.String()}); err != nil {
					return err
				}
			}
		}
		beforeJSON, _ := json.Marshal(before)
		afterJSON, _ := json.Marshal(row)
		return q.InsertAuditLog(ctx, data.InsertAuditLogParams{ID: s.ids.New(), OrganizationID: uuid.NullUUID{UUID: row.OrganizationID, Valid: true}, ActorUserID: uuid.NullUUID{UUID: actorID, Valid: true}, Action: "course.status." + status, ResourceType: "course", ResourceID: uuid.NullUUID{UUID: row.ID, Valid: true}, BeforeData: beforeJSON, AfterData: afterJSON})
	})
	if err != nil {
		return api.Course{}, err
	}
	return asAPI(row), nil
}

func validatePublishFacts(facts data.CoursePublishFactsRow) error {
	var violations []string
	if strings.TrimSpace(facts.Title) == "" {
		violations = append(violations, "title is required")
	}
	if strings.TrimSpace(facts.Description) == "" {
		violations = append(violations, "description is required")
	}
	if facts.InstructorCount < 1 {
		violations = append(violations, "at least one instructor is required")
	}
	if facts.ModuleCount < 1 {
		violations = append(violations, "at least one module is required")
	}
	if facts.PublishedLessonCount < 1 {
		violations = append(violations, "at least one published lesson is required")
	}
	if !facts.IsFree && facts.PriceMinor <= 0 {
		violations = append(violations, "paid course price must be positive")
	}
	if facts.UnreadyMediaCount > 0 {
		violations = append(violations, "all referenced media must be ready")
	}
	if len(violations) > 0 {
		return fmt.Errorf("course cannot be published: %s", strings.Join(violations, "; "))
	}
	return nil
}

func (s *Service) CreateModule(ctx context.Context, courseID uuid.UUID, in api.ModuleWrite) (data.CourseModule, error) {
	if in.Position < 1 || in.Position > math.MaxInt32 || strings.TrimSpace(in.Title) == "" {
		return data.CourseModule{}, errors.New("module title and positive position are required")
	}
	description := ""
	if in.Description != nil {
		description = *in.Description
	}
	return s.q.CreateModule(ctx, data.CreateModuleParams{ID: s.ids.New(), CourseID: courseID, Title: strings.TrimSpace(in.Title), Description: description, Position: int32(in.Position)}) // #nosec G115 -- range checked above
}
func (s *Service) UpdateModule(ctx context.Context, id uuid.UUID, in api.ModuleWrite) (data.CourseModule, error) {
	if in.Position < 1 || in.Position > math.MaxInt32 || strings.TrimSpace(in.Title) == "" {
		return data.CourseModule{}, errors.New("module title and positive position are required")
	}
	description := ""
	if in.Description != nil {
		description = *in.Description
	}
	return s.q.UpdateModule(ctx, data.UpdateModuleParams{ID: id, Title: strings.TrimSpace(in.Title), Description: description, Position: int32(in.Position)}) // #nosec G115 -- range checked above
}

func lessonParams(in api.LessonWrite) (string, string, string, uuid.NullUUID, string, int32, bool, bool, bool, pgtype.Int4, error) {
	if strings.TrimSpace(in.Title) == "" || in.Position < 1 || in.Position > math.MaxInt32 {
		return "", "", "", uuid.NullUUID{}, "", 0, false, false, false, pgtype.Int4{}, errors.New("lesson title and positive position are required")
	}
	description, body := "", ""
	if in.Description != nil {
		description = *in.Description
	}
	if in.Body != nil {
		body = *in.Body
	}
	duration := pgtype.Int4{}
	if in.DurationSeconds != nil {
		if *in.DurationSeconds < 0 || *in.DurationSeconds > math.MaxInt32 {
			return "", "", "", uuid.NullUUID{}, "", 0, false, false, false, pgtype.Int4{}, errors.New("lesson duration is outside the supported range")
		}
		duration = pgtype.Int4{Int32: int32(*in.DurationSeconds), Valid: true} // #nosec G115 -- range checked above
	}
	return strings.TrimSpace(in.Title), description, string(in.LessonType), nullUUID(in.MediaAssetId), body, int32(in.Position), in.IsPreview, in.IsRequired, in.IsPublished, duration, nil // #nosec G115 -- range checked above
}
func (s *Service) CreateLesson(ctx context.Context, moduleID uuid.UUID, in api.LessonWrite) (data.Lesson, error) {
	title, desc, kind, asset, body, pos, preview, required, published, duration, err := lessonParams(in)
	if err != nil {
		return data.Lesson{}, err
	}
	return s.q.CreateLesson(ctx, data.CreateLessonParams{ID: s.ids.New(), ModuleID: moduleID, Title: title, Description: desc, LessonType: kind, MediaAssetID: asset, Body: body, Position: pos, IsPreview: preview, IsRequired: required, IsPublished: published, DurationSeconds: duration})
}
func (s *Service) UpdateLesson(ctx context.Context, id uuid.UUID, in api.LessonWrite) (data.Lesson, error) {
	title, desc, kind, asset, body, pos, preview, required, published, duration, err := lessonParams(in)
	if err != nil {
		return data.Lesson{}, err
	}
	return s.q.UpdateLesson(ctx, data.UpdateLessonParams{ID: id, Title: title, Description: desc, LessonType: kind, MediaAssetID: asset, Body: body, Position: pos, IsPreview: preview, IsRequired: required, IsPublished: published, DurationSeconds: duration})
}

type CourseDetail struct {
	Course  api.Course     `json:"course"`
	Modules []ModuleDetail `json:"modules"`
}
type ModuleDetail struct {
	ID          uuid.UUID      `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	Position    int32          `json:"position"`
	Lessons     []LessonDetail `json:"lessons"`
}
type LessonDetail struct {
	ID          uuid.UUID `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	Position    int32     `json:"position"`
	Preview     bool      `json:"is_preview"`
	Required    bool      `json:"is_required"`
	Published   bool      `json:"is_published"`
	Duration    *int32    `json:"duration_seconds,omitempty"`
}

func (s *Service) Detail(ctx context.Context, id uuid.UUID, includeDraft bool) (CourseDetail, error) {
	course, err := s.Get(ctx, id)
	if err != nil {
		return CourseDetail{}, err
	}
	if course.Status != "published" && !includeDraft {
		return CourseDetail{}, ErrNotFound
	}
	rows, err := s.q.ListCourseContent(ctx, id)
	if err != nil {
		return CourseDetail{}, err
	}
	out := CourseDetail{Course: asAPI(course), Modules: []ModuleDetail{}}
	indexes := map[uuid.UUID]int{}
	for _, row := range rows {
		idx, ok := indexes[row.ModuleID]
		if !ok {
			idx = len(out.Modules)
			indexes[row.ModuleID] = idx
			out.Modules = append(out.Modules, ModuleDetail{ID: row.ModuleID, Title: row.ModuleTitle, Description: row.ModuleDescription, Position: row.ModulePosition, Lessons: []LessonDetail{}})
		}
		if !row.LessonID.Valid {
			continue
		}
		if !includeDraft && !row.IsPublished.Bool {
			continue
		}
		lesson := LessonDetail{ID: row.LessonID.UUID, Title: row.LessonTitle.String, Description: row.LessonDescription.String, Type: row.LessonType.String, Position: row.LessonPosition.Int32, Preview: row.IsPreview.Bool, Required: row.IsRequired.Bool, Published: row.IsPublished.Bool}
		if row.DurationSeconds.Valid {
			v := row.DurationSeconds.Int32
			lesson.Duration = &v
		}
		out.Modules[idx].Lessons = append(out.Modules[idx].Lessons, lesson)
	}
	return out, nil
}
