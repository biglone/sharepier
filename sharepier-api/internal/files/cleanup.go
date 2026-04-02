package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const uploadSessionCleanupBatchSize = 128

type UploadSessionCleanupResult struct {
	ExpiredSessions    int `json:"expiredSessions"`
	RemovedSessionFile int `json:"removedSessionFile"`
	RemovedOrphanFile  int `json:"removedOrphanFile"`
}

func (s *Service) CleanupExpiredUploadSessions(ctx context.Context) (UploadSessionCleanupResult, error) {
	var result UploadSessionCleanupResult

	for {
		tokens, err := s.deleteExpiredUploadSessionBatch(ctx, uploadSessionCleanupBatchSize)
		if err != nil {
			return result, err
		}
		if len(tokens) == 0 {
			break
		}

		removed, err := s.removeUploadSessionFiles(tokens)
		result.ExpiredSessions += len(tokens)
		result.RemovedSessionFile += removed
		if err != nil {
			return result, err
		}
	}

	removedOrphans, err := s.removeOrphanUploadSessionFiles(ctx)
	result.RemovedOrphanFile += removedOrphans
	if err != nil {
		return result, err
	}

	return result, nil
}

func (s *Service) deleteExpiredUploadSessionBatch(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = uploadSessionCleanupBatchSize
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(
		ctx,
		`with expired as (
			select id
			from upload_sessions
			where expired_at is not null and expired_at <= now()
			order by expired_at asc, id asc
			for update skip locked
			limit $1
		)
		delete from upload_sessions as sessions
		using expired
		where sessions.id = expired.id
		returning sessions.upload_token`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tokens := make([]string, 0, limit)
	for rows.Next() {
		var uploadToken string
		if err := rows.Scan(&uploadToken); err != nil {
			return nil, err
		}
		tokens = append(tokens, uploadToken)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return tokens, nil
}

func (s *Service) removeUploadSessionFiles(uploadTokens []string) (int, error) {
	removedCount := 0
	for _, uploadToken := range uploadTokens {
		err := os.Remove(s.uploadSessionPath(strings.TrimSpace(uploadToken)))
		switch {
		case err == nil:
			removedCount += 1
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return removedCount, err
		}
	}

	return removedCount, nil
}

func (s *Service) removeOrphanUploadSessionFiles(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(s.uploadSessionDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	activeTokens, err := s.loadActiveUploadSessionTokens(ctx)
	if err != nil {
		return 0, err
	}

	removedCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		uploadToken := uploadTokenFromSessionFile(entry.Name())
		if uploadToken == "" {
			continue
		}
		if _, exists := activeTokens[uploadToken]; exists {
			continue
		}

		err := os.Remove(filepath.Join(s.uploadSessionDir(), entry.Name()))
		switch {
		case err == nil:
			removedCount += 1
		case errors.Is(err, os.ErrNotExist):
			continue
		default:
			return removedCount, err
		}
	}

	return removedCount, nil
}

func (s *Service) loadActiveUploadSessionTokens(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `select upload_token from upload_sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	tokens := make(map[string]struct{})
	for rows.Next() {
		var uploadToken string
		if err := rows.Scan(&uploadToken); err != nil {
			return nil, err
		}
		uploadToken = strings.TrimSpace(uploadToken)
		if uploadToken != "" {
			tokens[uploadToken] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return tokens, nil
}

func uploadTokenFromSessionFile(fileName string) string {
	fileName = strings.TrimSpace(fileName)
	if !strings.HasSuffix(fileName, ".part") {
		return ""
	}

	uploadToken := strings.TrimSuffix(fileName, ".part")
	if uploadToken == "" {
		return ""
	}

	return uploadToken
}
