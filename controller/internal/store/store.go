package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/shiva-load-testing/controller/internal/model"
)

type Store struct {
	db *sql.DB
}

type loadTestScanner interface {
	Scan(dest ...any) error
}

type templateScanner interface {
	Scan(dest ...any) error
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Migrate(ctx context.Context) error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			username VARCHAR(255) NOT NULL UNIQUE,
			email VARCHAR(255) NOT NULL UNIQUE,
			hashed_password VARCHAR(255) NOT NULL,
			role VARCHAR(50) NOT NULL DEFAULT 'user',
			must_change_password BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS load_tests (
			id VARCHAR(36) PRIMARY KEY,
			project_name VARCHAR(255) NOT NULL,
			url TEXT NOT NULL,
			status VARCHAR(50) NOT NULL DEFAULT 'pending',
			result_json JSON,
			script_content MEDIUMTEXT,
			config_content MEDIUMTEXT,
			payload_source_json MEDIUMTEXT,
			payload_content MEDIUMTEXT,
			http_method VARCHAR(16) NOT NULL DEFAULT 'GET',
			content_type VARCHAR(255) NOT NULL DEFAULT 'application/json',
			auth_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			auth_mode VARCHAR(64) DEFAULT '',
			auth_token_url TEXT,
			auth_client_id VARCHAR(255) DEFAULT '',
			auth_client_auth_method VARCHAR(32) NOT NULL DEFAULT 'basic',
			auth_refresh_skew_seconds INT NOT NULL DEFAULT 30,
			auth_secret_source VARCHAR(32) DEFAULT '',
			auth_secret_configured BOOLEAN NOT NULL DEFAULT FALSE,
			user_id BIGINT NOT NULL,
			username VARCHAR(255) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS test_templates (
			id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			description TEXT,
			mode VARCHAR(20) NOT NULL DEFAULT 'builder',
			url TEXT,
			stages JSON,
			script_content MEDIUMTEXT,
			config_content MEDIUMTEXT,
			http_method VARCHAR(16) NOT NULL DEFAULT 'GET',
			content_type VARCHAR(255) NOT NULL DEFAULT 'application/json',
			payload_json MEDIUMTEXT,
			payload_target_kib INT NOT NULL DEFAULT 0,
			auth_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			auth_mode VARCHAR(64) DEFAULT '',
			auth_token_url TEXT,
			auth_client_id VARCHAR(255) DEFAULT '',
			auth_client_secret_encrypted MEDIUMTEXT,
			auth_client_auth_method VARCHAR(32) NOT NULL DEFAULT 'basic',
			auth_refresh_skew_seconds INT NOT NULL DEFAULT 30,
			auth_secret_source VARCHAR(32) DEFAULT '',
			auth_secret_configured BOOLEAN NOT NULL DEFAULT FALSE,
			is_system BOOLEAN NOT NULL DEFAULT FALSE,
			user_id BIGINT NOT NULL,
			username VARCHAR(255) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS scheduled_tests (
			id VARCHAR(36) PRIMARY KEY,
			name VARCHAR(255) NOT NULL,
			project_name VARCHAR(255) NOT NULL,
			url TEXT NOT NULL,
			mode VARCHAR(20) NOT NULL DEFAULT 'builder',
			executor VARCHAR(50) NOT NULL DEFAULT 'ramping-vus',
			stages JSON,
			vus INT DEFAULT 0,
			duration VARCHAR(50) DEFAULT '',
			rate INT DEFAULT 0,
			time_unit VARCHAR(10) DEFAULT '1s',
			pre_allocated_vus INT DEFAULT 0,
			max_vus INT DEFAULT 0,
			sleep_seconds DOUBLE DEFAULT 0.5,
			script_content MEDIUMTEXT,
			config_content MEDIUMTEXT,
			http_method VARCHAR(16) NOT NULL DEFAULT 'GET',
			content_type VARCHAR(255) NOT NULL DEFAULT 'application/json',
			payload_json MEDIUMTEXT,
			payload_target_kib INT NOT NULL DEFAULT 0,
			auth_enabled BOOLEAN NOT NULL DEFAULT FALSE,
			auth_mode VARCHAR(64) DEFAULT '',
			auth_token_url TEXT,
			auth_client_id VARCHAR(255) DEFAULT '',
			auth_client_secret_encrypted MEDIUMTEXT,
			auth_client_auth_method VARCHAR(32) NOT NULL DEFAULT 'basic',
			auth_refresh_skew_seconds INT NOT NULL DEFAULT 30,
			auth_secret_source VARCHAR(32) DEFAULT '',
			auth_secret_configured BOOLEAN NOT NULL DEFAULT FALSE,
			scheduled_at DATETIME NOT NULL,
			estimated_duration_s INT NOT NULL,
			timezone VARCHAR(64) NOT NULL DEFAULT 'UTC',
			recurrence_type VARCHAR(20) DEFAULT 'once',
			recurrence_rule VARCHAR(255) DEFAULT '',
			recurrence_end DATETIME DEFAULT NULL,
			skipped_occurrences JSON DEFAULT NULL,
			status VARCHAR(30) NOT NULL DEFAULT 'scheduled',
			paused BOOLEAN NOT NULL DEFAULT FALSE,
			user_id BIGINT NOT NULL,
			username VARCHAR(255) NOT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			INDEX idx_sched_at (scheduled_at),
			INDEX idx_sched_status (status)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS schedule_executions (
			id VARCHAR(36) PRIMARY KEY,
			schedule_id VARCHAR(36) NOT NULL,
			load_test_id VARCHAR(36) DEFAULT NULL,
			status VARCHAR(30) NOT NULL DEFAULT 'pending',
			scheduled_at DATETIME NOT NULL,
			started_at DATETIME DEFAULT NULL,
			ended_at DATETIME DEFAULT NULL,
			error_message TEXT DEFAULT NULL,
			error_detail JSON DEFAULT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (schedule_id) REFERENCES scheduled_tests(id) ON DELETE CASCADE,
			INDEX idx_exec_schedule (schedule_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
		`CREATE TABLE IF NOT EXISTS password_reset_tokens (
			id BIGINT AUTO_INCREMENT PRIMARY KEY,
			user_id BIGINT NOT NULL,
			token_hash VARCHAR(255) NOT NULL,
			expires_at DATETIME NOT NULL,
			used_at DATETIME DEFAULT NULL,
			created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
			UNIQUE KEY uniq_password_reset_token_hash (token_hash),
			INDEX idx_password_reset_user (user_id),
			INDEX idx_password_reset_expires_at (expires_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`,
	}
	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}

	// Migration: add columns if they don't exist (MySQL 8.0 lacks ADD COLUMN IF NOT EXISTS)
	addColumnIfMissing := []struct {
		table string
		col   string
		def   string
	}{
		{"load_tests", "script_content", "MEDIUMTEXT"},
		{"load_tests", "config_content", "MEDIUMTEXT"},
		{"load_tests", "payload_source_json", "MEDIUMTEXT"},
		{"load_tests", "payload_content", "MEDIUMTEXT"},
		{"load_tests", "http_method", "VARCHAR(16) NOT NULL DEFAULT 'GET'"},
		{"load_tests", "content_type", "VARCHAR(255) NOT NULL DEFAULT 'application/json'"},
		{"load_tests", "auth_enabled", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"load_tests", "auth_mode", "VARCHAR(64) DEFAULT ''"},
		{"load_tests", "auth_token_url", "TEXT"},
		{"load_tests", "auth_client_id", "VARCHAR(255) DEFAULT ''"},
		{"load_tests", "auth_client_auth_method", "VARCHAR(32) NOT NULL DEFAULT 'basic'"},
		{"load_tests", "auth_refresh_skew_seconds", "INT NOT NULL DEFAULT 30"},
		{"load_tests", "auth_secret_source", "VARCHAR(32) DEFAULT ''"},
		{"load_tests", "auth_secret_configured", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"test_templates", "http_method", "VARCHAR(16) NOT NULL DEFAULT 'GET'"},
		{"test_templates", "content_type", "VARCHAR(255) NOT NULL DEFAULT 'application/json'"},
		{"test_templates", "payload_json", "MEDIUMTEXT"},
		{"test_templates", "payload_target_kib", "INT NOT NULL DEFAULT 0"},
		{"test_templates", "auth_enabled", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"test_templates", "auth_mode", "VARCHAR(64) DEFAULT ''"},
		{"test_templates", "auth_token_url", "TEXT"},
		{"test_templates", "auth_client_id", "VARCHAR(255) DEFAULT ''"},
		{"test_templates", "auth_client_secret_encrypted", "MEDIUMTEXT"},
		{"test_templates", "auth_client_auth_method", "VARCHAR(32) NOT NULL DEFAULT 'basic'"},
		{"test_templates", "auth_refresh_skew_seconds", "INT NOT NULL DEFAULT 30"},
		{"test_templates", "auth_secret_source", "VARCHAR(32) DEFAULT ''"},
		{"test_templates", "auth_secret_configured", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"test_templates", "is_system", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"scheduled_tests", "http_method", "VARCHAR(16) NOT NULL DEFAULT 'GET'"},
		{"scheduled_tests", "content_type", "VARCHAR(255) NOT NULL DEFAULT 'application/json'"},
		{"scheduled_tests", "payload_json", "MEDIUMTEXT"},
		{"scheduled_tests", "payload_target_kib", "INT NOT NULL DEFAULT 0"},
		{"scheduled_tests", "auth_enabled", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"scheduled_tests", "auth_mode", "VARCHAR(64) DEFAULT ''"},
		{"scheduled_tests", "auth_token_url", "TEXT"},
		{"scheduled_tests", "auth_client_id", "VARCHAR(255) DEFAULT ''"},
		{"scheduled_tests", "auth_client_secret_encrypted", "MEDIUMTEXT"},
		{"scheduled_tests", "auth_client_auth_method", "VARCHAR(32) NOT NULL DEFAULT 'basic'"},
		{"scheduled_tests", "auth_refresh_skew_seconds", "INT NOT NULL DEFAULT 30"},
		{"scheduled_tests", "auth_secret_source", "VARCHAR(32) DEFAULT ''"},
		{"scheduled_tests", "auth_secret_configured", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"scheduled_tests", "skipped_occurrences", "JSON DEFAULT NULL"},
		{"users", "must_change_password", "BOOLEAN NOT NULL DEFAULT FALSE"},
	}
	for _, c := range addColumnIfMissing {
		var count int
		err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`, c.table, c.col,
		).Scan(&count)
		if err != nil {
			return fmt.Errorf("migrate check column %s: %w", c.col, err)
		}
		if count == 0 {
			if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.col, c.def)); err != nil {
				return fmt.Errorf("migrate add column %s: %w", c.col, err)
			}
		}
	}

	return nil
}

// User operations

func (s *Store) CreateUser(ctx context.Context, u *model.User) error {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO users (username, email, hashed_password, role, must_change_password) VALUES (?, ?, ?, ?, ?)`,
		u.Username, u.Email, u.HashedPassword, u.Role, u.MustChangePassword,
	)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	id, _ := res.LastInsertId()
	u.ID = id
	return nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*model.User, error) {
	u := &model.User{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, email, hashed_password, role, must_change_password, created_at, updated_at FROM users WHERE username = ?`,
		username,
	).Scan(&u.ID, &u.Username, &u.Email, &u.HashedPassword, &u.Role, &u.MustChangePassword, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	return u, nil
}

func (s *Store) GetUserByIdentifier(ctx context.Context, identifier string) (*model.User, error) {
	u := &model.User{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, email, hashed_password, role, must_change_password, created_at, updated_at
		 FROM users
		 WHERE username = ? OR email = ?
		 LIMIT 1`,
		identifier, identifier,
	).Scan(&u.ID, &u.Username, &u.Email, &u.HashedPassword, &u.Role, &u.MustChangePassword, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by identifier: %w", err)
	}
	return u, nil
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*model.User, error) {
	u := &model.User{}
	err := s.db.QueryRowContext(ctx,
		`SELECT id, username, email, hashed_password, role, must_change_password, created_at, updated_at FROM users WHERE id = ?`,
		id,
	).Scan(&u.ID, &u.Username, &u.Email, &u.HashedPassword, &u.Role, &u.MustChangePassword, &u.CreatedAt, &u.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

func (s *Store) GetUserMetricsByID(ctx context.Context, userID int64) (model.AdminUserMetrics, error) {
	var metrics model.AdminUserMetrics
	var lastTestAt sql.NullTime

	err := s.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(lt.total_tests, 0) AS total_tests,
			COALESCE(lt.completed_tests, 0) AS completed_tests,
			COALESCE(lt.failed_tests, 0) AS failed_tests,
			lt.last_test_at,
			COALESCE(st.total_schedules, 0) AS total_schedules,
			COALESCE(st.active_schedules, 0) AS active_schedules,
			COALESCE(tt.total_templates, 0) AS total_templates
		FROM users u
		LEFT JOIN (
			SELECT
				user_id,
				COUNT(*) AS total_tests,
				SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) AS completed_tests,
				SUM(CASE WHEN status IN ('failed', 'aborted', 'stopped') THEN 1 ELSE 0 END) AS failed_tests,
				MAX(created_at) AS last_test_at
			FROM load_tests
			WHERE user_id = ?
			GROUP BY user_id
		) lt ON lt.user_id = u.id
		LEFT JOIN (
			SELECT
				user_id,
				COUNT(*) AS total_schedules,
				SUM(CASE WHEN status IN ('scheduled', 'running') AND paused = FALSE THEN 1 ELSE 0 END) AS active_schedules
			FROM scheduled_tests
			WHERE user_id = ?
			GROUP BY user_id
		) st ON st.user_id = u.id
		LEFT JOIN (
			SELECT
				user_id,
				COUNT(*) AS total_templates
			FROM test_templates
			WHERE user_id = ?
			GROUP BY user_id
		) tt ON tt.user_id = u.id
		WHERE u.id = ?`,
		userID, userID, userID, userID,
	).Scan(
		&metrics.TotalTests,
		&metrics.CompletedTests,
		&metrics.FailedTests,
		&lastTestAt,
		&metrics.TotalSchedules,
		&metrics.ActiveSchedules,
		&metrics.TotalTemplates,
	)
	if err == sql.ErrNoRows {
		return metrics, nil
	}
	if err != nil {
		return metrics, fmt.Errorf("get user metrics by id: %w", err)
	}

	if lastTestAt.Valid {
		t := lastTestAt.Time
		metrics.LastTestAt = &t
	}

	return metrics, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]model.AdminUserRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT
			u.id,
			u.username,
			u.email,
			u.role,
			u.must_change_password,
			u.created_at,
			u.updated_at,
			COALESCE(lt.total_tests, 0) AS total_tests,
			COALESCE(lt.completed_tests, 0) AS completed_tests,
			COALESCE(lt.failed_tests, 0) AS failed_tests,
			lt.last_test_at,
			COALESCE(st.total_schedules, 0) AS total_schedules,
			COALESCE(st.active_schedules, 0) AS active_schedules,
			COALESCE(tt.total_templates, 0) AS total_templates
		FROM users u
		LEFT JOIN (
			SELECT
				user_id,
				COUNT(*) AS total_tests,
				SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END) AS completed_tests,
				SUM(CASE WHEN status IN ('failed', 'aborted', 'stopped') THEN 1 ELSE 0 END) AS failed_tests,
				MAX(created_at) AS last_test_at
			FROM load_tests
			GROUP BY user_id
		) lt ON lt.user_id = u.id
		LEFT JOIN (
			SELECT
				user_id,
				COUNT(*) AS total_schedules,
				SUM(CASE WHEN status IN ('scheduled', 'running') AND paused = FALSE THEN 1 ELSE 0 END) AS active_schedules
			FROM scheduled_tests
			GROUP BY user_id
		) st ON st.user_id = u.id
		LEFT JOIN (
			SELECT
				user_id,
				COUNT(*) AS total_templates
			FROM test_templates
			GROUP BY user_id
		) tt ON tt.user_id = u.id
		ORDER BY u.created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []model.AdminUserRow
	for rows.Next() {
		var (
			u          model.AdminUserRow
			lastTestAt sql.NullTime
		)
		if err := rows.Scan(
			&u.ID,
			&u.Username,
			&u.Email,
			&u.Role,
			&u.MustChangePassword,
			&u.CreatedAt,
			&u.UpdatedAt,
			&u.Metrics.TotalTests,
			&u.Metrics.CompletedTests,
			&u.Metrics.FailedTests,
			&lastTestAt,
			&u.Metrics.TotalSchedules,
			&u.Metrics.ActiveSchedules,
			&u.Metrics.TotalTemplates,
		); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		if lastTestAt.Valid {
			t := lastTestAt.Time
			u.Metrics.LastTestAt = &t
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) UpdatePassword(ctx context.Context, userID int64, hashedPassword string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET hashed_password = ?, must_change_password = FALSE WHERE id = ?`,
		hashedPassword, userID,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

func (s *Store) AdminResetPassword(ctx context.Context, userID int64, hashedPassword string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE users SET hashed_password = ?, must_change_password = TRUE WHERE id = ?`,
		hashedPassword, userID,
	)
	if err != nil {
		return fmt.Errorf("admin reset password: %w", err)
	}
	return nil
}

func (s *Store) CreatePasswordResetToken(ctx context.Context, token *model.PasswordResetToken) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password reset token tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM password_reset_tokens WHERE user_id = ? AND used_at IS NULL`,
		token.UserID,
	); err != nil {
		return fmt.Errorf("clear existing password reset tokens: %w", err)
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO password_reset_tokens (user_id, token_hash, expires_at) VALUES (?, ?, ?)`,
		token.UserID, token.TokenHash, token.ExpiresAt.UTC(),
	)
	if err != nil {
		return fmt.Errorf("create password reset token: %w", err)
	}

	tokenID, _ := res.LastInsertId()
	token.ID = tokenID

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password reset token tx: %w", err)
	}
	return nil
}

func (s *Store) ResetPasswordWithToken(ctx context.Context, tokenHash, hashedPassword string) (*model.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin password reset tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var userID int64
	var expiresAt time.Time
	var usedAt sql.NullTime
	if err := tx.QueryRowContext(ctx,
		`SELECT user_id, expires_at, used_at
		 FROM password_reset_tokens
		 WHERE token_hash = ?
		 LIMIT 1`,
		tokenHash,
	).Scan(&userID, &expiresAt, &usedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("load password reset token: %w", err)
	}

	if usedAt.Valid || time.Now().UTC().After(expiresAt.UTC()) {
		return nil, nil
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET hashed_password = ?, must_change_password = FALSE WHERE id = ?`,
		hashedPassword, userID,
	); err != nil {
		return nil, fmt.Errorf("update password from reset token: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE password_reset_tokens SET used_at = ? WHERE token_hash = ?`,
		time.Now().UTC(), tokenHash,
	); err != nil {
		return nil, fmt.Errorf("mark password reset token used: %w", err)
	}

	u := &model.User{}
	if err := tx.QueryRowContext(ctx,
		`SELECT id, username, email, hashed_password, role, must_change_password, created_at, updated_at
		 FROM users
		 WHERE id = ?`,
		userID,
	).Scan(&u.ID, &u.Username, &u.Email, &u.HashedPassword, &u.Role, &u.MustChangePassword, &u.CreatedAt, &u.UpdatedAt); err != nil {
		return nil, fmt.Errorf("load updated user after password reset: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit password reset tx: %w", err)
	}
	return u, nil
}

func (s *Store) UserCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

// LoadTest operations

func (s *Store) CreateLoadTest(ctx context.Context, lt *model.LoadTest) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO load_tests (id, project_name, url, status, script_content, config_content, payload_source_json, payload_content, http_method, content_type,
		 auth_enabled, auth_mode, auth_token_url, auth_client_id, auth_client_auth_method, auth_refresh_skew_seconds, auth_secret_source, auth_secret_configured,
		 user_id, username)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		lt.ID, lt.ProjectName, lt.URL, lt.Status, nullString(lt.ScriptContent), nullString(lt.ConfigContent), nullString(lt.PayloadSourceJSON), nullString(lt.PayloadContent),
		defaultHTTPMethod(lt.HTTPMethod), defaultContentType(lt.ContentType),
		lt.AuthConfig.Enabled, nullString(lt.AuthConfig.Mode), nullString(lt.AuthConfig.TokenURL), nullString(lt.AuthConfig.ClientID),
		defaultAuthClientAuthMethod(lt.AuthConfig.ClientAuthMethod), defaultAuthRefreshSkewSeconds(lt.AuthConfig.RefreshSkewSeconds), nullString(lt.AuthConfig.SecretSource), authConfigured(lt.AuthConfig),
		lt.UserID, lt.Username,
	)
	if err != nil {
		return fmt.Errorf("create load test: %w", err)
	}
	return nil
}

func defaultHTTPMethod(method string) string {
	if method == "" {
		return "GET"
	}
	return method
}

func defaultContentType(contentType string) string {
	if contentType == "" {
		return "application/json"
	}
	return contentType
}

func defaultAuthClientAuthMethod(method string) string {
	if method == "" {
		return "basic"
	}
	return method
}

func defaultAuthRefreshSkewSeconds(skew int) int {
	if skew <= 0 {
		return 30
	}
	return skew
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func authConfigured(cfg model.AuthConfig) bool {
	return cfg.SecretConfigured || cfg.ClientSecretEncrypted != ""
}

func applyAuthConfig(
	cfg *model.AuthConfig,
	enabled bool,
	mode sql.NullString,
	tokenURL sql.NullString,
	clientID sql.NullString,
	clientSecretEncrypted sql.NullString,
	clientAuthMethod string,
	refreshSkew int,
	secretSource sql.NullString,
	secretConfigured bool,
) {
	cfg.Enabled = enabled
	if mode.Valid {
		cfg.Mode = mode.String
	}
	if tokenURL.Valid {
		cfg.TokenURL = tokenURL.String
	}
	if clientID.Valid {
		cfg.ClientID = clientID.String
	}
	if clientSecretEncrypted.Valid {
		cfg.ClientSecretEncrypted = clientSecretEncrypted.String
	}
	cfg.ClientAuthMethod = clientAuthMethod
	cfg.RefreshSkewSeconds = refreshSkew
	if secretSource.Valid {
		cfg.SecretSource = secretSource.String
	}
	cfg.SecretConfigured = secretConfigured || cfg.ClientSecretEncrypted != ""
}

func scanLoadTest(scanner loadTestScanner, lt *model.LoadTest) error {
	var resultJSON, scriptContent, configContent, payloadSourceJSON, payloadContent sql.NullString
	var authMode, authTokenURL, authClientID, authSecretSource sql.NullString
	if err := scanner.Scan(
		&lt.ID, &lt.ProjectName, &lt.URL, &lt.Status, &resultJSON,
		&scriptContent, &configContent, &payloadSourceJSON, &payloadContent, &lt.HTTPMethod, &lt.ContentType,
		&lt.AuthConfig.Enabled, &authMode, &authTokenURL, &authClientID, &lt.AuthConfig.ClientAuthMethod, &lt.AuthConfig.RefreshSkewSeconds, &authSecretSource, &lt.AuthConfig.SecretConfigured,
		&lt.UserID, &lt.Username, &lt.CreatedAt,
	); err != nil {
		return err
	}
	if resultJSON.Valid {
		lt.ResultJSON = json.RawMessage(resultJSON.String)
	}
	if scriptContent.Valid {
		lt.ScriptContent = scriptContent.String
	}
	if configContent.Valid {
		lt.ConfigContent = configContent.String
	}
	if payloadSourceJSON.Valid {
		lt.PayloadSourceJSON = payloadSourceJSON.String
	}
	if payloadContent.Valid {
		lt.PayloadContent = payloadContent.String
	}
	applyAuthConfig(&lt.AuthConfig, lt.AuthConfig.Enabled, authMode, authTokenURL, authClientID, sql.NullString{}, lt.AuthConfig.ClientAuthMethod, lt.AuthConfig.RefreshSkewSeconds, authSecretSource, lt.AuthConfig.SecretConfigured)
	return nil
}

func scanLoadTestSummary(scanner loadTestScanner, lt *model.LoadTest) error {
	var resultJSON sql.NullString
	if err := scanner.Scan(
		&lt.ID, &lt.ProjectName, &lt.URL, &lt.Status, &resultJSON,
		&lt.UserID, &lt.Username, &lt.CreatedAt,
	); err != nil {
		return err
	}
	if resultJSON.Valid {
		lt.ResultJSON = json.RawMessage(resultJSON.String)
	}
	return nil
}

func buildLoadTestListQueries(userID int64, role string, search string) (string, string, []any) {
	var countQuery, listQuery string
	var args []any

	if role == "admin" {
		countQuery = `SELECT COUNT(*) FROM load_tests`
		listQuery = `SELECT id, project_name, url, status, result_json, user_id, username, created_at FROM load_tests`
		if search != "" {
			countQuery += ` WHERE project_name LIKE ? OR url LIKE ?`
			listQuery += ` WHERE project_name LIKE ? OR url LIKE ?`
			s := "%" + search + "%"
			args = append(args, s, s)
		}
		return countQuery, listQuery, args
	}

	countQuery = `SELECT COUNT(*) FROM load_tests WHERE user_id = ?`
	listQuery = `SELECT id, project_name, url, status, result_json, user_id, username, created_at FROM load_tests WHERE user_id = ?`
	args = append(args, userID)
	if search != "" {
		countQuery += ` AND (project_name LIKE ? OR url LIKE ?)`
		listQuery += ` AND (project_name LIKE ? OR url LIKE ?)`
		s := "%" + search + "%"
		args = append(args, s, s)
	}

	return countQuery, listQuery, args
}

func scanTemplate(scanner templateScanner, t *model.TestTemplate) error {
	var stagesJSON, scriptContent, configContent, payloadJSON sql.NullString
	var authMode, authTokenURL, authClientID, authClientSecretEncrypted, authSecretSource sql.NullString
	if err := scanner.Scan(
		&t.ID, &t.Name, &t.Description, &t.Mode, &t.URL, &stagesJSON,
		&scriptContent, &configContent, &t.HTTPMethod, &t.ContentType, &payloadJSON, &t.PayloadTargetKiB,
		&t.AuthConfig.Enabled, &authMode, &authTokenURL, &authClientID, &authClientSecretEncrypted, &t.AuthConfig.ClientAuthMethod, &t.AuthConfig.RefreshSkewSeconds, &authSecretSource, &t.AuthConfig.SecretConfigured,
		&t.System, &t.UserID, &t.Username, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return err
	}
	if stagesJSON.Valid {
		_ = json.Unmarshal([]byte(stagesJSON.String), &t.Stages)
	}
	if scriptContent.Valid {
		t.ScriptContent = scriptContent.String
	}
	if configContent.Valid {
		t.ConfigContent = configContent.String
	}
	if payloadJSON.Valid {
		t.PayloadJSON = payloadJSON.String
	}
	applyAuthConfig(&t.AuthConfig, t.AuthConfig.Enabled, authMode, authTokenURL, authClientID, authClientSecretEncrypted, t.AuthConfig.ClientAuthMethod, t.AuthConfig.RefreshSkewSeconds, authSecretSource, t.AuthConfig.SecretConfigured)
	return nil
}

func (s *Store) UpdateLoadTestResult(ctx context.Context, id string, status string, resultJSON json.RawMessage) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE load_tests SET status = ?, result_json = ? WHERE id = ?`,
		status, resultJSON, id,
	)
	if err != nil {
		return fmt.Errorf("update load test: %w", err)
	}
	return nil
}

func (s *Store) UpdateLoadTestPayloadContent(ctx context.Context, id string, payloadContent string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE load_tests SET payload_content = ? WHERE id = ?`,
		nullString(payloadContent), id,
	)
	if err != nil {
		return fmt.Errorf("update load test payload: %w", err)
	}
	return nil
}

func (s *Store) GetLoadTest(ctx context.Context, id string) (*model.LoadTest, error) {
	lt := &model.LoadTest{}
	err := scanLoadTest(s.db.QueryRowContext(ctx,
		`SELECT id, project_name, url, status, result_json, script_content, config_content, payload_source_json, payload_content, http_method, content_type,
		        auth_enabled, auth_mode, auth_token_url, auth_client_id, auth_client_auth_method, auth_refresh_skew_seconds, auth_secret_source, auth_secret_configured,
		        user_id, username, created_at
		   FROM load_tests WHERE id = ?`,
		id,
	), lt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get load test: %w", err)
	}
	return lt, nil
}

func (s *Store) ListLoadTests(ctx context.Context, userID int64, role string, limit, offset int, search string) ([]model.LoadTest, int, error) {
	countQuery, listQuery, args := buildLoadTestListQueries(userID, role, search)

	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count load tests: %w", err)
	}

	listQuery += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	listArgs := append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list load tests: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tests []model.LoadTest
	for rows.Next() {
		var lt model.LoadTest
		if err := scanLoadTestSummary(rows, &lt); err != nil {
			return nil, 0, fmt.Errorf("scan load test: %w", err)
		}
		tests = append(tests, lt)
	}
	return tests, total, rows.Err()
}

func (s *Store) ResetData(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM load_tests`)
	return err
}

// EnsureAdmin creates the initial admin user if no users exist.
func (s *Store) EnsureAdmin(ctx context.Context, username, email, hashedPassword string) error {
	count, err := s.UserCount(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	u := &model.User{
		Username:       username,
		Email:          email,
		HashedPassword: hashedPassword,
		Role:           "admin",
	}
	return s.CreateUser(ctx, u)
}

// Template operations

func (s *Store) CreateTemplate(ctx context.Context, t *model.TestTemplate) error {
	stagesJSON, _ := json.Marshal(t.Stages)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO test_templates (id, name, description, mode, url, stages, script_content, config_content, http_method, content_type, payload_json, payload_target_kib,
		 auth_enabled, auth_mode, auth_token_url, auth_client_id, auth_client_secret_encrypted, auth_client_auth_method, auth_refresh_skew_seconds, auth_secret_source, auth_secret_configured,
		 is_system, user_id, username)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, t.Description, t.Mode, t.URL, string(stagesJSON),
		nullString(t.ScriptContent), nullString(t.ConfigContent), defaultHTTPMethod(t.HTTPMethod), defaultContentType(t.ContentType), nullString(t.PayloadJSON), t.PayloadTargetKiB,
		t.AuthConfig.Enabled, nullString(t.AuthConfig.Mode), nullString(t.AuthConfig.TokenURL), nullString(t.AuthConfig.ClientID), nullString(t.AuthConfig.ClientSecretEncrypted),
		defaultAuthClientAuthMethod(t.AuthConfig.ClientAuthMethod), defaultAuthRefreshSkewSeconds(t.AuthConfig.RefreshSkewSeconds), nullString(t.AuthConfig.SecretSource), authConfigured(t.AuthConfig),
		t.System, t.UserID, t.Username,
	)
	if err != nil {
		return fmt.Errorf("create template: %w", err)
	}
	return nil
}

func (s *Store) GetTemplate(ctx context.Context, id string) (*model.TestTemplate, error) {
	t := &model.TestTemplate{}
	err := scanTemplate(s.db.QueryRowContext(ctx,
		`SELECT id, name, description, mode, url, stages, script_content, config_content, http_method, content_type, payload_json, payload_target_kib,
		        auth_enabled, auth_mode, auth_token_url, auth_client_id, auth_client_secret_encrypted, auth_client_auth_method, auth_refresh_skew_seconds, auth_secret_source, auth_secret_configured,
		        is_system, user_id, username, created_at, updated_at
		 FROM test_templates WHERE id = ?`, id,
	), t)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get template: %w", err)
	}
	return t, nil
}

func (s *Store) ListTemplates(ctx context.Context, userID int64, role string) ([]model.TestTemplate, error) {
	var query string
	var args []any

	if role == "admin" {
		query = `SELECT id, name, description, mode, url, stages, script_content, config_content, http_method, content_type, payload_json, payload_target_kib,
		         auth_enabled, auth_mode, auth_token_url, auth_client_id, auth_client_secret_encrypted, auth_client_auth_method, auth_refresh_skew_seconds, auth_secret_source, auth_secret_configured,
		         is_system, user_id, username, created_at, updated_at
		         FROM test_templates ORDER BY is_system DESC, updated_at DESC`
	} else {
		query = `SELECT id, name, description, mode, url, stages, script_content, config_content, http_method, content_type, payload_json, payload_target_kib,
		         auth_enabled, auth_mode, auth_token_url, auth_client_id, auth_client_secret_encrypted, auth_client_auth_method, auth_refresh_skew_seconds, auth_secret_source, auth_secret_configured,
		         is_system, user_id, username, created_at, updated_at
		         FROM test_templates WHERE is_system = TRUE OR user_id = ? ORDER BY is_system DESC, updated_at DESC`
		args = append(args, userID)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var templates []model.TestTemplate
	for rows.Next() {
		var t model.TestTemplate
		if err := scanTemplate(rows, &t); err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

func (s *Store) UpdateTemplate(ctx context.Context, t *model.TestTemplate) error {
	stagesJSON, _ := json.Marshal(t.Stages)
	_, err := s.db.ExecContext(ctx,
		`UPDATE test_templates SET name = ?, description = ?, url = ?, stages = ?,
		 script_content = ?, config_content = ?, http_method = ?, content_type = ?, payload_json = ?, payload_target_kib = ?,
		 auth_enabled = ?, auth_mode = ?, auth_token_url = ?, auth_client_id = ?, auth_client_secret_encrypted = ?, auth_client_auth_method = ?, auth_refresh_skew_seconds = ?, auth_secret_source = ?, auth_secret_configured = ?
		 WHERE id = ?`,
		t.Name, t.Description, t.URL, string(stagesJSON),
		nullString(t.ScriptContent), nullString(t.ConfigContent), defaultHTTPMethod(t.HTTPMethod), defaultContentType(t.ContentType), nullString(t.PayloadJSON), t.PayloadTargetKiB,
		t.AuthConfig.Enabled, nullString(t.AuthConfig.Mode), nullString(t.AuthConfig.TokenURL), nullString(t.AuthConfig.ClientID), nullString(t.AuthConfig.ClientSecretEncrypted),
		defaultAuthClientAuthMethod(t.AuthConfig.ClientAuthMethod), defaultAuthRefreshSkewSeconds(t.AuthConfig.RefreshSkewSeconds), nullString(t.AuthConfig.SecretSource), authConfigured(t.AuthConfig),
		t.ID,
	)
	if err != nil {
		return fmt.Errorf("update template: %w", err)
	}
	return nil
}

func (s *Store) DeleteTemplate(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM test_templates WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete template: %w", err)
	}
	return nil
}

// MarkStaleRunningTests transitions any 'running' or 'pending' tests to 'aborted'.
// Called at startup to clean up orphaned tests from a previous crash.
func (s *Store) MarkStaleRunningTests(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE load_tests SET status = 'aborted' WHERE status IN ('running', 'pending')`,
	)
	if err != nil {
		return 0, fmt.Errorf("mark stale tests: %w", err)
	}
	return result.RowsAffected()
}

