package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"mobile-egress/relay/internal/enrollment"
	_ "modernc.org/sqlite"
)

const schemaVersion = 3

const adminMutationReplaySchema = `CREATE TABLE IF NOT EXISTS admin_mutation_replay (
    request_id TEXT PRIMARY KEY,
    digest BLOB NOT NULL CHECK(length(digest) = 32),
    operation TEXT NOT NULL CHECK(operation IN ('setup','rotate','repair')),
    state TEXT NOT NULL CHECK(state IN ('reserved','executing','completed','indeterminate')),
    response BLOB,
    created_at INTEGER NOT NULL,
    CHECK(length(request_id) = 32
          AND request_id = lower(request_id)
          AND request_id NOT GLOB '*[^0-9a-f]*'),
    CHECK((state = 'completed' AND response IS NOT NULL
           AND length(response) BETWEEN 1 AND 524288)
          OR (state <> 'completed' AND response IS NULL))
) STRICT`

var (
	errCapabilityInvalid = errors.New("invalid enrollment capability")
	errCapabilityExpired = errors.New("expired enrollment capability")
	errCapabilityRole    = errors.New("enrollment capability role mismatch")
	errIdentityNotFound  = errors.New("identity not found")
	errIdentityLimit     = errors.New("identity role limit reached")
)

type metricsSnapshot struct {
	TotalStreams int64
	ByteCount    int64
	ErrorCounts  map[string]int64
}

type store struct {
	db *sql.DB
}

func createStore(path string) (*store, error) {
	state, err := openDatabase(path)
	if err != nil {
		return nil, err
	}
	if err := state.initialize(context.Background()); err != nil {
		state.Close()
		return nil, err
	}
	return state, nil
}

func openDatabase(path string) (*store, error) {
	// Driver-level pragmas are applied to every replacement connection, not
	// just the first pooled handle opened below.
	dsn, err := sqliteDataSourceName(path)
	if err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite state: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	state := &store{db: database}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("open SQLite state: %w", err)
	}
	if _, err := database.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		database.Close()
		return nil, fmt.Errorf("configure SQLite foreign keys: %w", err)
	}
	if _, err := database.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		database.Close()
		return nil, fmt.Errorf("configure SQLite busy timeout: %w", err)
	}
	return state, nil
}

func sqliteDataSourceName(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve SQLite state path: %w", err)
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	uri := &url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "busy_timeout(5000)")
	uri.RawQuery = query.Encode()
	return uri.String(), nil
}

func openStore(path string) (*store, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect SQLite state: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, errors.New("SQLite state is missing or invalid")
	}
	state, err := openDatabase(path)
	if err != nil {
		return nil, err
	}
	var version int
	if err := state.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		state.Close()
		return nil, fmt.Errorf("read SQLite schema version: %w", err)
	}
	if version <= 0 || version > schemaVersion {
		state.Close()
		return nil, fmt.Errorf("unsupported SQLite schema version %d", version)
	}
	for version < schemaVersion {
		switch version {
		case 1:
			if err := state.migrateFromVersionOne(context.Background()); err != nil {
				state.Close()
				return nil, err
			}
			version = 2
		case 2:
			if err := state.migrateFromVersionTwo(context.Background()); err != nil {
				state.Close()
				return nil, err
			}
			version = 3
		default:
			state.Close()
			return nil, fmt.Errorf("unsupported SQLite schema version %d", version)
		}
	}
	if version != schemaVersion {
		state.Close()
		return nil, fmt.Errorf("unsupported SQLite schema version %d", version)
	}
	return state, nil
}

