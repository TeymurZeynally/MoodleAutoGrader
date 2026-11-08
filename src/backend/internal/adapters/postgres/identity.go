package postgres

import (
	"context"
	"encoding/json"
	"fmt"
)

// Schema identity: users and the audit log.

func (s *Store) UpsertUser(ctx context.Context, site string, moodleUserID int64, name string) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `insert into identity.users (moodle_site, moodle_user_id, full_name, last_login_at) values ($1, $2, $3, now())
		on conflict (moodle_site, moodle_user_id) do update set full_name = excluded.full_name, last_login_at = now() returning id`, site, moodleUserID, name).Scan(&id)
	return id, wrap("upsert user", err)
}

func (s *Store) UserName(ctx context.Context, userID int64) (string, error) {
	var name string
	err := s.db.QueryRow(ctx, `select full_name from identity.users where id = $1`, userID).Scan(&name)
	return name, wrap("user name", err)
}

func (s *Store) Audit(ctx context.Context, userID int64, action, entity string, entityID int64, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode audit payload: %w", err)
	}

	_, err = s.db.Exec(ctx, `insert into identity.audit_log (user_id, action, entity, entity_id, payload) values (nullif($1, 0), $2, $3, $4, $5)`, userID, action, entity, entityID, raw)
	return wrap("audit", err)
}