// --- Scheduled Tests ---

type scheduledTestScanner interface {
	Scan(dest ...any) error
}

type scheduleExecutionScanner interface {
	Scan(dest ...any) error
}

const scheduledTestSelect = `SELECT id, name, project_name, url, mode, executor, stages,
		 vus, duration, rate, time_unit, pre_allocated_vus, max_vus, sleep_seconds,
		 script_content, config_content, http_method, content_type, payload_json, payload_target_kib,
		 auth_enabled, auth_mode, auth_token_url, auth_client_id, auth_client_secret_encrypted, auth_client_auth_method, auth_refresh_skew_seconds, auth_secret_source, auth_secret_configured,
		 scheduled_at, estimated_duration_s, timezone,
		 recurrence_type, recurrence_rule, recurrence_end, skipped_occurrences, status, paused, user_id, username,
		 created_at, updated_at
		 FROM scheduled_tests`

func encodeSkippedOccurrences(values []time.Time) any {
	if len(values) == 0 {
		return nil
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return nil
	}
	return string(payload)
}

func decodeSkippedOccurrences(raw sql.NullString) []time.Time {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	var decoded []time.Time
	if err := json.Unmarshal([]byte(raw.String), &decoded); err != nil {
		return nil
	}
	return decoded
}

func scanScheduledTest(scanner scheduledTestScanner, st *model.ScheduledTest) error {
	var stagesJSON []byte
	var sleepSec sql.NullFloat64
	var recEnd sql.NullTime
	var payloadJSON, authMode, authTokenURL, authClientID, authClientSecretEncrypted, authSecretSource, skippedOccurrences sql.NullString

	if err := scanner.Scan(&st.ID, &st.Name, &st.ProjectName, &st.URL, &st.Mode, &st.Executor, &stagesJSON,
		&st.VUs, &st.Duration, &st.Rate, &st.TimeUnit, &st.PreAllocatedVUs, &st.MaxVUs, &sleepSec,
		&st.ScriptContent, &st.ConfigContent, &st.HTTPMethod, &st.ContentType, &payloadJSON, &st.PayloadTargetKiB,
		&st.AuthConfig.Enabled, &authMode, &authTokenURL, &authClientID, &authClientSecretEncrypted, &st.AuthConfig.ClientAuthMethod, &st.AuthConfig.RefreshSkewSeconds, &authSecretSource, &st.AuthConfig.SecretConfigured,
		&st.ScheduledAt, &st.EstimatedDurationS, &st.Timezone,
		&st.RecurrenceType, &st.RecurrenceRule, &recEnd, &skippedOccurrences, &st.Status, &st.Paused, &st.UserID, &st.Username,
		&st.CreatedAt, &st.UpdatedAt,
	); err != nil {
		return err
	}

	if len(stagesJSON) > 0 {
		_ = json.Unmarshal(stagesJSON, &st.Stages)
	}
	if sleepSec.Valid {
		v := sleepSec.Float64
		st.SleepSeconds = &v
	}
	if payloadJSON.Valid {
		st.PayloadJSON = payloadJSON.String
	}
	applyAuthConfig(&st.AuthConfig, st.AuthConfig.Enabled, authMode, authTokenURL, authClientID, authClientSecretEncrypted, st.AuthConfig.ClientAuthMethod, st.AuthConfig.RefreshSkewSeconds, authSecretSource, st.AuthConfig.SecretConfigured)
	if recEnd.Valid {
		st.RecurrenceEnd = &recEnd.Time
	}
	st.SkippedOccurrences = decodeSkippedOccurrences(skippedOccurrences)

	return nil
}