func (state *store) initialize(ctx context.Context) error {
	if _, err := state.db.ExecContext(ctx, `PRAGMA journal_mode = WAL`); err != nil {
		return fmt.Errorf("initialize SQLite journal: %w", err)
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS identities (
            serial TEXT PRIMARY KEY,
            role TEXT NOT NULL CHECK (role IN ('owner', 'agent', 'client')),
            created_at INTEGER NOT NULL,
            last_seen_at INTEGER,
            revoked_at INTEGER
        ) STRICT`,
		`CREATE TABLE IF NOT EXISTS pairing_capabilities (
            capability_hash BLOB PRIMARY KEY,
            role TEXT NOT NULL CHECK (role IN ('owner', 'agent', 'client')),
            created_at INTEGER NOT NULL,
            expires_at INTEGER NOT NULL,
            consumed_at INTEGER
        ) STRICT`,
		`CREATE TABLE IF NOT EXISTS metrics (
            singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1),
            total_streams INTEGER NOT NULL DEFAULT 0,
            byte_count INTEGER NOT NULL DEFAULT 0
        ) STRICT`,
		`INSERT OR IGNORE INTO metrics(singleton_id) VALUES (1)`,
		`CREATE TABLE IF NOT EXISTS error_metrics (
            code TEXT PRIMARY KEY,
            count INTEGER NOT NULL
        ) STRICT`,
		`CREATE TABLE IF NOT EXISTS settings (
            key TEXT PRIMARY KEY,
            value TEXT NOT NULL
        ) STRICT`,
		`CREATE TABLE IF NOT EXISTS endpoint_migrations (
            capability_hash BLOB PRIMARY KEY,
            relay_url TEXT NOT NULL,
            created_at INTEGER NOT NULL,
            expires_at INTEGER NOT NULL,
            consumed_at INTEGER
        ) STRICT`,
		adminMutationReplaySchema,
	}
	transaction, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite initialization: %w", err)
	}
	defer transaction.Rollback()
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize SQLite state: %w", err)
		}
	}
	if err := validSchemaFromQuery(ctx, transaction); err != nil {
		return fmt.Errorf("validate initialized SQLite state: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("set initialized SQLite schema version: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite initialization: %w", err)
	}
	return nil
}

func (state *store) migrateFromVersionOne(ctx context.Context) error {
	transaction, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS settings (
        key TEXT PRIMARY KEY,
        value TEXT NOT NULL
    ) STRICT`); err != nil {
		return fmt.Errorf("migrate SQLite settings: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS endpoint_migrations (
        capability_hash BLOB PRIMARY KEY,
        relay_url TEXT NOT NULL,
        created_at INTEGER NOT NULL,
        expires_at INTEGER NOT NULL,
        consumed_at INTEGER
    ) STRICT`); err != nil {
		return fmt.Errorf("migrate SQLite endpoint migrations: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 2`); err != nil {
		return fmt.Errorf("update SQLite schema version: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration: %w", err)
	}
	return nil
}

func (state *store) migrateFromVersionTwo(ctx context.Context) error {
	transaction, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite schema migration: %w", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, adminMutationReplaySchema); err != nil {
		return fmt.Errorf("migrate SQLite admin replay journal: %w", err)
	}
	if err := validSchemaFromQuery(ctx, transaction); err != nil {
		return fmt.Errorf("validate migrated SQLite state: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `PRAGMA user_version = 3`); err != nil {
		return fmt.Errorf("update SQLite schema version: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite schema migration: %w", err)
	}
	return nil
}

func (state *store) Close() error {
	if state == nil || state.db == nil {
		return nil
	}
	return state.db.Close()
}

func (state *store) insertCapability(ctx context.Context, hash [sha256.Size]byte, role enrollment.Role, createdAt, expiresAt time.Time) error {
	_, err := state.db.ExecContext(ctx,
		`INSERT INTO pairing_capabilities(capability_hash, role, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hash[:], string(role), createdAt.Unix(), expiresAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("persist enrollment capability: %w", err)
	}
	return nil
}

func (state *store) capabilityCount(ctx context.Context, role string) (int, error) {
	var count int
	err := state.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pairing_capabilities WHERE role = ?`, role,
	).Scan(&count)
	return count, err
}

func (state *store) activeIdentityCount(ctx context.Context, role enrollment.Role) (int, error) {
	var count int
	err := state.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM identities WHERE role = ? AND revoked_at IS NULL`, string(role),
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count active identities: %w", err)
	}
	return count, nil
}

func (state *store) createIdentity(ctx context.Context, serial string, role enrollment.Role, now time.Time) error {
	transaction, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin identity transaction: %w", err)
	}
	defer transaction.Rollback()
	if role == enrollment.RoleClient {
		var count int
		if err := transaction.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM identities WHERE role = ? AND revoked_at IS NULL`, string(role),
		).Scan(&count); err != nil {
			return fmt.Errorf("count active Client identities: %w", err)
		}
		if count >= maximumClientIdentities {
			return errIdentityLimit
		}
	}
	_, err = transaction.ExecContext(ctx,
		`INSERT INTO identities(serial, role, created_at) VALUES (?, ?, ?)`, serial, string(role), now.Unix(),
	)
	if err != nil {
		return fmt.Errorf("persist identity: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit identity: %w", err)
	}
	return nil
}

