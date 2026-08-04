-- name: CreateCategory :one
INSERT INTO course_categories (id, organization_id, name, slug, description) VALUES ($1,$2,$3,$4,$5) RETURNING *;

-- name: UpdateCategory :one
UPDATE course_categories SET name=$3, slug=$4, description=$5, updated_at=now()
WHERE id=$1 AND organization_id=$2 RETURNING *;

-- name: DeleteCategory :execrows
DELETE FROM course_categories WHERE id=$1 AND organization_id=$2;

-- name: ListCategories :many
SELECT * FROM course_categories WHERE organization_id=sqlc.arg(organization_id)
 AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (created_at,id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_size);

-- name: CreateCourse :one
INSERT INTO courses (id, organization_id, category_id, thumbnail_asset_id, title, slug, description, language, level, is_free, price_minor, currency, created_by)
VALUES (sqlc.arg(id),sqlc.arg(organization_id),sqlc.narg(category_id),sqlc.narg(thumbnail_asset_id),sqlc.arg(title),sqlc.arg(slug),sqlc.arg(description),sqlc.arg(language),sqlc.arg(level),sqlc.arg(is_free),sqlc.arg(price_minor),sqlc.arg(currency),sqlc.arg(created_by)) RETURNING *;

-- name: GetCourse :one
SELECT * FROM courses WHERE id=$1;

-- name: GetCourseByModule :one
SELECT c.* FROM courses c JOIN course_modules m ON m.course_id=c.id WHERE m.id=$1;

-- name: GetCourseByLesson :one
SELECT c.* FROM courses c JOIN course_modules m ON m.course_id=c.id JOIN lessons l ON l.module_id=m.id WHERE l.id=$1;

-- name: GetLesson :one
SELECT * FROM lessons WHERE id=$1;

-- name: GetCourseForUpdate :one
SELECT * FROM courses WHERE id=$1 FOR UPDATE;

-- name: UpdateCourse :one
UPDATE courses SET category_id=sqlc.narg(category_id), thumbnail_asset_id=sqlc.narg(thumbnail_asset_id), title=sqlc.arg(title), slug=sqlc.arg(slug), description=sqlc.arg(description),
 language=sqlc.arg(language), level=sqlc.arg(level), is_free=sqlc.arg(is_free), price_minor=sqlc.arg(price_minor), currency=sqlc.arg(currency),
 version=version+1, updated_at=now()
WHERE id=sqlc.arg(id) AND organization_id=sqlc.arg(organization_id) AND version=sqlc.arg(expected_version) RETURNING *;

-- name: ListPublishedCourses :many
SELECT c.*, cc.name AS category_name
FROM courses c LEFT JOIN course_categories cc ON cc.id=c.category_id
WHERE c.status='published'
  AND (sqlc.narg(organization_id)::uuid IS NULL OR c.organization_id=sqlc.narg(organization_id))
  AND (sqlc.arg(search)::text = '' OR c.search_vector @@ websearch_to_tsquery('simple', sqlc.arg(search)) OR c.title % sqlc.arg(search))
  AND (sqlc.narg(category_id)::uuid IS NULL OR c.category_id=sqlc.narg(category_id))
  AND (sqlc.narg(level)::text IS NULL OR c.level=sqlc.narg(level))
  AND (sqlc.narg(cursor_published_at)::timestamptz IS NULL OR (c.published_at, c.id) < (sqlc.narg(cursor_published_at), sqlc.narg(cursor_id)::uuid))
ORDER BY c.published_at DESC, c.id DESC LIMIT sqlc.arg(page_size);

-- name: ListManagedCourses :many
SELECT DISTINCT c.* FROM courses c
LEFT JOIN course_instructors ci ON ci.course_id=c.id
WHERE c.organization_id=sqlc.arg(organization_id)
  AND (sqlc.narg(instructor_id)::uuid IS NULL OR ci.instructor_id=sqlc.narg(instructor_id))
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL OR (c.created_at,c.id)<(sqlc.narg(cursor_created_at),sqlc.narg(cursor_id)::uuid))
ORDER BY c.created_at DESC,c.id DESC LIMIT sqlc.arg(page_size);

