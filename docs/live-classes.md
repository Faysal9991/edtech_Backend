# Live classes

LiveKit is behind `LiveClassProvider`. The production adapter signs standard participant tokens; tests use a fake.

The backend generates every room name. Instructor publish grants require an active instructor assignment. Student grants disable publishing and require active enrollment. Clients never supply a room or grant set.

```mermaid
sequenceDiagram
  participant User
  participant API
  participant DB
  participant LK as LiveKit
  User->>API: POST live-session/{id}/join-token
  API->>DB: verify assignment or enrollment
  API-->>User: restricted signed token + URL + expiry
  User->>LK: connect
  LK->>API: signed participant_joined
  API->>DB: dedupe event + open interval
  LK->>API: signed participant_left
  API->>DB: dedupe event + lock/close latest interval
```

Reconnects create new attendance intervals. Leaves close only the latest open interval and calculate a non-negative duration. Reports sum intervals, so reconnects neither overwrite earlier attendance nor count disconnected time.

Verify production by creating a session, joining once as an assigned instructor and once as an enrolled student, and confirming signed webhook events appear uniquely in `live_webhook_events` with matching attendance intervals.