func (state *store) setRelayURL(ctx context.Context, relayURL string) error {
	_, err := state.db.ExecContext(ctx, `
        INSERT INTO settings(key, value) VALUES ('relay_url', ?)
        ON CONFLICT(key) DO UPDATE SET value = excluded.value`, relayURL,
	)
	if err != nil {
		return fmt.Errorf("persist relay URL: %w", err)
	}
	return nil
}

func (state *store) relayURL(ctx context.Context) (string, error) {
	var relayURL string
	if err := state.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'relay_url'`).Scan(&relayURL); err != nil {
		return "", fmt.Errorf("read relay URL: %w", err)
	}
	return relayURL, nil
}

func (state *store) insertEndpointMigration(ctx context.Context, hash [sha256.Size]byte, relayURL string, createdAt, expiresAt time.Time) error {
	_, err := state.db.ExecContext(ctx,
		`INSERT INTO endpoint_migrations(capability_hash, relay_url, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		hash[:], relayURL, createdAt.Unix(), expiresAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("persist endpoint migration: %w", err)
	}
	return nil
}

func (state *store) consumeEndpointMigration(ctx context.Context, hash [sha256.Size]byte, now time.Time) (string, error) {
	transaction, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin endpoint migration transaction: %w", err)
	}
	defer transaction.Rollback()
	var relayURL string
	var expiresAt int64
	var consumedAt sql.NullInt64
	err = transaction.QueryRowContext(ctx,
		`SELECT relay_url, expires_at, consumed_at FROM endpoint_migrations WHERE capability_hash = ?`, hash[:],
	).Scan(&relayURL, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) || consumedAt.Valid {
		return "", errCapabilityInvalid
	}
	if err != nil {
		return "", fmt.Errorf("read endpoint migration: %w", err)
	}
	if !now.Before(time.Unix(expiresAt, 0)) {
		return "", errCapabilityExpired
	}
	result, err := transaction.ExecContext(ctx,
		`UPDATE endpoint_migrations SET consumed_at = ? WHERE capability_hash = ? AND consumed_at IS NULL`, now.Unix(), hash[:],
	)
	if err != nil {
		return "", fmt.Errorf("consume endpoint migration: %w", err)
	}
	if updated, err := result.RowsAffected(); err != nil || updated != 1 {
		return "", errCapabilityInvalid
	}
	if err := transaction.Commit(); err != nil {
		return "", fmt.Errorf("commit endpoint migration: %w", err)
	}
	return relayURL, nil
}

func (state *store) redeemCapabilityAndCreateIdentity(
	ctx context.Context,
	hash [sha256.Size]byte,
	role enrollment.Role,
	serial string,
	now time.Time,
) error {
	transaction, err := state.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin enrollment transaction: %w", err)
	}
	defer transaction.Rollback()

	var persistedRole string
	var expiresAt int64
	var consumedAt sql.NullInt64
	err = transaction.QueryRowContext(ctx,
		`SELECT role, expires_at, consumed_at FROM pairing_capabilities WHERE capability_hash = ?`, hash[:],
	).Scan(&persistedRole, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) || consumedAt.Valid {
		return errCapabilityInvalid
	}
	if err != nil {
		return fmt.Errorf("read enrollment capability: %w", err)
	}
	if persistedRole != string(role) {
		return errCapabilityRole
	}
	if !now.Before(time.Unix(expiresAt, 0)) {
		return errCapabilityExpired
	}
	if role == enrollment.RoleClient {
		var count int
		if err := transaction.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM identities WHERE role = ? AND revoked_at IS NULL`, string(role),
		).Scan(&count); err != nil {
			return fmt.Errorf("count active Client identities: %w", err)
		}
		if count >= maximumClientIdentities {
			return errIdentityLimit
		}
	}

	result, err := transaction.ExecContext(ctx,
		`UPDATE pairing_capabilities SET consumed_at = ? WHERE capability_hash = ? AND consumed_at IS NULL`,
		now.Unix(), hash[:],
	)
	if err != nil {
		return fmt.Errorf("consume enrollment capability: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errCapabilityInvalid
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO identities(serial, role, created_at) VALUES (?, ?, ?)`, serial, string(role), now.Unix(),
	); err != nil {
		return fmt.Errorf("persist enrolled identity: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit enrollment transaction: %w", err)
	}
	return nil
}