func (s *Store) getScheduledTestByQuery(ctx context.Context, query string, args ...any) (*model.ScheduledTest, error) {
	st := &model.ScheduledTest{}
	err := scanScheduledTest(s.db.QueryRowContext(ctx, query, args...), st)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return st, nil
}

func (s *Store) listScheduledTestsByQuery(ctx context.Context, query string, args ...any) ([]model.ScheduledTest, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var result []model.ScheduledTest
	for rows.Next() {
		var st model.ScheduledTest
		if err := scanScheduledTest(rows, &st); err != nil {
			return nil, err
		}
		result = append(result, st)
	}

	return result, rows.Err()
}

func scanScheduleExecution(scanner scheduleExecutionScanner, ex *model.ScheduleExecution) error {
	var loadTestID, errMsg, errDetail sql.NullString
	var startedAt, endedAt sql.NullTime

	if err := scanner.Scan(&ex.ID, &ex.ScheduleID, &loadTestID, &ex.Status, &ex.ScheduledAt,
		&startedAt, &endedAt, &errMsg, &errDetail, &ex.CreatedAt); err != nil {
		return err
	}

	if loadTestID.Valid {
		ex.LoadTestID = &loadTestID.String
	}
	if startedAt.Valid {
		ex.StartedAt = &startedAt.Time
	}
	if endedAt.Valid {
		ex.EndedAt = &endedAt.Time
	}
	if errMsg.Valid {
		ex.ErrorMessage = &errMsg.String
	}
	if errDetail.Valid {
		ex.ErrorDetail = &errDetail.String
	}

	return nil
}

