package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"mobile-egress/internal/clientcontrol"
)

const standaloneEnrollmentSchema = `CREATE TABLE IF NOT EXISTS client_enrollments (
 id TEXT PRIMARY KEY,
 node_id TEXT NOT NULL,
 capability_hash BLOB NOT NULL CHECK(length(capability_hash) = 32),
 expires_at INTEGER NOT NULL,
 client_serial TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK(state IN ('invited','submitted','approved','delivered','acknowledged','canceled')),
 record BLOB NOT NULL CHECK(length(record) BETWEEN 1 AND 2097152)
) STRICT`

var errBootstrapConflict = errors.New("Client invitation already bound")
var errEnrollmentState = errors.New("invalid Client enrollment state")
var errGeneration = errors.New("invalid Client configuration generation")

func (state *store) migrateFromVersionThree(ctx context.Context) error {
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, standaloneEnrollmentSchema); err != nil {
		return err
	}
	if err = validSchemaFromQuery(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `PRAGMA user_version = 4`); err != nil {
		return err
	}
	return tx.Commit()
}

func countClientAdmissions(ctx context.Context, tx *sql.Tx, now time.Time) (int, error) {
	var count int
	err := tx.QueryRowContext(ctx, `SELECT
 (SELECT COUNT(*) FROM identities WHERE role = 'client' AND revoked_at IS NULL) +
 (SELECT COUNT(*) FROM client_enrollments WHERE client_serial = '' AND state IN ('invited','submitted') AND expires_at > ?)`, now.Unix()).Scan(&count)
	return count, err
}

func (state *store) createClientInvitation(ctx context.Context, value clientcontrol.Enrollment, capability string, now time.Time) (clientcontrol.Enrollment, error) {
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return value, err
	}
	defer tx.Rollback()
	var existingRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT e.record FROM client_enrollments e LEFT JOIN identities i ON e.client_serial = i.serial WHERE e.node_id = ? AND e.state <> 'canceled' AND ((e.client_serial = '' AND e.expires_at > ?) OR (i.serial IS NOT NULL AND i.revoked_at IS NULL))`, value.NodeID, now.Unix()).Scan(&existingRaw)
	if err == nil {
		var existing clientcontrol.Enrollment
		if err = json.Unmarshal(existingRaw, &existing); err != nil {
			return value, err
		}
		if existing.DisplayName != value.DisplayName {
			return value, errEnrollmentState
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return value, err
	}
	count, err := countClientAdmissions(ctx, tx, now)
	if err != nil {
		return value, err
	}
	if count >= maximumClientIdentities {
		return value, errIdentityLimit
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	hash := sha256.Sum256([]byte(capability))
	if _, err = tx.ExecContext(ctx, `INSERT INTO client_enrollments(id,node_id,capability_hash,expires_at,state,record) VALUES(?,?,?,?,?,?)`, value.ID, value.NodeID, hash[:], value.ExpiresAt.Unix(), value.State, raw); err != nil {
		return value, err
	}
	return value, tx.Commit()
}

func loadClientEnrollment(ctx context.Context, tx *sql.Tx, id string) (clientcontrol.Enrollment, []byte, error) {
	var result clientcontrol.Enrollment
	var raw, hash []byte
	err := tx.QueryRowContext(ctx, `SELECT record,capability_hash FROM client_enrollments WHERE id = ?`, id).Scan(&raw, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil, errCapabilityInvalid
	}
	if err != nil {
		return result, nil, err
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return result, nil, err
	}
	return result, hash, nil
}

func saveClientEnrollment(ctx context.Context, tx *sql.Tx, value clientcontrol.Enrollment) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE client_enrollments SET client_serial = ?, state = ?, record = ? WHERE id = ?`, value.ClientSerial, value.State, raw, value.ID)
	return err
}

// mutateClientEnrollment serializes the durable handoff, including certificate
// insertion, so a lost response can never create a second identity.
func (state *store) mutateClientEnrollment(ctx context.Context, id string, fn func(*sql.Tx, *clientcontrol.Enrollment, []byte) error) (clientcontrol.Enrollment, error) {
	tx, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return clientcontrol.Enrollment{}, err
	}
	defer tx.Rollback()
	value, hash, err := loadClientEnrollment(ctx, tx, id)
	if err != nil {
		return value, err
	}
	if err = fn(tx, &value, hash); err != nil {
		return clientcontrol.Enrollment{}, err
	}
	if err = saveClientEnrollment(ctx, tx, value); err != nil {
		return clientcontrol.Enrollment{}, err
	}
	if err = tx.Commit(); err != nil {
		return clientcontrol.Enrollment{}, err
	}
	return value, nil
}

func checkClientCapability(value clientcontrol.Enrollment, storedHash []byte, capability string, now time.Time) error {
	supplied := sha256.Sum256([]byte(capability))
	if subtle.ConstantTimeCompare(storedHash, supplied[:]) != 1 || value.State == "canceled" {
		return errCapabilityInvalid
	}
	if !now.Before(value.ExpiresAt) {
		return errCapabilityExpired
	}
	return nil
}

func activeClientEnrollment(ctx context.Context, tx *sql.Tx, value clientcontrol.Enrollment) error {
	if value.State == "canceled" {
		return errCapabilityInvalid
	}
	if value.ClientSerial == "" {
		return nil
	}
	var revoked sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT revoked_at FROM identities WHERE serial = ? AND role = 'client'`, value.ClientSerial).Scan(&revoked); err != nil {
		return errCapabilityInvalid
	}
	if revoked.Valid {
		return errCapabilityInvalid
	}
	return nil
}

func (state *store) clientEnrollments(ctx context.Context) ([]clientcontrol.Enrollment, error) {
	rows, err := state.db.QueryContext(ctx, `SELECT e.record FROM client_enrollments e LEFT JOIN identities i ON e.client_serial = i.serial WHERE e.state <> 'canceled' AND (e.client_serial = '' OR i.revoked_at IS NULL) ORDER BY (e.client_serial <> '') DESC, e.expires_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]clientcontrol.Enrollment, 0)
	for rows.Next() {
		var raw []byte
		var value clientcontrol.Enrollment
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		if value.ClientSerial == "" && !time.Now().Before(value.ExpiresAt) {
			value.State = "expired"
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (state *store) clientStatuses(ctx context.Context) ([]clientcontrol.Status, error) {
	rows, err := state.db.QueryContext(ctx, `SELECT i.serial,i.last_seen_at,e.record FROM identities i LEFT JOIN client_enrollments e ON e.client_serial = i.serial WHERE i.role = 'client' AND i.revoked_at IS NULL ORDER BY i.serial`)
	if err != nil {
		return nil, err
	}
	result := make([]clientcontrol.Status, 0)
	defer rows.Close()
	for rows.Next() {
		var value clientcontrol.Status
		var lastSeen sql.NullInt64
		var record []byte
		if err = rows.Scan(&value.ClientSerial, &lastSeen, &record); err != nil {
			return nil, err
		}
		if lastSeen.Valid {
			value.LastSeen = time.Unix(lastSeen.Int64, 0).UTC()
		}
		if len(record) > 0 {
			var enrollment clientcontrol.Enrollment
			if err = json.Unmarshal(record, &enrollment); err != nil {
				return nil, err
			}
			value.EnrollmentID = enrollment.ID
			value.NodeID = enrollment.NodeID
			value.AppliedGeneration = enrollment.AppliedGeneration
			value.ServiceVersion = enrollment.ServiceVersion
			if enrollment.Bootstrap != nil {
				value.Platform = enrollment.Bootstrap.Platform
				value.Architecture = enrollment.Bootstrap.Architecture
			}
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
