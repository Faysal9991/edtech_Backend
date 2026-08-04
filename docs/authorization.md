# Authorization

## Roles

| Role | Scope | Core authority |
|---|---|---|
| `super_admin` | platform | organizations, all users, reports, payments, audit |
| `organization_admin` | one organization | membership, invitations, courses, reports, payments |
| `instructor` | assigned courses | content, assessments, live classes, students, grading |
| `student` | self and active enrollments | learning, attempts, submissions, results, payments, certificates |

Authorization is layered:

1. Firebase token identifies a local active user.
2. Organization middleware validates `X-Organization-ID` or an organization path ID against an active PostgreSQL membership.
3. Current membership roles authorize broad actions.
4. Resource checks compare the database resource organization and course assignment.
5. Ownership checks constrain progress, answers, submissions, orders, notifications, results, media, and certificates.

An arbitrary client organization UUID never grants access. Course and media lookups derive the organization from stored relationships. Instructor handlers verify `course_instructors`; students verify `enrollments`.

Common negative cases return `403` without leaking another tenant's content. Integration tests cover cross-organization membership, enrollment uniqueness, and webhook replay. Unit tests cover missing/invalid identity and answer-key leakage.

## Staff request example

```bash
curl "$PUBLIC_API_URL/api/v1/reports/overview?from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z" \
  -H "Authorization: Bearer $FIREBASE_ID_TOKEN" \
  -H "X-Organization-ID: $ORGANIZATION_ID"
```

## Auditing

Sensitive course updates store before/after JSON, actor, organization, resource, and request identity in `audit_logs`. Add audit calls for newly introduced administrative actions in the same transaction as the state change.