func scanRecurringSchedule(scanner scheduledTestScanner, st *model.ScheduledTest) error {
	var recEnd sql.NullTime
	var skippedOccurrences sql.NullString
	if err := scanner.Scan(&st.ID, &st.Name, &st.ProjectName, &st.ScheduledAt,
		&st.EstimatedDurationS, &st.Timezone, &st.RecurrenceType, &st.RecurrenceRule,
		&recEnd, &skippedOccurrences, &st.Status, &st.Username, &st.UserID); err != nil {
		return err
	}
	if recEnd.Valid {
		st.RecurrenceEnd = &recEnd.Time
	}
	st.SkippedOccurrences = decodeSkippedOccurrences(skippedOccurrences)
	return nil
}

func (s *Store) SetTemplateSystem(ctx context.Context, id string, system bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE test_templates SET is_system = ? WHERE id = ?`, system, id)
	if err != nil {
		return fmt.Errorf("set template system: %w", err)
	}
	return nil
}

func (s *Store) ListSystemTemplates(ctx context.Context) ([]model.TestTemplate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, description, mode, url, stages, script_content, config_content, http_method, content_type, payload_json, payload_target_kib,
		        auth_enabled, auth_mode, auth_token_url, auth_client_id, auth_client_secret_encrypted, auth_client_auth_method, auth_refresh_skew_seconds, auth_secret_source, auth_secret_configured,
		        is_system, user_id, username, created_at, updated_at
		   FROM test_templates
		  WHERE is_system = TRUE
		  ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list system templates: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var templates []model.TestTemplate
	for rows.Next() {
		var t model.TestTemplate
		if err := scanTemplate(rows, &t); err != nil {
			return nil, fmt.Errorf("scan system template: %w", err)
		}
		templates = append(templates, t)
	}
	return templates, rows.Err()
}

func (s *Store) CreateSchedule(ctx context.Context, st *model.ScheduledTest) error {
	stagesJSON, _ := json.Marshal(st.Stages)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO scheduled_tests (id, name, project_name, url, mode, executor, stages,
		 vus, duration, rate, time_unit, pre_allocated_vus, max_vus, sleep_seconds,
		 script_content, config_content, http_method, content_type, payload_json, payload_target_kib,
		 auth_enabled, auth_mode, auth_token_url, auth_client_id, auth_client_secret_encrypted, auth_client_auth_method, auth_refresh_skew_seconds, auth_secret_source, auth_secret_configured,
		 scheduled_at, estimated_duration_s, timezone,
		 recurrence_type, recurrence_rule, recurrence_end, skipped_occurrences, status, paused, user_id, username)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		st.ID, st.Name, st.ProjectName, st.URL, st.Mode, st.Executor, string(stagesJSON),
		st.VUs, st.Duration, st.Rate, st.TimeUnit, st.PreAllocatedVUs, st.MaxVUs, st.SleepSeconds,
		st.ScriptContent, st.ConfigContent, defaultHTTPMethod(st.HTTPMethod), defaultContentType(st.ContentType), nullString(st.PayloadJSON), st.PayloadTargetKiB,
		st.AuthConfig.Enabled, nullString(st.AuthConfig.Mode), nullString(st.AuthConfig.TokenURL), nullString(st.AuthConfig.ClientID), nullString(st.AuthConfig.ClientSecretEncrypted),
		defaultAuthClientAuthMethod(st.AuthConfig.ClientAuthMethod), defaultAuthRefreshSkewSeconds(st.AuthConfig.RefreshSkewSeconds), nullString(st.AuthConfig.SecretSource), authConfigured(st.AuthConfig),
		st.ScheduledAt, st.EstimatedDurationS, st.Timezone,
		st.RecurrenceType, st.RecurrenceRule, st.RecurrenceEnd, encodeSkippedOccurrences(st.SkippedOccurrences), st.Status, st.Paused,
		st.UserID, st.Username,
	)
	return err
}

func (s *Store) GetSchedule(ctx context.Context, id string) (*model.ScheduledTest, error) {
	st, err := s.getScheduledTestByQuery(ctx, scheduledTestSelect+` WHERE id = ?`, id)
	if err != nil {
		return nil, fmt.Errorf("get schedule: %w", err)
	}
	return st, nil
}

func (s *Store) ListSchedules(ctx context.Context) ([]model.ScheduledTest, error) {
	result, err := s.listScheduledTestsByQuery(ctx, scheduledTestSelect+` ORDER BY scheduled_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	return result, nil
}

