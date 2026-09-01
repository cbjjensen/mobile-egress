package service

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStoreSchemaV3FreshAndConstraints(t *testing.T) {
	path := filepath.Join(t.TempDir(), databaseFilename)
	state, err := createStore(path)
	if err != nil {
		t.Fatalf("createStore() error = %v", err)
	}
	defer state.Close()

	var version int
	if err := state.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if version != 3 {
		t.Fatalf("user_version = %d, want 3", version)
	}
	if err := state.validSchema(context.Background()); err != nil {
		t.Fatalf("validSchema() error = %v", err)
	}

	validDigest := make([]byte, 32)
	validID := strings.Repeat("a", 32)
	tests := []struct {
		name      string
		requestID string
		digest    []byte
		operation string
		state     string
		response  []byte
	}{
		{name: "short request ID", requestID: "a", digest: validDigest, operation: "setup", state: "reserved"},
		{name: "uppercase request ID", requestID: strings.Repeat("A", 32), digest: validDigest, operation: "setup", state: "reserved"},
		{name: "nonhex request ID", requestID: strings.Repeat("z", 32), digest: validDigest, operation: "setup", state: "reserved"},
		{name: "short digest", requestID: validID, digest: make([]byte, 31), operation: "setup", state: "reserved"},
		{name: "status operation", requestID: validID, digest: validDigest, operation: "status", state: "reserved"},
		{name: "unknown state", requestID: validID, digest: validDigest, operation: "setup", state: "unknown"},
		{name: "completed without response", requestID: validID, digest: validDigest, operation: "setup", state: "completed"},
		{name: "reserved with response", requestID: validID, digest: validDigest, operation: "setup", state: "reserved", response: []byte(`{"ok":true}`)},
		{name: "empty completed response", requestID: validID, digest: validDigest, operation: "setup", state: "completed", response: []byte{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := state.db.Exec(`INSERT INTO admin_mutation_replay(request_id, digest, operation, state, response, created_at) VALUES (?, ?, ?, ?, ?, 1)`,
				test.requestID, test.digest, test.operation, test.state, test.response)
			if err == nil {
				t.Fatal("invalid replay row was accepted")
			}
		})
	}
}

func TestStoreMigrationChainsPreserveLegacyData(t *testing.T) {
	for _, fromVersion := range []int{1, 2} {
		t.Run(strconv.Itoa(fromVersion)+"_to_3", func(t *testing.T) {
			path := filepath.Join(t.TempDir(), databaseFilename)
			createLegacyStoreFixture(t, path, fromVersion)

			state, err := openStore(path)
			if err != nil {
				t.Fatalf("openStore(v%d) error = %v", fromVersion, err)
			}
			defer state.Close()
			var version int
			if err := state.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
				t.Fatalf("read user_version: %v", err)
			}
			if version != 3 {
				t.Fatalf("user_version = %d, want 3", version)
			}
			var role string
			if err := state.db.QueryRow(`SELECT role FROM identities WHERE serial = 'LEGACY'`).Scan(&role); err != nil {
				t.Fatalf("legacy identity was not preserved: %v", err)
			}
			if role != "owner" {
				t.Fatalf("legacy role = %q, want owner", role)
			}
			if err := state.validSchema(context.Background()); err != nil {
				t.Fatalf("migrated validSchema() error = %v", err)
			}
		})
	}
}

func TestStoreMigrationRejectsUnsupportedVersions(t *testing.T) {
	for _, version := range []int{-1, 0, 4} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), databaseFilename)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`CREATE TABLE marker(value TEXT) STRICT`); err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`PRAGMA user_version = ` + strconv.Itoa(version)); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if state, err := openStore(path); err == nil {
				state.Close()
				t.Fatalf("openStore() accepted schema version %d", version)
			}
		})
	}
}

func TestStoreSchemaV3RejectsMalformedReplayTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), databaseFilename)
	state, err := createStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := state.db.Exec(`DROP TABLE admin_mutation_replay`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`CREATE TABLE admin_mutation_replay(request_id TEXT PRIMARY KEY) STRICT`); err != nil {
		t.Fatal(err)
	}
	if err := state.validSchema(context.Background()); err == nil {
		t.Fatal("validSchema() accepted malformed replay table")
	}
}

