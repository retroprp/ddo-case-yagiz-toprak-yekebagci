package middleware

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func RecordAudit(ctx context.Context, pool *pgxpool.Pool, userID *int, action, entityType, entityID string, oldData, newData any) {
	var oldJSON, newJSON []byte
	if oldData != nil {
		oldJSON, _ = json.Marshal(oldData)
	}
	if newData != nil {
		newJSON, _ = json.Marshal(newData)
	}

	_, _ = pool.Exec(ctx,
		`INSERT INTO audit_logs (user_id, action, entity_type, entity_id, old_data, new_data)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, action, entityType, entityID, oldJSON, newJSON,
	)
}

func RecordAuditTx(ctx context.Context, tx pgx.Tx, userID *int, action, entityType, entityID string, oldData, newData any) error {
	var oldJSON, newJSON []byte
	if oldData != nil {
		oldJSON, _ = json.Marshal(oldData)
	}
	if newData != nil {
		newJSON, _ = json.Marshal(newData)
	}

	_, err := tx.Exec(ctx,
		`INSERT INTO audit_logs (user_id, action, entity_type, entity_id, old_data, new_data)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, action, entityType, entityID, oldJSON, newJSON,
	)
	return err
}