func (s *Store) UpdateSchedule(ctx context.Context, st *model.ScheduledTest) error {
	stagesJSON, _ := json.Marshal(st.Stages)
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tests SET name=?, project_name=?, url=?, mode=?, executor=?, stages=?,
		 vus=?, duration=?, rate=?, time_unit=?, pre_allocated_vus=?, max_vus=?, sleep_seconds=?,
		 script_content=?, config_content=?, http_method=?, content_type=?, payload_json=?, payload_target_kib=?,
		 auth_enabled=?, auth_mode=?, auth_token_url=?, auth_client_id=?, auth_client_secret_encrypted=?, auth_client_auth_method=?, auth_refresh_skew_seconds=?, auth_secret_source=?, auth_secret_configured=?,
		 scheduled_at=?, estimated_duration_s=?, timezone=?,
		 recurrence_type=?, recurrence_rule=?, recurrence_end=?, skipped_occurrences=?, status=?, paused=?
		 WHERE id=?`,
		st.Name, st.ProjectName, st.URL, st.Mode, st.Executor, string(stagesJSON),
		st.VUs, st.Duration, st.Rate, st.TimeUnit, st.PreAllocatedVUs, st.MaxVUs, st.SleepSeconds,
		st.ScriptContent, st.ConfigContent, defaultHTTPMethod(st.HTTPMethod), defaultContentType(st.ContentType), nullString(st.PayloadJSON), st.PayloadTargetKiB,
		st.AuthConfig.Enabled, nullString(st.AuthConfig.Mode), nullString(st.AuthConfig.TokenURL), nullString(st.AuthConfig.ClientID), nullString(st.AuthConfig.ClientSecretEncrypted),
		defaultAuthClientAuthMethod(st.AuthConfig.ClientAuthMethod), defaultAuthRefreshSkewSeconds(st.AuthConfig.RefreshSkewSeconds), nullString(st.AuthConfig.SecretSource), authConfigured(st.AuthConfig),
		st.ScheduledAt, st.EstimatedDurationS, st.Timezone,
		st.RecurrenceType, st.RecurrenceRule, st.RecurrenceEnd, encodeSkippedOccurrences(st.SkippedOccurrences), st.Status, st.Paused,
		st.ID,
	)
	return err
}

func (s *Store) UpdateScheduleStatus(ctx context.Context, id, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tests SET status = ? WHERE id = ?`, status, id)
	return err
}