func TestStoreMigrationRejectsPreexistingMalformedReplayWithoutAdvancingVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), databaseFilename)
	createLegacyStoreFixture(t, path, 2)
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE admin_mutation_replay(request_id TEXT PRIMARY KEY) STRICT`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	if state, err := openStore(path); err == nil {
		state.Close()
		t.Fatal("openStore() migrated a malformed preexisting replay table")
	}
	database, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var version int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("user_version after rejected migration = %d, want 2", version)
	}
}

func TestStoreMigrationRejectsQuotedLiteralNearMatchWithoutAdvancingVersion(t *testing.T) {
	for _, test := range []struct {
		name string
		ddl  func(string) string
	}{
		{name: "embedded whitespace", ddl: func(schema string) string {
			return strings.Replace(schema, "'setup'", "'set up'", 1)
		}},
		{name: "optional-clause text inside literal", ddl: func(schema string) string {
			schema = strings.Replace(schema, "CREATE TABLE IF NOT EXISTS", "CREATE TABLE", 1)
			return strings.Replace(schema, "'setup'", "'ifnotexistssetup'", 1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), databaseFilename)
			createLegacyStoreFixture(t, path, 2)
			database, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(test.ddl(adminMutationReplaySchema)); err != nil {
				database.Close()
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			if state, err := openStore(path); err == nil {
				state.Close()
				t.Fatal("openStore() migrated replay DDL whose quoted operation literal differs")
			}
			database, err = sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			var version int
			if err := database.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version != 2 {
				t.Fatalf("user_version after quoted-literal rejection = %d, want 2", version)
			}
		})
	}
}

func TestStoreSchemaV3RejectsRelaxedReplayResponseInvariant(t *testing.T) {
	path := filepath.Join(t.TempDir(), databaseFilename)
	state, err := createStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := state.db.Exec(`DROP TABLE admin_mutation_replay`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`CREATE TABLE admin_mutation_replay (
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
              OR response IS NULL)
    ) STRICT`); err != nil {
		t.Fatal(err)
	}
	if err := state.validSchema(context.Background()); err == nil {
		t.Fatal("validSchema() accepted a relaxed response/state invariant")
	}
}

func TestStoreSchemaV3RejectsReplayTableWithoutPrimaryKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), databaseFilename)
	state, err := createStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if _, err := state.db.Exec(`DROP TABLE admin_mutation_replay`); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.Exec(`CREATE TABLE admin_mutation_replay (
        request_id TEXT NOT NULL,
        digest BLOB NOT NULL CHECK(length(digest) = 32),
        operation TEXT NOT NULL CHECK(operation IN ('setup','rotate','repair')),
        state TEXT NOT NULL CHECK(state IN ('reserved','executing','completed','indeterminate')),
        response BLOB,
        created_at INTEGER NOT NULL,
        CHECK(length(request_id) = 32 AND request_id = lower(request_id) AND request_id NOT GLOB '*[^0-9a-f]*'),
        CHECK((state = 'completed' AND response IS NOT NULL AND length(response) BETWEEN 1 AND 524288)
              OR (state <> 'completed' AND response IS NULL))
    ) STRICT`); err != nil {
		t.Fatal(err)
	}
	if err := state.validSchema(context.Background()); err == nil {
		t.Fatal("validSchema() accepted replay table without request_id primary key")
	}
}

func TestStoreConfiguresEverySQLiteConnection(t *testing.T) {
	state, err := createStore(filepath.Join(t.TempDir(), databaseFilename))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	state.db.SetMaxIdleConns(0)
	for attempt := 0; attempt < 3; attempt++ {
		connection, err := state.db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var foreignKeys, busyTimeout int
		if err := connection.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			connection.Close()
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(context.Background(), `PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
			connection.Close()
			t.Fatal(err)
		}
		connection.Close()
		if foreignKeys != 1 || busyTimeout != 5000 {
			t.Fatalf("connection %d pragmas = foreign_keys %d, busy_timeout %d", attempt, foreignKeys, busyTimeout)
		}
	}
}

func TestStoreDSNPreservesSpecialFilesystemPath(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "state # percent%")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "relay #100%.db")
	state, err := createStore(path)
	if err != nil {
		t.Fatalf("createStore(special path) error = %v", err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		t.Fatalf("exact database path was not preserved: info=%v err=%v", info, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(path) {
			t.Fatalf("DSN created unexpected sibling %q", entry.Name())
		}
	}
}

func createLegacyStoreFixture(t *testing.T, path string, version int) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	statements := []string{
		`CREATE TABLE identities (serial TEXT PRIMARY KEY, role TEXT NOT NULL CHECK (role IN ('owner', 'agent', 'client')), created_at INTEGER NOT NULL, last_seen_at INTEGER, revoked_at INTEGER) STRICT`,
		`CREATE TABLE pairing_capabilities (capability_hash BLOB PRIMARY KEY, role TEXT NOT NULL CHECK (role IN ('owner', 'agent', 'client')), created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, consumed_at INTEGER) STRICT`,
		`CREATE TABLE metrics (singleton_id INTEGER PRIMARY KEY CHECK (singleton_id = 1), total_streams INTEGER NOT NULL DEFAULT 0, byte_count INTEGER NOT NULL DEFAULT 0) STRICT`,
		`INSERT INTO metrics(singleton_id) VALUES (1)`,
		`CREATE TABLE error_metrics (code TEXT PRIMARY KEY, count INTEGER NOT NULL) STRICT`,
		`INSERT INTO identities(serial, role, created_at) VALUES ('LEGACY', 'owner', 1)`,
	}
	if version >= 2 {
		statements = append(statements,
			`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT`,
			`CREATE TABLE endpoint_migrations (capability_hash BLOB PRIMARY KEY, relay_url TEXT NOT NULL, created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, consumed_at INTEGER) STRICT`,
		)
	}
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("fixture statement failed: %v", err)
		}
	}
	if _, err := database.Exec(`PRAGMA user_version = ` + strconv.Itoa(version)); err != nil {
		t.Fatal(err)
	}
}