-- name: AssignCourseInstructor :exec
INSERT INTO course_instructors (course_id,instructor_id,assigned_by) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING;

-- name: RemoveCourseInstructor :execrows
DELETE FROM course_instructors WHERE course_id=$1 AND instructor_id=$2;

-- name: IsCourseInstructor :one
SELECT EXISTS(SELECT 1 FROM course_instructors WHERE course_id=$1 AND instructor_id=$2) AS assigned;

-- name: CountCourseInstructors :one
SELECT count(*) FROM course_instructors WHERE course_id=$1;

-- name: CreateModule :one
INSERT INTO course_modules (id,course_id,title,description,position) VALUES ($1,$2,$3,$4,$5) RETURNING *;

-- name: UpdateModule :one
UPDATE course_modules SET title=$2,description=$3,position=$4,updated_at=now() WHERE id=$1 RETURNING *;

-- name: DeleteModule :execrows
DELETE FROM course_modules WHERE id=$1;

-- name: CreateLesson :one
INSERT INTO lessons (id,module_id,title,description,lesson_type,media_asset_id,body,position,is_preview,is_required,is_published,duration_seconds)
VALUES (sqlc.arg(id),sqlc.arg(module_id),sqlc.arg(title),sqlc.arg(description),sqlc.arg(lesson_type),sqlc.narg(media_asset_id),sqlc.arg(body),sqlc.arg(position),sqlc.arg(is_preview),sqlc.arg(is_required),sqlc.arg(is_published),sqlc.narg(duration_seconds)) RETURNING *;

-- name: UpdateLesson :one
UPDATE lessons SET title=sqlc.arg(title),description=sqlc.arg(description),lesson_type=sqlc.arg(lesson_type),media_asset_id=sqlc.narg(media_asset_id),body=sqlc.arg(body),position=sqlc.arg(position),is_preview=sqlc.arg(is_preview),is_required=sqlc.arg(is_required),is_published=sqlc.arg(is_published),duration_seconds=sqlc.narg(duration_seconds),updated_at=now()
WHERE id=sqlc.arg(id) RETURNING *;

-- name: DeleteLesson :execrows
DELETE FROM lessons WHERE id=$1;

-- name: ListCourseContent :many
SELECT m.id AS module_id,m.title AS module_title,m.description AS module_description,m.position AS module_position,
 l.id AS lesson_id,l.title AS lesson_title,l.description AS lesson_description,l.lesson_type,l.media_asset_id,l.body,l.position AS lesson_position,l.is_preview,l.is_required,l.is_published,l.duration_seconds
FROM course_modules m LEFT JOIN lessons l ON l.module_id=m.id
WHERE m.course_id=$1 ORDER BY m.position,m.id,l.position,l.id;

-- name: CoursePublishFacts :one
SELECT c.id,c.title,c.description,c.is_free,c.price_minor,c.thumbnail_asset_id,
 (SELECT count(*) FROM course_instructors ci WHERE ci.course_id=c.id) AS instructor_count,
 (SELECT count(*) FROM course_modules m WHERE m.course_id=c.id) AS module_count,
 (SELECT count(*) FROM lessons l JOIN course_modules m ON m.id=l.module_id WHERE m.course_id=c.id AND l.is_published) AS published_lesson_count,
 ((SELECT count(*) FROM lessons l JOIN course_modules m ON m.id=l.module_id LEFT JOIN media_assets a ON a.id=l.media_asset_id WHERE m.course_id=c.id AND l.media_asset_id IS NOT NULL AND a.status <> 'ready')
  + (SELECT count(*) FROM media_assets a WHERE a.id=c.thumbnail_asset_id AND a.status <> 'ready')) AS unready_media_count
FROM courses c WHERE c.id=$1;

-- name: SetCourseStatus :one
UPDATE courses SET status=$2,published_at=CASE WHEN $2='published' THEN COALESCE(published_at,now()) ELSE published_at END,version=version+1,updated_at=now() WHERE id=$1 RETURNING *;