func (s *Store) UpdateScheduleNextRun(ctx context.Context, id string, nextAt time.Time, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tests SET scheduled_at = ?, status = ? WHERE id = ?`, nextAt, status, id)
	return err
}

func (s *Store) PauseSchedule(ctx context.Context, id string, paused bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tests SET paused = ? WHERE id = ?`, paused, id)
	return err
}

func (s *Store) DeleteSchedule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM scheduled_tests WHERE id = ?`, id)
	return err
}

// GetDueSchedule returns the next scheduled test that is due to run.
// Looks ahead by 15 seconds to account for tick interval.
func (s *Store) GetDueSchedule(ctx context.Context) (*model.ScheduledTest, error) {
	st, err := s.getScheduledTestByQuery(ctx,
		scheduledTestSelect+`
		 WHERE status = 'scheduled' AND paused = FALSE
		 AND scheduled_at <= DATE_ADD(NOW(), INTERVAL 15 SECOND)
		 ORDER BY scheduled_at ASC LIMIT 1`,
	)
	if err != nil {
		return nil, fmt.Errorf("get due schedule: %w", err)
	}
	return st, nil
}

// GetOverlappingSchedules returns schedules whose buffered time windows overlap with the proposed slot.
// estimated_duration_s is already stored with the scheduling buffer applied at write time.
func (s *Store) GetOverlappingSchedules(ctx context.Context, start, end time.Time, excludeID string) ([]model.ScheduledTest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, project_name, scheduled_at, estimated_duration_s, recurrence_type, status, username, user_id
		 FROM scheduled_tests
		 WHERE status IN ('scheduled', 'running')
		 AND paused = FALSE
		 AND id != ?
		 AND scheduled_at < ?
		 AND DATE_ADD(scheduled_at, INTERVAL estimated_duration_s SECOND) > ?
		 ORDER BY scheduled_at ASC`,
		excludeID, end, start,
	)
	if err != nil {
		return nil, fmt.Errorf("get overlapping: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var result []model.ScheduledTest
	for rows.Next() {
		var st model.ScheduledTest
		if err := rows.Scan(&st.ID, &st.Name, &st.ProjectName, &st.ScheduledAt,
			&st.EstimatedDurationS, &st.RecurrenceType, &st.Status, &st.Username, &st.UserID); err != nil {
			return nil, fmt.Errorf("scan overlap: %w", err)
		}
		result = append(result, st)
	}
	return result, rows.Err()
}

// GetRecurringSchedules returns all active recurring schedules for overlap expansion.
func (s *Store) GetRecurringSchedules(ctx context.Context) ([]model.ScheduledTest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, project_name, scheduled_at, estimated_duration_s, timezone,
		 recurrence_type, recurrence_rule, recurrence_end, skipped_occurrences, status, username, user_id
		 FROM scheduled_tests
		 WHERE status = 'scheduled' AND paused = FALSE AND recurrence_type != 'once'
		 ORDER BY scheduled_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("get recurring: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var result []model.ScheduledTest
	for rows.Next() {
		var st model.ScheduledTest
		if err := scanRecurringSchedule(rows, &st); err != nil {
			return nil, fmt.Errorf("scan recurring: %w", err)
		}
		result = append(result, st)
	}
	return result, rows.Err()
}

// --- Schedule Executions ---

func (s *Store) CreateExecution(ctx context.Context, ex *model.ScheduleExecution) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO schedule_executions (id, schedule_id, load_test_id, status, scheduled_at, started_at, ended_at, error_message, error_detail)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		ex.ID, ex.ScheduleID, ex.LoadTestID, ex.Status, ex.ScheduledAt, ex.StartedAt, ex.EndedAt, ex.ErrorMessage, ex.ErrorDetail,
	)
	return err
}