func (state *store) identityStatus(ctx context.Context, serial string) (enrollment.Role, bool, error) {
	var role string
	var revokedAt sql.NullInt64
	err := state.db.QueryRowContext(ctx,
		`SELECT role, revoked_at FROM identities WHERE serial = ?`, serial,
	).Scan(&role, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, errIdentityNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("read identity status: %w", err)
	}
	return enrollment.Role(role), revokedAt.Valid, nil
}

func (state *store) touchIdentity(ctx context.Context, serial string, now time.Time) error {
	_, err := state.db.ExecContext(ctx, `UPDATE identities SET last_seen_at = ? WHERE serial = ?`, now.Unix(), serial)
	return err
}

func (state *store) revokeIdentity(ctx context.Context, serial string, now time.Time) error {
	result, err := state.db.ExecContext(ctx,
		`UPDATE identities SET revoked_at = COALESCE(revoked_at, ?) WHERE serial = ?`, now.Unix(), serial,
	)
	if err != nil {
		return fmt.Errorf("revoke identity: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read revocation result: %w", err)
	}
	if updated != 1 {
		return errIdentityNotFound
	}
	return nil
}

func (state *store) metrics(ctx context.Context) (metricsSnapshot, error) {
	var snapshot metricsSnapshot
	if err := state.db.QueryRowContext(ctx,
		`SELECT total_streams, byte_count FROM metrics WHERE singleton_id = 1`,
	).Scan(&snapshot.TotalStreams, &snapshot.ByteCount); err != nil {
		return metricsSnapshot{}, fmt.Errorf("read aggregate metrics: %w", err)
	}
	snapshot.ErrorCounts = make(map[string]int64)
	rows, err := state.db.QueryContext(ctx, `SELECT code, count FROM error_metrics ORDER BY code`)
	if err != nil {
		return metricsSnapshot{}, fmt.Errorf("read error metrics: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var count int64
		if err := rows.Scan(&code, &count); err != nil {
			return metricsSnapshot{}, err
		}
		snapshot.ErrorCounts[code] = count
	}
	return snapshot, rows.Err()
}

func (state *store) incrementTotalStreams(ctx context.Context) error {
	_, err := state.db.ExecContext(ctx, `UPDATE metrics SET total_streams = total_streams + 1 WHERE singleton_id = 1`)
	if err != nil {
		return fmt.Errorf("increment aggregate stream count: %w", err)
	}
	return nil
}

func (state *store) addBytes(ctx context.Context, count int64) error {
	if count < 0 {
		return errors.New("byte count cannot be negative")
	}
	_, err := state.db.ExecContext(ctx, `UPDATE metrics SET byte_count = byte_count + ? WHERE singleton_id = 1`, count)
	if err != nil {
		return fmt.Errorf("increment aggregate byte count: %w", err)
	}
	return nil
}

func (state *store) incrementError(ctx context.Context, code string) error {
	if !validErrorCode(code) {
		return errors.New("invalid redacted error code")
	}
	_, err := state.db.ExecContext(ctx, `
        INSERT INTO error_metrics(code, count) VALUES (?, 1)
        ON CONFLICT(code) DO UPDATE SET count = count + 1`, code,
	)
	if err != nil {
		return fmt.Errorf("increment redacted error count: %w", err)
	}
	return nil
}

func (state *store) validSchema(ctx context.Context) error {
	return validSchemaFromQuery(ctx, state.db)
}

type schemaQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func validSchemaFromQuery(ctx context.Context, queryer schemaQueryer) error {
	required := map[string]bool{
		"identities": false, "pairing_capabilities": false, "metrics": false, "error_metrics": false,
		"settings": false, "endpoint_migrations": false, "admin_mutation_replay": false,
	}
	requiredAutoindexes := map[string]string{
		"sqlite_autoindex_identities_1":            "identities",
		"sqlite_autoindex_pairing_capabilities_1":  "pairing_capabilities",
		"sqlite_autoindex_error_metrics_1":         "error_metrics",
		"sqlite_autoindex_settings_1":              "settings",
		"sqlite_autoindex_endpoint_migrations_1":   "endpoint_migrations",
		"sqlite_autoindex_admin_mutation_replay_1": "admin_mutation_replay",
	}
	seenAutoindexes := make(map[string]bool, len(requiredAutoindexes))
	rows, err := queryer.QueryContext(ctx, `SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var objectType, name, tableName string
		var createSQL sql.NullString
		if err := rows.Scan(&objectType, &name, &tableName, &createSQL); err != nil {
			rows.Close()
			return err
		}
		switch objectType {
		case "table":
			present, expected := required[name]
			if !expected || present || tableName != name || !createSQL.Valid {
				rows.Close()
				return errors.New("SQLite state has unexpected schema object " + name)
			}
			required[name] = true
		case "index":
			expectedTable, expected := requiredAutoindexes[name]
			if createSQL.Valid || !expected || seenAutoindexes[name] || tableName != expectedTable {
				rows.Close()
				return errors.New("SQLite state has unexpected schema object " + name)
			}
			seenAutoindexes[name] = true
		default:
			rows.Close()
			return errors.New("SQLite state has unexpected schema object " + name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for name, present := range required {
		if !present {
			return errors.New("SQLite state is missing required table " + name)
		}
	}
	for name := range requiredAutoindexes {
		if !seenAutoindexes[name] {
			return errors.New("SQLite state is missing required schema object " + name)
		}
	}
	rows, err = queryer.QueryContext(ctx, `PRAGMA table_info(admin_mutation_replay)`)
	if err != nil {
		return err
	}
	type columnRequirement struct {
		columnType string
		notNull    bool
		primaryKey bool
		present    bool
	}
	wantColumns := map[string]columnRequirement{
		"request_id": {columnType: "TEXT", notNull: true, primaryKey: true},
		"digest":     {columnType: "BLOB", notNull: true},
		"operation":  {columnType: "TEXT", notNull: true},
		"state":      {columnType: "TEXT", notNull: true},
		"response":   {columnType: "BLOB"},
		"created_at": {columnType: "INTEGER", notNull: true},
	}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return err
		}
		requirement, ok := wantColumns[name]
		if !ok {
			rows.Close()
			return errors.New("SQLite admin replay journal has unexpected column " + name)
		}
		if !strings.EqualFold(columnType, requirement.columnType) || (notNull != 0) != requirement.notNull || (primaryKey != 0) != requirement.primaryKey {
			rows.Close()
			return errors.New("SQLite admin replay journal has invalid column " + name)
		}
		requirement.present = true
		wantColumns[name] = requirement
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for name, requirement := range wantColumns {
		if !requirement.present {
			return errors.New("SQLite admin replay journal is missing required column " + name)
		}
	}
	var createSQL string
	if err := queryer.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'admin_mutation_replay'`).Scan(&createSQL); err != nil {
		return err
	}
	if normalizeAdminReplaySchema(createSQL) != normalizeAdminReplaySchema(adminMutationReplaySchema) {
		return errors.New("SQLite admin replay journal has invalid definition")
	}
	return nil
}

func normalizeAdminReplaySchema(statement string) string {
	statement = strings.TrimSuffix(strings.TrimSpace(statement), ";")
	var normalized strings.Builder
	var quote byte
	for index := 0; index < len(statement); index++ {
		character := statement[index]
		if quote == 0 {
			switch character {
			case '\'', '"', '`':
				quote = character
				normalized.WriteByte(character)
			case '[':
				quote = ']'
				normalized.WriteByte(character)
			case ' ', '\t', '\r', '\n', '\f':
				continue
			default:
				if character >= 'A' && character <= 'Z' {
					character += 'a' - 'A'
				}
				normalized.WriteByte(character)
			}
			continue
		}
		normalized.WriteByte(character)
		if character != quote {
			continue
		}
		if quote != ']' && index+1 < len(statement) && statement[index+1] == quote {
			normalized.WriteByte(statement[index+1])
			index++
			continue
		}
		quote = 0
	}
	if quote != 0 {
		return ""
	}
	value := normalized.String()
	const withOptionalClause = "createtableifnotexistsadmin_mutation_replay"
	const withoutOptionalClause = "createtableadmin_mutation_replay"
	if strings.HasPrefix(value, withOptionalClause) {
		value = withoutOptionalClause + strings.TrimPrefix(value, withOptionalClause)
	}
	return value
}
