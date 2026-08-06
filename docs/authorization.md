# Authorization

## Roles

| API role | Organization role | Core authority |
|---|---|---|
| `admin` | `super_admin` or `organization_admin` | users, moderation, payments, reports, audit |
| `teacher` | `instructor` | owned courses, content, assessments, live classes, grading |
| `student` | `student` | own enrollments, progress, attempts, submissions, results, payments, certificates |

Authorization is layered:

1. The signed access token supplies a user subject, but PostgreSQL reloads the active account and current global roles on every request.
2. `X-Organization-ID`, when present, must resolve to an active membership; otherwise the user’s deterministic first active membership is used.
3. Global roles authorize `/admin` and `/teacher` namespaces.
4. Application services compare the resource’s stored organization and course ownership.
5. Ownership/enrollment checks constrain progress, answers, submissions, orders, notifications, results, media, certificates, and LiveKit grants.

An arbitrary organization UUID never grants access. A teacher must be present in `course_instructors`; a student must own an active enrollment. Correct quiz answers are added only to privileged teacher/admin views or permitted post-submission results.

```bash
curl "$PUBLIC_API_URL/api/v1/admin/reports/overview?from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z" \
  -H "Authorization: Bearer $ACCESS_TOKEN" \
  -H "X-Organization-ID: $ORGANIZATION_ID"
```

Important administrative and security changes write actor, action, resource, safe metadata, IP/user-agent where available, request identity, and timestamp. Passwords, tokens, provider secrets, and request bodies are excluded.