func (s *Store) UpdateExecution(ctx context.Context, id string, status string, loadTestID *string, startedAt, endedAt *time.Time, errMsg, errDetail *string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE schedule_executions SET status=?, load_test_id=?, started_at=?, ended_at=?, error_message=?, error_detail=?
		 WHERE id=?`,
		status, loadTestID, startedAt, endedAt, errMsg, errDetail, id,
	)
	return err
}

func (s *Store) ListExecutions(ctx context.Context, scheduleID string) ([]model.ScheduleExecution, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, schedule_id, load_test_id, status, scheduled_at, started_at, ended_at, error_message, error_detail, created_at
		 FROM schedule_executions WHERE schedule_id = ? ORDER BY created_at DESC LIMIT 50`, scheduleID)
	if err != nil {
		return nil, fmt.Errorf("list executions: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var result []model.ScheduleExecution
	for rows.Next() {
		var ex model.ScheduleExecution
		if err := scanScheduleExecution(rows, &ex); err != nil {
			return nil, fmt.Errorf("scan execution: %w", err)
		}
		result = append(result, ex)
	}
	return result, rows.Err()
}

func (s *Store) CountScheduleExecutions(ctx context.Context, scheduleID string) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schedule_executions WHERE schedule_id = ?`, scheduleID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// CountConsecutiveFailures counts how many of the last N executions for a schedule failed consecutively.
func (s *Store) CountConsecutiveFailures(ctx context.Context, scheduleID string) (int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status FROM schedule_executions WHERE schedule_id = ? ORDER BY created_at DESC LIMIT 10`, scheduleID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var status string
		if err := rows.Scan(&status); err != nil {
			return 0, err
		}
		if status == "failed" {
			count++
		} else {
			break
		}
	}
	return count, rows.Err()
}

// MarkStaleScheduleExecutions marks any running schedule executions as failed.
// Called at startup to clean up after a crash.
func (s *Store) MarkStaleScheduleExecutions(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE schedule_executions SET status = 'failed', error_message = 'controller restart — execution interrupted'
		 WHERE status IN ('running', 'pending')`)
	if err != nil {
		return 0, fmt.Errorf("mark stale executions: %w", err)
	}
	return result.RowsAffected()
}

// ResetStaleRunningSchedules resets schedules stuck in 'running' status back to 'scheduled'.
// Called at startup after crash recovery.
func (s *Store) ResetStaleRunningSchedules(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE scheduled_tests SET status = 'scheduled' WHERE status = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("reset stale schedules: %w", err)
	}
	return result.RowsAffected()
}

// WaitForDB polls until the database is reachable or the context expires.
func WaitForDB(ctx context.Context, db *sql.DB) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := db.PingContext(ctx); err == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}
