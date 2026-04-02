package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func EnsureAdminUser(ctx context.Context, database *sql.DB, username, password string) (bool, error) {
	var count int
	if err := database.QueryRowContext(ctx, `select count(*) from users`).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}

	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return false, errors.New("bootstrap admin credentials are required when users table is empty")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}

	if _, err := database.ExecContext(
		ctx,
		`insert into users (username, password_hash, role) values ($1, $2, 'admin')`,
		username,
		string(passwordHash),
	); err != nil {
		return false, err
	}

	return true, nil
}
